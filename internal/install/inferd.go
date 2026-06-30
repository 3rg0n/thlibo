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
//	[ probe admin socket ] ── reachable + version OK ──► UsedExisting → done
//	          │                          │
//	          │                          version too old
//	          │                          ▼
//	          │                    stop running daemon
//	          │                          │
//	          unreachable ───────────────┘
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
	"bytes"
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
	// inferdReleasesURL returns the JSON list of releases (newest
	// first). We scan it for the latest STABLE tag rather than trusting
	// /releases/latest, because inferd tags its RCs ("-rc.N") WITHOUT
	// setting GitHub's prerelease flag, so /releases/latest can return
	// an RC. See fetchLatestInferdTag.
	inferdReleasesURL = "https://api.github.com/repos/3rg0n/inferd/releases?per_page=30"

	// inferdReleaseDLBase is the per-tag asset download root.
	inferdReleaseDLBase = "https://github.com/3rg0n/inferd/releases/download"

	// cosignIdentityRegexp / cosignIssuer pin the OIDC identity
	// that's allowed to sign inferd releases. The pattern matches
	// inferd's release.yml at any v* tag.
	cosignIdentityRegexp = `^https://github\.com/3rg0n/inferd/\.github/workflows/release\.ya?ml@refs/tags/v.+$`
	cosignIssuer         = "https://token.actions.githubusercontent.com"

	// MinInferdVersion is the oldest inferd build thlibo will
	// probe-then-delegate to. Older binaries are treated as
	// not-installed and trigger a fresh install of the latest
	// release.
	//
	// Floor history:
	//   - v0.1.13: first build with the temp-copy-free model loader
	//     (inferd commit 1fe99d4 / inferd#6). On hosts where /tmp is
	//     small (WSL2 default tmpfs is half RAM) and ProtectHome= is
	//     set, every earlier version's llamacpp init crashes with
	//     ENOSPC or EROFS during the GGUF verification copy.
	//   - v0.1.14: macOS install-launchagent.sh now writes
	//     --backend llamacpp + --model-path into ProgramArguments
	//     (inferd#9 / inferd#8). Earlier launchagents bound a mock
	//     daemon — every macOS user got canned tokens with no
	//     warning. The daemon binary itself is also v0.1.14, so an
	//     in-place binary upgrade alone isn't enough; the install
	//     script has to re-run to rewrite the plist. Forcing the
	//     fresh-install branch (which calls inferd's installer)
	//     does both at once.
	//   - v0.4.0: the unified IPC wire (inferd ADR 0021 / inferd#34).
	//     v0.4 removed the protocol-v1 socket thlibo used to dial and
	//     replaced newline-delimited framing with the length-prefixed
	//     v2 wire. thlibo's codec (internal/inferd) now speaks ONLY
	//     that wire, so any daemon below v0.4.0 is unreachable — the
	//     gate must floor here, otherwise thlibo would accept an old
	//     daemon at probe time and then silently fail open on every
	//     request (ADR 0006) with no path to recovery. This is the
	//     load-bearing floor as of the v0.4 wire migration.
	MinInferdVersion = "v0.4.0"
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

	// Reachable: set on the InstalledFresh / StartedExisting paths
	// after a short post-install probe. True iff the daemon's admin
	// socket answered within the readiness window — i.e. the autostart
	// actually took and the daemon is up, not just "files placed."
	// False means the install steps ran but the daemon didn't come up
	// in time (model still loading, or a real failure to surface).
	Reachable bool

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
		// If the admin frame didn't include a version (pre-v0.1.14
		// daemons omit it), fall back to shelling the binary on disk.
		if version == "" {
			if binPath := findInstalledInferdBinary(); binPath != "" {
				version = readBinaryVersion(binPath)
			}
		}
		if !versionIsOlder(version, MinInferdVersion) {
			// Running and version is acceptable (or undetectable).
			r.UsedExisting = true
			r.ResolvedVersion = version
			if status != "" && status != "ready" {
				r.Notes = append(r.Notes,
					fmt.Sprintf("inferd is %s; thlibo will fail open until ready", status))
			}
			return r, nil
		}
		// Running but too old: stop it so the fresh-install branch
		// can replace it. Failure to stop is non-fatal — the installer
		// runs anyway and the service manager will overwrite the unit.
		r.Notes = append(r.Notes,
			fmt.Sprintf("inferd %s is running but older than minimum %s; stopping for upgrade",
				version, MinInferdVersion))
		stopInferd()
	}

	// 2. Probe: is the inferd-daemon binary already on disk?
	if binPath := findInstalledInferdBinary(); binPath != "" {
		// Don't delegate to a known-bad version. Older inferds
		// shipped a llamacpp loader that copy-verifies the GGUF
		// through $TMPDIR; on small-/tmp + ProtectHome= hosts
		// (WSL is the canonical case) that crashes the daemon
		// before it ever serves a request. Treat such binaries as
		// "not installed" and fall through to the fresh-install
		// branch, which fetches a current release.
		v := readBinaryVersion(binPath)
		if v != "" && versionIsOlder(v, MinInferdVersion) {
			r.Notes = append(r.Notes,
				fmt.Sprintf("found inferd %s at %s but it's older than the minimum supported %s; upgrading", v, binPath, MinInferdVersion))
		} else {
			if err := startInstalledInferd(binPath, &r); err != nil {
				r.Notes = append(r.Notes,
					fmt.Sprintf("found %s but couldn't start it: %v", binPath, err))
				r.Notes = append(r.Notes,
					"start inferd manually (systemctl / launchctl / sc) before re-running thlibo install")
				return r, nil
			}
			r.StartedExisting = true
			r.ResolvedVersion = v
			return r, nil
		}
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
	// Verify the autostart actually brought the daemon up — a clean
	// installer exit doesn't guarantee a running daemon (#47). Probe
	// the admin socket briefly; record the result so the installer can
	// report "started and reachable" vs. "installed but not yet
	// responding" instead of a blanket "complete."
	r.Reachable = waitForInferdReady(15 * time.Second)
	if !r.Reachable {
		r.Notes = append(r.Notes,
			"daemon not reachable yet after install — it may still be loading the model on first boot; "+
				"check `inferdctl doctor` / the daemon logs if it doesn't come up shortly")
	}
	return r, nil
}

// waitForInferdReady polls the admin socket until it answers or the
// window elapses. Returns true as soon as inferd is reachable. Used as
// a post-install confidence check so the installer reports the daemon's
// real state, not just "files placed."
func waitForInferdReady(window time.Duration) bool {
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		reachable, _, _ := probeInferdAdmin(ctx)
		cancel()
		if reachable {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
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

	// Read the on-connect snapshot. The daemon writes it immediately;
	// if nothing shows up within the deadline we treat the daemon as
	// unhealthy enough to skip. One read of 8 KiB: the snapshot
	// (a handful of capabilities frames + one lifecycle frame) fits
	// comfortably for realistic backend counts. If a pathological
	// multi-backend daemon overflowed it and the lifecycle frame were
	// truncated off, parseAdminSnapshot returns an empty status — which
	// the caller treats as reachable-but-unknown (no bogus note), so the
	// failure mode is safe rather than wrong.
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

	status, version = parseAdminSnapshot(buf[:n])
	return true, status, version
}

// lifecycleStates is the closed set of admin-socket `status` values that
// represent the daemon's lifecycle (protocol-v1 §5). Anything else —
// notably inferd v2's `capabilities` backend-advertisement frames — is
// not a lifecycle state and must not be reported as one. We *allowlist*
// these rather than denylisting `capabilities`: a readiness client must
// ignore unknown status values (protocol-v1 §5.2), and allowlisting is
// the only form that stays correct if a future wire adds a *second*
// non-lifecycle advertisement frame (denylisting would silently regress
// to the #54 bug).
var lifecycleStates = map[string]bool{
	"starting":      true,
	"loading_model": true,
	"ready":         true,
	"restarting":    true,
	"draining":      true,
}

// parseAdminSnapshot extracts the lifecycle status + version from the
// snapshot the admin socket writes on connect.
//
// The snapshot is *several* NDJSON frames, not one. inferd v2 leads with
// one `capabilities` advertisement frame per loaded backend (e.g.
// embeddinggemma-300m, then gemma-4-e4b) before the actual lifecycle
// frame (`ready`/`loading_model`/...). We report the last real lifecycle
// status — taking the first line (the old bug, #54) surfaced the
// `capabilities` frame as the bogus Note "inferd is capabilities; thlibo
// will fail open until ready" even when a later frame said `ready`. A
// snapshot that is all advertisements with no lifecycle frame yet (or a
// frame truncated by the single read) yields an empty status, which the
// caller treats as reachable-but-unknown and emits no note.
func parseAdminSnapshot(b []byte) (status, version string) {
	for _, line := range splitFrames(b) {
		var frame struct {
			Status  string `json:"status"`
			Version string `json:"version,omitempty"`
		}
		if err := json.Unmarshal(line, &frame); err != nil {
			continue // partial/trailing line; skip
		}
		// Version may ride any frame (a capabilities frame can carry it
		// while the lifecycle frame doesn't); keep the last non-empty.
		if frame.Version != "" {
			version = frame.Version
		}
		if lifecycleStates[frame.Status] {
			status = frame.Status
		}
	}
	return status, version
}

// splitFrames returns each complete `\n`-terminated NDJSON line in b.
// A trailing fragment without a newline is dropped (a partial frame the
// read didn't finish), matching the daemon's one-object-per-line wire.
func splitFrames(b []byte) [][]byte {
	var frames [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			if line := bytes.TrimSpace(b[start:i]); len(line) > 0 {
				frames = append(frames, line)
			}
			start = i + 1
		}
	}
	return frames
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
				// Current layout: install.ps1's $BinaryPath default is
				// %LOCALAPPDATA%\inferd\inferd-daemon.exe (binary +
				// backends as siblings). The older bin/ subdir is kept
				// as a fallback so a re-run still finds a pre-0.7.5
				// install instead of re-downloading.
				candidates = append(candidates, filepath.Join(appData, "inferd", name))
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

// versionIsOlder reports whether got < want. Both arguments may carry
// a leading 'v' or none. Each is parsed as up to four dot-separated
// numeric components; any non-numeric suffix on a component (e.g. a
// "-rc1" trailer) is dropped before comparison. Empty `got` returns
// false — we'd rather under-flag than spuriously upgrade a binary the
// caller couldn't fingerprint.
func versionIsOlder(got, want string) bool {
	if strings.TrimSpace(got) == "" {
		return false
	}
	g := parseSemverTuple(got)
	w := parseSemverTuple(want)
	for i := 0; i < 4; i++ {
		if g[i] < w[i] {
			return true
		}
		if g[i] > w[i] {
			return false
		}
	}
	return false
}

// parseSemverTuple turns "v0.1.13" / "0.1.13" / "0.1.13-rc1" into
// [0,1,13,0]. Components that fail to parse become zero so a malformed
// string doesn't accidentally compare older than a valid one.
func parseSemverTuple(s string) [4]int {
	var out [4]int
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	parts := strings.SplitN(s, ".", 4)
	for i := 0; i < len(out) && i < len(parts); i++ {
		p := parts[i]
		// Strip any "-rc1" / "+build" trailer.
		if cut := strings.IndexAny(p, "-+"); cut >= 0 {
			p = p[:cut]
		}
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				n = 0
				break
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out
}

// stopInferd asks the platform's service manager to stop the running
// inferd daemon. Best-effort: failures are silently ignored because the
// fresh-install branch (inferd's own installer) will overwrite the unit
// and re-bootstrap the agent regardless.
func stopInferd() {
	switch runtime.GOOS {
	case "linux":
		_ = runCommandSilent("systemctl", "--user", "stop", "inferd.service")
	case "darwin":
		home, _ := os.UserHomeDir()
		plist := filepath.Join(home, "Library", "LaunchAgents", "io.inferd.daemon.plist")
		uid := fmt.Sprintf("%d", os.Getuid())
		_ = runCommandSilent("launchctl", "bootout", "gui/"+uid, plist)
	case "windows":
		_ = runCommandSilent("sc.exe", "stop", "inferd-daemon")
	}
}

// runCommandSilent is like runCommand but discards stdout/stderr.
func runCommandSilent(name string, args ...string) error {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd := exec.Command(name, args...) // #nosec G204 -- name + args are constants from this package
	return cmd.Run()
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
		// Copy the ggml backend libs alongside the binary BEFORE running
		// the launchagent script — it aborts (exit 1) if libllama is not
		// a sibling of the binary, leaving no LaunchAgent and no running
		// daemon (#47).
		if err := copyBackends(extractDir, filepath.Dir(stableBin)); err != nil {
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
		// ggml backend libs must sit next to the binary ($ORIGIN RPATH);
		// without them the daemon starts then aborts with "no backends
		// are loaded" on model load. Same requirement as macOS (#47).
		if err := copyBackends(extractDir, filepath.Dir(stableBin)); err != nil {
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
		// inferd's Windows install.ps1 is a NO-ADMIN, per-user install:
		// it places the daemon + backends under %LOCALAPPDATA%\inferd
		// and registers a Startup-folder shortcut (shell:startup) that
		// launches the daemon as the current user on login — the same
		// per-user posture as the macOS LaunchAgent and Linux
		// systemd --user paths. So thlibo runs it directly (no manual
		// elevated step), achieving zero-touch autostart on Windows too.
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
		// install.ps1 defaults $BinaryPath to %LOCALAPPDATA%\inferd\
		// inferd-daemon.exe — stage the binary + backends there so the
		// script's defaults line up and the daemon finds its ggml libs
		// as siblings.
		stableDir := filepath.Join(appData, "inferd")
		stableBin := filepath.Join(stableDir, inferdBinName())
		stableScript := filepath.Join(stableDir, "install.ps1")
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
		// ggml backend DLLs must sit next to the binary (loader searches
		// the exe's own dir). Without them the daemon loads no compute
		// backend (#47).
		if err := copyBackends(extractDir, stableDir); err != nil {
			return err
		}

		// Run install.ps1 as the current user — it registers the
		// Startup shortcut and starts the daemon. -ExecutionPolicy
		// Bypass so an unsigned-script policy doesn't block it.
		// stableScript/stableBin are thlibo-built paths under
		// %LOCALAPPDATA% (LOCALAPPDATA is a trusted OS env var, not
		// caller-controlled argv) — no command injection surface.
		// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
		cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass",
			"-File", stableScript, "-BinaryPath", stableBin) // #nosec G204,G702 -- args are %LOCALAPPDATA%-rooted thlibo paths, not user input
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			// Don't hard-fail the whole install — leave the staged files
			// + a manual hint so the user can finish, mirroring the
			// fail-open posture elsewhere.
			r.Notes = append(r.Notes,
				fmt.Sprintf("inferd staged at %s but install.ps1 failed: %v", stableBin, err))
			r.Notes = append(r.Notes,
				fmt.Sprintf("finish manually: powershell -ExecutionPolicy Bypass -File '%s' -BinaryPath '%s'", stableScript, stableBin))
			return ErrInferdNeedsManualStep
		}
		r.Notes = append(r.Notes,
			fmt.Sprintf("installed inferd via install.ps1 (per-user Startup shortcut; binary at %s)", stableBin))
		return nil
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
	req, err := http.NewRequest(http.MethodGet, inferdReleasesURL, nil)
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
	var releases []struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", err
	}
	// The list is newest-first. Pick the first STABLE release. A
	// release is stable iff it's not a draft, not GitHub-flagged
	// prerelease, AND its tag carries no pre-release suffix
	// ("v1.2.3-rc.1" / "-beta"). The suffix check is the load-bearing
	// one: inferd ships RC tags without the prerelease flag, so trusting
	// the flag alone would install an RC sidecar (issue #47 follow-up).
	for _, rel := range releases {
		if rel.Draft || rel.Prerelease || rel.TagName == "" {
			continue
		}
		if isPrereleaseTag(rel.TagName) {
			continue
		}
		return rel.TagName, nil
	}
	return "", errors.New("inferd: no stable (non-prerelease) release found; pass --inferd-version to pin one")
}

// isPrereleaseTag reports whether a semver tag carries a pre-release
// suffix — anything after a hyphen in the version core, e.g.
// "v0.5.1-rc.1", "v1.0.0-beta.2". Stable tags ("v0.5.0") have no hyphen.
func isPrereleaseTag(tag string) bool {
	return strings.Contains(strings.TrimPrefix(tag, "v"), "-")
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

// download streams url to dest. The cap is a runaway-guard, not a tight
// bound: inferd's release bundles now ship the ggml/CUDA/Metal backend
// libraries alongside the daemon, so a platform archive is ~600+ MB
// (the Windows zip with CUDA DLLs is ~633 MB). The previous 200 MiB cap
// silently truncated the archive (io.LimitReader stops without error),
// producing a corrupt "not a valid zip" on every fresh install. 2 GiB
// leaves generous headroom while still bounding a pathological response.
func download(url, dest string, progress ProgressFunc) error {
	const maxBytes = 2 << 30 // 2 GiB
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

	// If the server declared a size larger than the cap, fail loudly up
	// front rather than truncating.
	if resp.ContentLength > maxBytes {
		return fmt.Errorf("inferd: %s is %d bytes, exceeds the %d-byte download cap", url, resp.ContentLength, int64(maxBytes))
	}

	written := int64(0)
	total := resp.ContentLength
	// Read up to maxBytes+1: if we actually get maxBytes+1 bytes the
	// response overflowed the cap, so error instead of silently writing
	// a truncated (corrupt) archive (the old bug: a 633 MB zip clipped
	// to 200 MB looked like "not a valid zip").
	body := io.LimitReader(resp.Body, maxBytes+1)

	buf := make([]byte, 64*1024)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if written+int64(n) > maxBytes {
				// Remove the partial file so a later step can't mistake a
				// capped-off download for a valid (just corrupt) archive
				// and emit a misleading "not a valid zip" instead.
				_ = out.Close()
				_ = os.Remove(dest)
				return fmt.Errorf("inferd: download %s exceeded the %d-byte cap (response larger than expected)", url, int64(maxBytes))
			}
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

// copyBackends copies the ggml backend module libraries from the
// extracted tarball's backends/ subdir into dstDir (the dir holding the
// stable inferd-daemon binary). This is REQUIRED: ggml's
// ggml_backend_load_all() searches the daemon executable's own
// directory for the backend modules (libggml-*, libllama, .dylib/.so) —
// not a backends/ subdir — and inferd's launchagent/systemd path
// refuses to install when libllama is missing next to the binary. We
// place the libs as siblings of the binary so the daemon loads its
// compute backend at startup (without this the daemon aborts with
// "no backends are loaded" / the installer exits 1 → #47).
//
// extractDir is the tarball root (contains both the binary and
// backends/). Best-effort per file is wrong here — a missing backend
// lib is fatal at daemon start — so any copy error is returned.
func copyBackends(extractDir, dstDir string) error {
	srcDir := filepath.Join(extractDir, "backends")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			// No backends/ in the tarball — older layout that shipped
			// libs next to the binary already; nothing to do.
			return nil
		}
		return fmt.Errorf("inferd: read backends dir %s: %w", srcDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		src := filepath.Join(srcDir, e.Name())
		dst := filepath.Join(dstDir, e.Name())
		// Backend libs must be executable/loadable; 0o755 matches what
		// the daemon binary gets.
		if err := copyFile(src, dst, 0o755); err != nil {
			return err
		}
	}
	return nil
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
