// Sidecar inferd installer.
//
// thlibo v0.6+ is pure middleware: inference moved to the separate
// inferd daemon. This file owns the "make sure inferd is up before
// thlibo install completes" path.
//
// Design philosophy:
//
//	If inferd is already running on this machine, use it.
//	If inferd is installed but not running, start it.
//	If inferd is not installed, fetch it and run inferd's
//	  own bundled installer (thlibo doesn't manage inferd's
//	  config — inferd owns its own contract).
//
// State machine in InstallInferd:
//
//	[ probe admin socket ] ── reachable ──► UsedExisting → done
//	          │
//	          unreachable
//	          ▼
//	[ probe binary on PATH + known dirs ] ── found ──► start it ──► StartedExisting → done
//	          │
//	          not found
//	          ▼
//	[ download tarball + run inferd's installer ] ──► InstalledFresh → done
//
// Things this file deliberately does NOT do:
//
//   - Modify inferd's plist / systemd unit.
//   - Configure --backend, --model-path, or any inferd flag. Inferd
//     decides its own config from its own installer + env vars.
//     Users who want a specific backend run `INFERD_BACKEND=llamacpp
//     <inferd-installer>` themselves; thlibo doesn't second-guess.
//   - Reinstall inferd if it's already there. Latest-version chasing
//     is the user's choice via their package manager.

package install

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// inferdLatestURL returns the JSON for the most recent
	// non-prerelease tag.
	inferdLatestURL = "https://api.github.com/repos/3rg0n/inferd/releases/latest"

	// inferdReleaseDLBase is the per-tag asset download root.
	inferdReleaseDLBase = "https://github.com/3rg0n/inferd/releases/download"

	// cosignIdentityRegexp / cosignIssuer pin the OIDC identity
	// that's allowed to sign inferd releases. The pattern matches
	// inferd's release.yml at any v* tag.
	cosignIdentityRegexp = `^https://github\.com/3rg0n/inferd/\.github/workflows/release\.ya?ml@refs/tags/v.+$`
	cosignIssuer         = "https://token.actions.githubusercontent.com"
)

// InferdInstallSpec captures what the installer needs to know.
type InferdInstallSpec struct {
	// Version pins the inferd tag to install when a fresh install
	// is needed. Empty means "fetch latest from GitHub Releases."
	// Ignored when an existing inferd is found running or installed.
	Version string
}

// InferdInstallResult reports what the orchestrator did.
type InferdInstallResult struct {
	// Skipped: the user passed --skip-inferd. No probing, no
	// install. Other fields are zero.
	Skipped bool

	// UsedExisting: inferd was already reachable when thlibo
	// install ran. No download, no start, no config write.
	UsedExisting bool

	// StartedExisting: an inferd binary was already on disk but
	// wasn't running. thlibo started it via the platform's
	// service manager (systemctl --user / launchctl bootstrap /
	// sc start).
	StartedExisting bool

	// InstalledFresh: no inferd was found, so thlibo downloaded
	// the tarball and ran inferd's bundled installer
	// (packaging/install-launchagent.sh on macOS, etc).
	InstalledFresh bool

	// ResolvedVersion: the version that ended up active. For
	// UsedExisting, this is whatever the running daemon reported
	// in the admin status frame (best-effort; may be empty if
	// inferd doesn't include it). For StartedExisting and
	// InstalledFresh, this is whatever the binary was tagged at.
	ResolvedVersion string

	// CosignVerified: only meaningful on the InstalledFresh path.
	// True if cosign was on PATH and the .cosign.bundle validated.
	CosignVerified bool

	// Notes: human-readable lines surfaced by the orchestrator.
	// The installer prints them inline; users see them in the
	// `thlibo install` output.
	Notes []string
}

// PullOptions matches the shape v0.5's PullEngine used so callers
// can swap implementations cleanly.
type PullOptions struct {
	Progress ProgressFunc
}

// ProgressFunc is the per-byte progress callback signature.
// total may be 0 if the server did not send Content-Length.
type ProgressFunc func(written, total int64)

// InstallInferd is the orchestrator. See the file-level state
// machine for the flow.
func InstallInferd(spec InferdInstallSpec, opts PullOptions) (InferdInstallResult, error) {
	var r InferdInstallResult
	platform := currentPlatform()
	if !platformSupported(platform) {
		return r, fmt.Errorf("inferd: %s not supported by inferd's release matrix", platform)
	}

	// 1. Probe: is inferd already reachable?
	probeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if reachable, status, version := probeInferdAdmin(probeCtx); reachable {
		r.UsedExisting = true
		r.ResolvedVersion = version
		if status != "" && status != "ready" {
			r.Notes = append(r.Notes,
				fmt.Sprintf("inferd is %s; thlibo will fail open until ready", status))
		}
		return r, nil
	}

	// 2. Probe: is the inferd-daemon binary already on disk?
	if binPath := findInstalledInferdBinary(); binPath != "" {
		if err := startInstalledInferd(binPath, &r); err != nil {
			r.Notes = append(r.Notes,
				fmt.Sprintf("found %s but couldn't start it: %v", binPath, err))
			r.Notes = append(r.Notes,
				"start inferd manually (systemctl / launchctl / sc) before re-running thlibo install")
			return r, nil
		}
		r.StartedExisting = true
		r.ResolvedVersion = readBinaryVersion(binPath)
		return r, nil
	}

	// 3. Fresh install: download the tarball + run inferd's installer.
	version := spec.Version
	if version == "" {
		v, err := fetchLatestInferdTag()
		if err != nil {
			return r, fmt.Errorf("inferd: resolve latest: %w", err)
		}
		version = v
	}
	r.ResolvedVersion = version

	extractDir, err := pullInferd(version, opts)
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(extractDir) // #nosec G104 -- best-effort temp cleanup

	// Optional cosign verify.
	if v, note := tryCosignVerify(version, extractDir); v {
		r.CosignVerified = true
	} else if note != "" {
		r.Notes = append(r.Notes, note)
	}

	if err := runInferdInstaller(extractDir, &r); err != nil {
		// ErrInferdNeedsManualStep: thlibo did everything it
		// could, but the final activation needs the user (e.g.
		// elevated install.ps1 on Windows). Record it as a
		// partial-success; don't surface as an error to the
		// caller because the middleware is still healthy.
		if errors.Is(err, ErrInferdNeedsManualStep) {
			return r, nil
		}
		return r, err
	}
	r.InstalledFresh = true
	return r, nil
}

// probeInferdAdmin connects to inferd's admin socket and reads one
// status frame. Returns (reachable, current-status, version-if-known).
//
// The admin socket binds before the inference socket per protocol-v1,
// so reachable on admin is a stronger signal than reachable on
// infer.sock. We accept any non-error status as "inferd is up";
// reporting `loading_model` or `restarting` to the caller as a Note
// is the orchestrator's job.
func probeInferdAdmin(ctx context.Context) (reachable bool, status, version string) {
	addr := defaultInferdAdminAddr()
	if addr == "" {
		return false, "", ""
	}

	conn, err := dialInferdAdmin(ctx, addr)
	if err != nil {
		return false, "", ""
	}
	defer conn.Close()

	// Read up to one status frame. The daemon writes the snapshot
	// immediately on connect; if it doesn't show up in the deadline
	// we treat the daemon as unhealthy enough to skip.
	_ = conn.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
	buf := make([]byte, 8192)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		// Connection succeeded but no frame — daemon process is
		// up enough to bind the socket but not far enough to
		// publish a status. Treat as reachable; let the caller
		// surface the partial-readiness if it matters.
		return true, "", ""
	}

	// Frames are NDJSON; we may have read more than one in the
	// buffer. Use the first complete line.
	line := buf[:n]
	if idx := indexByteFrame(line); idx >= 0 {
		line = line[:idx]
	}
	var frame struct {
		Status  string `json:"status"`
		Detail  any    `json:"detail,omitempty"`
		Version string `json:"version,omitempty"`
	}
	if err := json.Unmarshal(line, &frame); err != nil {
		return true, "", ""
	}
	return true, frame.Status, frame.Version
}

func indexByteFrame(b []byte) int {
	for i, c := range b {
		if c == '\n' {
			return i
		}
	}
	return -1
}

// dialInferdAdmin connects to inferd's admin socket on this platform.
// Linux/macOS: Unix domain socket. Windows: named pipe.
func dialInferdAdmin(ctx context.Context, addr string) (net.Conn, error) {
	if runtime.GOOS == "windows" {
		// Use the same os.OpenFile retry shape as inferdcli's
		// pipe dialer; can't import inferdcli here without a
		// cycle. Best-effort, short timeout.
		return openWindowsPipe(ctx, addr)
	}
	var d net.Dialer
	return d.DialContext(ctx, "unix", addr)
}

// defaultInferdAdminAddr returns the admin socket location inferd
// publishes per protocol-v1. Mirrors inferdcli.DefaultAdminAddress
// without the import dependency.
func defaultInferdAdminAddr() string {
	switch runtime.GOOS {
	case "linux":
		if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
			return filepath.Join(d, "inferd", "admin.sock")
		}
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, ".inferd", "run", "admin.sock")
		}
		return filepath.Join(os.TempDir(), "inferd", "admin.sock")
	case "darwin":
		return filepath.Join(os.TempDir(), "inferd", "admin.sock")
	case "windows":
		return `\\.\pipe\inferd-admin`
	}
	return ""
}

// findInstalledInferdBinary returns the path to inferd-daemon if it's
// present in any of the canonical locations. Empty string means not
// found.
func findInstalledInferdBinary() string {
	name := inferdBinName()
	candidates := []string{}

	// $PATH lookup first — covers brew, apt, manual installs.
	if p, err := exec.LookPath(name); err == nil {
		return p
	}

	// Per-user known dirs (the spots inferd's own installer drops
	// the binary into).
	home, err := os.UserHomeDir()
	if err == nil {
		switch runtime.GOOS {
		case "linux", "darwin":
			candidates = append(candidates, filepath.Join(home, ".local", "bin", name))
			candidates = append(candidates, "/usr/local/bin/"+name)
		case "windows":
			if appData := os.Getenv("LOCALAPPDATA"); appData != "" {
				candidates = append(candidates, filepath.Join(appData, "inferd", "bin", name))
			}
		}
	}

	for _, c := range candidates {
		// #nosec G703 -- c is built from constant subpath + os.UserHomeDir / $LOCALAPPDATA; not user input
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

// startInstalledInferd asks the platform's service manager to bring
// up the existing inferd binary. Assumes the unit / plist / Windows
// service is already registered (true if inferd was installed via
// its own installer previously).
func startInstalledInferd(binPath string, r *InferdInstallResult) error {
	switch runtime.GOOS {
	case "linux":
		// Try the user systemd unit first. If the unit doesn't
		// exist, fall back to running the daemon in the background
		// and let the caller deal with persistence.
		if err := runCommand("systemctl", "--user", "start", "inferd.service"); err == nil {
			r.Notes = append(r.Notes, "started inferd via `systemctl --user start inferd`")
			return nil
		}
		// Fall back: spawn the binary detached.
		// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
		cmd := exec.Command(binPath) // #nosec G204 -- binPath came from findInstalledInferdBinary
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("spawn inferd: %w", err)
		}
		r.Notes = append(r.Notes,
			"systemd unit not registered; started inferd as a detached process — register with `inferd's installer` for persistence")
		return nil
	case "darwin":
		// Bootstrap the LaunchAgent. If the plist isn't there,
		// fail loud — inferd's own installer was supposed to put
		// it there.
		home, _ := os.UserHomeDir()
		plist := filepath.Join(home, "Library", "LaunchAgents", "io.inferd.daemon.plist")
		if _, err := os.Stat(plist); err != nil {
			return fmt.Errorf("inferd: LaunchAgent plist missing at %s; run inferd's installer", plist)
		}
		uid := fmt.Sprintf("%d", os.Getuid())
		// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
		if err := runCommand("launchctl", "bootstrap", "gui/"+uid, plist); err != nil {
			// Already-loaded? Try a kickstart.
			_ = runCommand("launchctl", "kickstart", "gui/"+uid+"/io.inferd.daemon")
		}
		r.Notes = append(r.Notes, "started inferd via `launchctl bootstrap`")
		return nil
	case "windows":
		// Try `sc.exe start`. If the service isn't registered we
		// can't auto-register it (inferd's install.ps1 needs
		// admin), so we surface a clear note.
		if err := runCommand("sc.exe", "start", "inferd-daemon"); err == nil {
			r.Notes = append(r.Notes, "started inferd via `sc.exe start inferd-daemon`")
			return nil
		}
		return fmt.Errorf("inferd: 'sc.exe start inferd-daemon' failed; run inferd's install.ps1 elevated")
	}
	return fmt.Errorf("inferd: don't know how to start on %s", runtime.GOOS)
}

// readBinaryVersion shells the binary with --version and returns the
// version string it prints. Returns empty on any failure.
func readBinaryVersion(binPath string) string {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	out, err := exec.Command(binPath, "--version").Output() // #nosec G204 -- binPath is from findInstalledInferdBinary
	if err != nil {
		return ""
	}
	// Format is "inferd-daemon 0.1.12" — just return the trailing
	// token so callers can compare or display it.
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// runInferdInstaller invokes inferd's bundled platform installer.
//   - macOS: packaging/install-launchagent.sh (substitutes plist
//     placeholders, creates ~/Library/Logs/inferd, bootstraps the
//     LaunchAgent).
//   - Linux: there's no bundled install-systemd.sh in v0.1.12; we
//     install the unit ourselves (just copy file) and start it.
//   - Windows: packaging\install.ps1 (needs admin).
func runInferdInstaller(extractDir string, r *InferdInstallResult) error {
	switch runtime.GOOS {
	case "darwin":
		script := filepath.Join(extractDir, "packaging", "install-launchagent.sh")
		if _, err := os.Stat(script); err != nil {
			return fmt.Errorf("inferd: tarball missing packaging/install-launchagent.sh: %w", err)
		}
		// Pass the path to the binary inside the extract dir as
		// the script's argument; it'll resolve __BIN__ to that.
		bin := filepath.Join(extractDir, inferdBinName())
		if _, err := os.Stat(bin); err != nil {
			return fmt.Errorf("inferd: tarball missing %s: %w", inferdBinName(), err)
		}
		// First copy the binary somewhere stable — the script
		// substitutes __BIN__ with the path we pass, so it has to
		// be a path that survives extractDir cleanup.
		home, _ := os.UserHomeDir()
		stableBin := filepath.Join(home, ".local", "bin", inferdBinName())
		if err := os.MkdirAll(filepath.Dir(stableBin), 0o750); err != nil {
			return fmt.Errorf("inferd: create %s: %w", filepath.Dir(stableBin), err)
		}
		if err := copyFile(bin, stableBin, 0o750); err != nil {
			return err
		}
		// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
		cmd := exec.Command(script, stableBin) // #nosec G204 -- script + path are from our extract tree
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("inferd: install-launchagent.sh: %w", err)
		}
		r.Notes = append(r.Notes,
			fmt.Sprintf("installed inferd via packaging/install-launchagent.sh (binary at %s)", stableBin))
		return nil

	case "linux":
		// Inferd v0.1.12 doesn't bundle an install-systemd.sh, so
		// thlibo does the minimal job: drop the binary at
		// ~/.local/bin, install the unit at
		// ~/.config/systemd/user, daemon-reload + enable + start.
		// This stays the smallest possible "do what inferd's
		// future installer would do."
		home, _ := os.UserHomeDir()
		bin := filepath.Join(extractDir, inferdBinName())
		stableBin := filepath.Join(home, ".local", "bin", inferdBinName())
		if err := os.MkdirAll(filepath.Dir(stableBin), 0o750); err != nil {
			return fmt.Errorf("inferd: create %s: %w", filepath.Dir(stableBin), err)
		}
		if err := copyFile(bin, stableBin, 0o750); err != nil {
			return err
		}
		unitSrc := filepath.Join(extractDir, "packaging", "inferd.service")
		unitDst := filepath.Join(home, ".config", "systemd", "user", "inferd.service")
		if err := os.MkdirAll(filepath.Dir(unitDst), 0o750); err != nil {
			return fmt.Errorf("inferd: create unit dir: %w", err)
		}
		if err := copyFile(unitSrc, unitDst, 0o644); err != nil {
			return err
		}
		_ = runCommand("systemctl", "--user", "daemon-reload")
		if err := runCommand("systemctl", "--user", "enable", "--now", "inferd.service"); err != nil {
			return fmt.Errorf("inferd: systemctl enable --now: %w", err)
		}
		r.Notes = append(r.Notes,
			fmt.Sprintf("installed inferd at %s and enabled inferd.service", stableBin))
		return nil

	case "windows":
		// Inferd's Windows install.ps1 needs admin (it registers a
		// service via sc.exe). thlibo runs as the regular user, so
		// we can't auto-execute it.
		//
		// What we CAN do: copy the binary + the install script to
		// a stable location (out of our temp extract dir, which is
		// about to be RemoveAll'd by the caller's defer), then
		// surface clear instructions for the user to run install.ps1
		// elevated. Without this, the path we'd print would point
		// at a temp dir we just deleted.
		script := filepath.Join(extractDir, "packaging", "install.ps1")
		if _, err := os.Stat(script); err != nil {
			return fmt.Errorf("inferd: tarball missing packaging\\install.ps1: %w", err)
		}
		bin := filepath.Join(extractDir, inferdBinName())
		if _, err := os.Stat(bin); err != nil {
			return fmt.Errorf("inferd: tarball missing %s: %w", inferdBinName(), err)
		}

		appData := os.Getenv("LOCALAPPDATA")
		if appData == "" {
			return fmt.Errorf("inferd: LOCALAPPDATA env var not set; can't pick stable install dir")
		}
		stableDir := filepath.Join(appData, "inferd", "bin")
		stableBin := filepath.Join(stableDir, inferdBinName())
		stableScript := filepath.Join(appData, "inferd", "install.ps1")
		// #nosec G703 -- stableDir is %LOCALAPPDATA% + literal subpath; not user input
		if err := os.MkdirAll(stableDir, 0o750); err != nil {
			return fmt.Errorf("inferd: create %s: %w", stableDir, err)
		}
		if err := copyFile(bin, stableBin, 0o750); err != nil {
			return err
		}
		if err := copyFile(script, stableScript, 0o750); err != nil {
			return err
		}

		r.Notes = append(r.Notes,
			fmt.Sprintf("inferd binary copied to %s", stableBin))
		r.Notes = append(r.Notes,
			fmt.Sprintf("install.ps1 copied to %s", stableScript))
		r.Notes = append(r.Notes,
			"Windows install.ps1 needs admin to register the service.")
		r.Notes = append(r.Notes,
			fmt.Sprintf("From an ELEVATED PowerShell, run: & '%s'", stableScript))

		// Return ErrInferdNeedsManualStep so the orchestrator
		// records this as "binary placed, manual step required"
		// rather than "fully installed."
		return ErrInferdNeedsManualStep
	}
	return fmt.Errorf("inferd: don't know how to install on %s", runtime.GOOS)
}

// ErrInferdNeedsManualStep is a sentinel returned from
// runInferdInstaller when thlibo did everything it could but the
// final activation step needs the user (e.g. running install.ps1
// elevated on Windows). The orchestrator catches this specifically
// to record InstalledFresh=false but still treat the install as
// "ok, just incomplete."
var ErrInferdNeedsManualStep = errors.New("inferd: manual step required")

// runCommand wraps exec.Command with stdout/stderr inherited so the
// user sees the underlying tool's output.
func runCommand(name string, args ...string) error {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd := exec.Command(name, args...) // #nosec G204 -- name + args are constants from this package
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// fetchLatestInferdTag hits the GitHub Releases API and returns the
// most recent non-prerelease tag.
func fetchLatestInferdTag() (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodGet, inferdLatestURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.TagName == "" {
		return "", errors.New("GitHub API returned empty tag_name")
	}
	if payload.Prerelease {
		return "", fmt.Errorf("GitHub returned prerelease %s; pass --inferd-version to install a prerelease", payload.TagName)
	}
	return payload.TagName, nil
}

// pullInferd downloads + extracts the tarball/zip for version into a
// fresh temp directory.
func pullInferd(version string, opts PullOptions) (string, error) {
	platform := currentPlatform()
	asset := assetNameFor(version, platform)
	if asset == "" {
		return "", fmt.Errorf("inferd: no asset name pattern for %s", platform)
	}
	url := fmt.Sprintf("%s/%s/%s", inferdReleaseDLBase, version, asset)

	tmpDir, err := os.MkdirTemp("", "thlibo-inferd-*")
	if err != nil {
		return "", fmt.Errorf("inferd: create temp dir: %w", err)
	}
	archivePath := filepath.Join(tmpDir, asset)
	if err := download(url, archivePath, opts.Progress); err != nil {
		return "", err
	}
	if _, err := sha256OfFile(archivePath); err != nil {
		return "", err
	}

	bundleURL := url + ".cosign.bundle"
	bundlePath := archivePath + ".cosign.bundle"
	if err := download(bundleURL, bundlePath, nil); err != nil {
		_ = os.Remove(bundlePath)
	}

	extractDir := filepath.Join(tmpDir, "extracted")
	if err := os.MkdirAll(extractDir, 0o750); err != nil {
		return "", fmt.Errorf("inferd: create extract dir: %w", err)
	}
	if strings.HasSuffix(asset, ".zip") {
		if err := extractZip(archivePath, extractDir); err != nil {
			return "", err
		}
	} else {
		if err := extractTarGz(archivePath, extractDir); err != nil {
			return "", err
		}
	}
	return extractDir, nil
}

// tryCosignVerify runs `cosign verify-blob` against the bundle if
// cosign is on PATH.
func tryCosignVerify(version, extractDir string) (bool, string) {
	cosignPath, err := exec.LookPath("cosign")
	if err != nil {
		return false, "cosign not on PATH; skipped signature verify (HTTPS trust only)"
	}
	parent := filepath.Dir(extractDir)
	platform := currentPlatform()
	asset := assetNameFor(version, platform)
	bundle := filepath.Join(parent, asset+".cosign.bundle")
	tarball := filepath.Join(parent, asset)
	if _, err := os.Stat(bundle); err != nil {
		return false, ".cosign.bundle missing for this release; HTTPS trust only"
	}
	// #nosec G204 -- cosignPath is from exec.LookPath; flags are constants
	cmd := exec.Command(cosignPath,
		"verify-blob",
		"--bundle", bundle,
		"--certificate-identity-regexp", cosignIdentityRegexp,
		"--certificate-oidc-issuer", cosignIssuer,
		tarball,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Sprintf("cosign verify failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return true, ""
}

// download streams url to dest. 200 MB cap is generous for inferd
// (~3-9 MB tarballs).
func download(url, dest string, progress ProgressFunc) error {
	const maxBytes = 200 << 20
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("inferd: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("inferd: download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("inferd: download %s: HTTP %d", url, resp.StatusCode)
	}

	out, err := os.Create(dest) // #nosec G304 -- dest is under our own MkdirTemp
	if err != nil {
		return fmt.Errorf("inferd: create %s: %w", dest, err)
	}
	defer out.Close()

	written := int64(0)
	total := resp.ContentLength
	body := io.LimitReader(resp.Body, maxBytes)

	buf := make([]byte, 64*1024)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return fmt.Errorf("inferd: write %s: %w", dest, werr)
			}
			written += int64(n)
			if progress != nil {
				progress(written, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("inferd: read body: %w", readErr)
		}
	}
	return nil
}

func sha256OfFile(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- caller-controlled path inside our temp dir
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractTarGz extracts src.tar.gz into dst. Strips one leading path
// component (inferd-vX.Y.Z-<platform>/...). Refuses paths that escape dst.
func extractTarGz(src, dst string) error {
	f, err := os.Open(src) // #nosec G304 -- our own download path
	if err != nil {
		return fmt.Errorf("inferd: open %s: %w", src, err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("inferd: gunzip %s: %w", src, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("inferd: tar next: %w", err)
		}
		stripped := stripLeadingComponent(hdr.Name)
		if stripped == "" {
			continue
		}
		dstPath, err := safeJoin(dst, stripped)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dstPath, 0o750); err != nil {
				return fmt.Errorf("inferd: mkdir %s: %w", dstPath, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dstPath), 0o750); err != nil {
				return fmt.Errorf("inferd: mkdir %s: %w", filepath.Dir(dstPath), err)
			}
			out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777) // #nosec G115,G304,G703 -- mode masked with 0o777 (9 bits, safe uint32 conversion); dstPath is post-safeJoin inside our MkdirTemp
			if err != nil {
				return fmt.Errorf("inferd: create %s: %w", dstPath, err)
			}
			if _, err := io.Copy(out, io.LimitReader(tr, 200<<20)); err != nil { // #nosec G110 // nosemgrep: go.lang.security.decompression_bomb.potential-dos-via-decompression-bomb -- io.LimitReader caps each member at 200 MiB
				_ = out.Close()
				return fmt.Errorf("inferd: copy %s: %w", dstPath, err)
			}
			if err := out.Close(); err != nil {
				return fmt.Errorf("inferd: close %s: %w", dstPath, err)
			}
		}
	}
	return nil
}

func extractZip(src, dst string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("inferd: open zip %s: %w", src, err)
	}
	defer r.Close()
	for _, f := range r.File {
		stripped := stripLeadingComponent(f.Name)
		if stripped == "" {
			continue
		}
		dstPath, err := safeJoin(dst, stripped)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dstPath, 0o750); err != nil {
				return fmt.Errorf("inferd: mkdir %s: %w", dstPath, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o750); err != nil {
			return fmt.Errorf("inferd: mkdir %s: %w", filepath.Dir(dstPath), err)
		}
		in, err := f.Open()
		if err != nil {
			return fmt.Errorf("inferd: open zip member %s: %w", f.Name, err)
		}
		out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode()&0o777) // #nosec G304
		if err != nil {
			_ = in.Close()
			return fmt.Errorf("inferd: create %s: %w", dstPath, err)
		}
		if _, err := io.Copy(out, io.LimitReader(in, 200<<20)); err != nil { // #nosec G110 // nosemgrep: go.lang.security.decompression_bomb.potential-dos-via-decompression-bomb -- io.LimitReader caps each member at 200 MiB
			_ = in.Close()
			_ = out.Close()
			return fmt.Errorf("inferd: copy %s: %w", dstPath, err)
		}
		_ = in.Close()
		if err := out.Close(); err != nil {
			return fmt.Errorf("inferd: close %s: %w", dstPath, err)
		}
	}
	return nil
}

func stripLeadingComponent(p string) string {
	p = strings.TrimPrefix(filepath.ToSlash(p), "./")
	idx := strings.IndexRune(p, '/')
	if idx < 0 {
		return ""
	}
	rest := p[idx+1:]
	if rest == "" {
		return ""
	}
	return filepath.FromSlash(rest)
}

func safeJoin(base, rel string) (string, error) {
	full := filepath.Join(base, rel)
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	sep := string(filepath.Separator)
	if !strings.HasPrefix(abs+sep, baseAbs+sep) && abs != baseAbs {
		return "", fmt.Errorf("inferd: archive entry %q escapes extract dir", rel)
	}
	return full, nil
}

// copyFile copies src to dst with mode. Both src and dst are
// caller-controlled paths inside our own extraction tree
// (MkdirTemp + safeJoin) or our resolved install dirs ($HOME-
// rooted); neither is user-supplied input. gosec's taint analysis
// can't follow this through, hence the per-call-site G703 + G304
// annotations on the file ops below.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) // #nosec G304,G703 -- src is post-safeJoin path inside our MkdirTemp
	if err != nil {
		return fmt.Errorf("inferd: open %s: %w", src, err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil { // #nosec G703 -- dst is from spec.BinaryDir / spec.UnitDir, $HOME-rooted constants
		return fmt.Errorf("inferd: mkdir %s: %w", filepath.Dir(dst), err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode) // #nosec G304,G703 -- dst as above
	if err != nil {
		return fmt.Errorf("inferd: create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("inferd: copy %s -> %s: %w", src, dst, err)
	}
	return out.Close()
}

// assetNameFor returns the inferd release asset filename for
// (version, platform). Patterns track inferd's release.yml.
func assetNameFor(version, platform string) string {
	v := strings.TrimPrefix(version, "v")
	switch platform {
	case "linux-amd64":
		return fmt.Sprintf("inferd-v%s-x86_64-unknown-linux-gnu.tar.gz", v)
	case "linux-arm64":
		return fmt.Sprintf("inferd-v%s-aarch64-unknown-linux-gnu.tar.gz", v)
	case "darwin-arm64":
		return fmt.Sprintf("inferd-v%s-aarch64-apple-darwin.tar.gz", v)
	case "windows-amd64":
		return fmt.Sprintf("inferd-v%s-x86_64-pc-windows-msvc.zip", v)
	}
	return ""
}

func platformSupported(platform string) bool {
	return assetNameFor("v0.0.0", platform) != ""
}

func currentPlatform() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

func inferdBinName() string {
	if runtime.GOOS == "windows" {
		return "inferd-daemon.exe"
	}
	return "inferd-daemon"
}

// ErrInferdUnsupported is returned by InstallInferd when the current
// platform isn't in the release matrix.
var ErrInferdUnsupported = errors.New("inferd: platform not supported")
