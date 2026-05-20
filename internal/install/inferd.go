// Sidecar inferd installer.
//
// thlibo v0.6+ is pure middleware: inference moved to the separate
// inferd daemon. This file owns the "install inferd alongside thlibo
// at install time" path.
//
// Design choices:
//
//   - Latest by default, --inferd-version override. We don't pin a
//     specific inferd release in source — instead we hit
//     api.github.com/repos/3rg0n/inferd/releases/latest at install
//     time and fetch whatever's current. Inferd ships often (5
//     releases in 48h during M2 stabilisation); pinning a single
//     SHA in thlibo source would be a constant-bump treadmill.
//   - Cosign verify when available; HTTPS trust otherwise. Each
//     inferd release ships <asset>.cosign.bundle alongside the
//     tarball. If `cosign` is on PATH, we shell out and verify-blob
//     against inferd's keyless OIDC identity. If not, we trust
//     GitHub's HTTPS. Strongest-when-available, no install-time
//     dep.
//   - Use inferd's bundled platform manifest verbatim. Each tarball
//     has packaging/inferd.service (Linux), packaging/io.inferd.daemon.plist
//     (macOS), packaging/install.ps1 (Windows). We drop these into
//     the right system path rather than rolling our own.
//   - Backend override via systemd drop-in. The bundled unit
//     defaults to --backend mock; we write a thlibo.conf drop-in
//     that flips ExecStart to --backend llamacpp pointing at the
//     migrated GGUF in the shared model store. Users can delete
//     the drop-in to restore inferd's defaults.

package install

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// non-prerelease tag. Releases marked prerelease (e.g.
	// v0.1.0-alpha.0) are skipped here; consumers wanting alphas
	// pass --inferd-version explicitly.
	inferdLatestURL = "https://api.github.com/repos/3rg0n/inferd/releases/latest"

	// inferdReleaseDLBase is the per-tag asset download root.
	inferdReleaseDLBase = "https://github.com/3rg0n/inferd/releases/download"

	// cosignIdentityRegexp / cosignIssuer pin the OIDC identity
	// that's allowed to sign inferd releases. The pattern matches
	// inferd's release.yml at any v* tag.
	cosignIdentityRegexp = `^https://github\.com/3rg0n/inferd/\.github/workflows/release\.ya?ml@refs/tags/v.+$`
	cosignIssuer         = "https://token.actions.githubusercontent.com"
)

// InferdInstallSpec captures everything the installer needs to know
// about where + how to install inferd.
type InferdInstallSpec struct {
	// Version is the inferd tag to install, e.g. "v0.1.9". Empty
	// means "fetch latest from GitHub Releases at install time."
	Version string

	BinaryDir   string // ~/.local/bin or %LOCALAPPDATA%\inferd\bin
	UnitDir     string // ~/.config/systemd/user, ~/Library/LaunchAgents, or empty
	DropinDir   string // Linux: ~/.config/systemd/user/inferd.service.d; empty elsewhere
	ModelPath   string // resolved GGUF path; passed to --model-path. Empty -> mock backend
	SkipBackendOverride bool
}

// InferdInstallResult reports what the sidecar installer did.
type InferdInstallResult struct {
	Skipped             bool   // user passed --skip-inferd
	ResolvedVersion     string // tag we actually fetched (after "latest" resolution)
	BinaryPath          string
	BinarySize          int64
	UnitInstalled       bool
	UnitDropinInstalled bool
	BackendConfigured   string // "llamacpp" or "mock"
	ModelPath           string
	CosignVerified      bool   // .cosign.bundle was checked + passed
	Notes               []string
}

// PullOptions matches the shape v0.5's PullEngine used so callers
// can swap implementations cleanly.
type PullOptions struct {
	Progress ProgressFunc
}

// ProgressFunc is the per-byte progress callback signature.
// total may be 0 if the server did not send Content-Length.
type ProgressFunc func(written, total int64)

// InstallInferd is the high-level orchestrator: resolve the version,
// download the matching tarball, verify (cosign if present),
// extract, drop the binary into BinaryDir, drop the bundled
// platform manifest into UnitDir, and (Linux only) write a thlibo
// drop-in that flips the backend to llamacpp.
//
// Idempotent: a re-run with the same Version + ModelPath leaves disk
// unchanged after the version-detection probe + drop-in compare.
func InstallInferd(spec InferdInstallSpec, opts PullOptions) (InferdInstallResult, error) {
	var r InferdInstallResult
	platform := currentPlatform()
	if !platformSupported(platform) {
		return r, fmt.Errorf("inferd: %s not supported by inferd's release matrix", platform)
	}

	// 1. Resolve the requested version. Empty -> latest.
	version := spec.Version
	if version == "" {
		v, err := fetchLatestInferdTag()
		if err != nil {
			return r, fmt.Errorf("inferd: resolve latest: %w", err)
		}
		version = v
	}
	r.ResolvedVersion = version
	r.BinaryPath = filepath.Join(spec.BinaryDir, inferdBinName())

	// 2. Idempotency: if the on-disk inferd reports the same
	//    version, skip the download.
	skipDownload := onDiskMatchesVersion(r.BinaryPath, version)
	if !skipDownload {
		extractDir, err := pullInferd(version, opts)
		if err != nil {
			return r, err
		}
		defer os.RemoveAll(extractDir) // #nosec G104 -- best-effort temp cleanup

		// 3. Optional cosign verify.
		if v, note := tryCosignVerify(version, extractDir); v {
			r.CosignVerified = true
		} else if note != "" {
			r.Notes = append(r.Notes, note)
		}

		// 4. Copy binary into place.
		srcBin := filepath.Join(extractDir, inferdBinName())
		if _, err := os.Stat(srcBin); err != nil {
			return r, fmt.Errorf("inferd: tarball missing %s: %w", inferdBinName(), err)
		}
		if err := os.MkdirAll(spec.BinaryDir, 0o755); err != nil {
			return r, fmt.Errorf("inferd: create %s: %w", spec.BinaryDir, err)
		}
		if err := copyFile(srcBin, r.BinaryPath, 0o755); err != nil {
			return r, err
		}
		if info, err := os.Stat(r.BinaryPath); err == nil {
			r.BinarySize = info.Size()
		}

		// 5. Drop the bundled platform manifest.
		if err := installPlatformUnit(spec, extractDir, &r); err != nil {
			return r, err
		}
	}

	// 6. Backend override drop-in (Linux today; macOS/Windows TODO).
	if !spec.SkipBackendOverride {
		if installed, err := writeBackendDropin(spec, &r); err != nil {
			return r, err
		} else if installed {
			r.UnitDropinInstalled = true
		}
	}

	return r, nil
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
		// /releases/latest never returns prereleases per the API
		// contract, but defend in depth in case GitHub changes
		// behaviour.
		return "", fmt.Errorf("GitHub returned prerelease %s; pass --inferd-version to install a prerelease", payload.TagName)
	}
	return payload.TagName, nil
}

// pullInferd downloads + extracts the tarball/zip for version into
// a fresh temp directory. Returns the directory containing
// inferd-daemon + packaging/.
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
	// Capture the SHA-256 we observed for the result struct.
	if _, err := sha256OfFile(archivePath); err != nil {
		return "", err
	}

	// Optionally also fetch the .cosign.bundle for verification later.
	bundleURL := url + ".cosign.bundle"
	bundlePath := archivePath + ".cosign.bundle"
	if err := download(bundleURL, bundlePath, nil); err != nil {
		// Bundle isn't fatal; we just won't verify if it's missing.
		_ = os.Remove(bundlePath)
	}

	extractDir := filepath.Join(tmpDir, "extracted")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
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
// cosign is on PATH. Returns (verified, advisory-note). Verified=true
// means the signature checked out; note is non-empty when we want
// the installer to surface an explanation (cosign missing, bundle
// missing, verify failed).
func tryCosignVerify(version, extractDir string) (bool, string) {
	cosignPath, err := exec.LookPath("cosign")
	if err != nil {
		return false, "cosign not on PATH; skipped signature verify (HTTPS trust only)"
	}
	// The bundle + tarball both live in the parent of extractDir
	// (we MkdirTemp/archive.tar.gz, MkdirTemp/extracted/...).
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

// onDiskMatchesVersion runs `<bin> --version` and reports whether
// the output ends with the requested tag. Returns false silently if
// the binary is missing, unreadable, or doesn't speak --version.
func onDiskMatchesVersion(binPath, wantVersion string) bool {
	if _, err := os.Stat(binPath); err != nil {
		return false
	}
	// #nosec G204 -- binPath is a path we resolved + own; argv is constant
	cmd := exec.Command(binPath, "--version")
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	// Format is e.g. "inferd-daemon 0.1.9". Match either with or
	// without the leading "v".
	want := strings.TrimPrefix(wantVersion, "v")
	return strings.Contains(string(out), want)
}

// download streams url to dest. Returns the SHA-256 it observed.
// 200 MB cap is generous for inferd (~3-4 MB tarballs).
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

// sha256OfFile returns the hex-encoded SHA-256 of path. Used for the
// installer to record what it actually fetched (separate from
// signature verify).
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
// component (inferd's tarballs put everything under
// inferd-vX.Y.Z-<platform>/...). Refuses paths that escape dst.
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
			if err := os.MkdirAll(dstPath, 0o755); err != nil {
				return fmt.Errorf("inferd: mkdir %s: %w", dstPath, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
				return fmt.Errorf("inferd: mkdir %s: %w", filepath.Dir(dstPath), err)
			}
			out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777) // #nosec G304
			if err != nil {
				return fmt.Errorf("inferd: create %s: %w", dstPath, err)
			}
			if _, err := io.Copy(out, io.LimitReader(tr, 200<<20)); err != nil { // #nosec G110
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

// extractZip extracts src.zip into dst with the same path-strip
// (inferd-vX.Y.Z-x86_64-pc-windows-msvc/...) and zip-slip
// protection.
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
			if err := os.MkdirAll(dstPath, 0o755); err != nil {
				return fmt.Errorf("inferd: mkdir %s: %w", dstPath, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
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
		if _, err := io.Copy(out, io.LimitReader(in, 200<<20)); err != nil { // #nosec G110
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

// stripLeadingComponent removes the first path component, e.g.
// "inferd-v0.1.9-linux/inferd-daemon" -> "inferd-daemon".
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

// safeJoin refuses archive entries that resolve outside base.
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

// installPlatformUnit drops the bundled manifest from the inferd
// tarball into the right system path.
func installPlatformUnit(spec InferdInstallSpec, extractDir string, r *InferdInstallResult) error {
	if spec.UnitDir == "" {
		return nil
	}
	switch runtime.GOOS {
	case "linux":
		src := filepath.Join(extractDir, "packaging", "inferd.service")
		if _, err := os.Stat(src); err != nil {
			r.Notes = append(r.Notes, "tarball missing packaging/inferd.service; skipping unit install")
			return nil
		}
		dst := filepath.Join(spec.UnitDir, "inferd.service")
		if err := os.MkdirAll(spec.UnitDir, 0o755); err != nil {
			return fmt.Errorf("inferd: create unit dir: %w", err)
		}
		if err := copyFile(src, dst, 0o644); err != nil {
			return err
		}
		r.UnitInstalled = true
	case "darwin":
		src := filepath.Join(extractDir, "packaging", "io.inferd.daemon.plist")
		if _, err := os.Stat(src); err != nil {
			r.Notes = append(r.Notes, "tarball missing packaging/io.inferd.daemon.plist; skipping LaunchAgent install")
			return nil
		}
		dst := filepath.Join(spec.UnitDir, "io.inferd.daemon.plist")
		if err := os.MkdirAll(spec.UnitDir, 0o755); err != nil {
			return fmt.Errorf("inferd: create LaunchAgents dir: %w", err)
		}
		if err := copyFile(src, dst, 0o644); err != nil {
			return err
		}
		r.UnitInstalled = true
	case "windows":
		// install.ps1 needs admin; skip auto-run from `thlibo
		// install`. Note the user.
		r.Notes = append(r.Notes,
			`Windows: run inferd's packaging\install.ps1 from an elevated PowerShell to register the service`)
	}
	return nil
}

// writeBackendDropin writes a small systemd override that flips the
// daemon to --backend llamacpp pointing at spec.ModelPath. Currently
// Linux-only; macOS launchd plist override + Windows service config
// are TODO.
//
// Returns (installed, err). installed=true means the drop-in is now
// at the expected path with the expected content (whether we wrote
// it or it was already there).
func writeBackendDropin(spec InferdInstallSpec, r *InferdInstallResult) (bool, error) {
	r.BackendConfigured = "mock"
	if spec.ModelPath == "" {
		r.Notes = append(r.Notes,
			"no model on disk; inferd will run --backend mock until a model is available")
		return false, nil
	}
	switch runtime.GOOS {
	case "linux":
		return writeLinuxDropin(spec, r)
	case "darwin", "windows":
		r.Notes = append(r.Notes,
			fmt.Sprintf("backend override not yet automated on %s; inferd will start --backend mock; configure manually for real inference", runtime.GOOS))
		return false, nil
	default:
		return false, nil
	}
}

func writeLinuxDropin(spec InferdInstallSpec, r *InferdInstallResult) (bool, error) {
	if spec.DropinDir == "" {
		return false, fmt.Errorf("inferd: DropinDir must be set on Linux")
	}
	if err := os.MkdirAll(spec.DropinDir, 0o755); err != nil {
		return false, fmt.Errorf("inferd: create dropin dir: %w", err)
	}
	content := fmt.Sprintf(`# thlibo-owned drop-in: switch inferd to --backend llamacpp pointing at
# the shared model store. Written by `+"`thlibo install`"+`. Safe to
# delete if you want to manage inferd's backend yourself.

[Service]
ExecStart=
ExecStart=%%h/.local/bin/inferd-daemon \
    --backend llamacpp \
    --model-path %s \
    --lock %%t/inferd/inferd.lock \
    --uds %%t/inferd/infer.sock \
    --admin-addr %%t/inferd/admin.sock
`, escapeForUnit(spec.ModelPath))

	dropinPath := filepath.Join(spec.DropinDir, "thlibo.conf")
	if existing, err := os.ReadFile(dropinPath); err == nil { // #nosec G304 -- our own write path
		if string(existing) == content {
			return true, nil
		}
	}
	if err := os.WriteFile(dropinPath, []byte(content), 0o644); err != nil { // #nosec G306 -- systemd reads this
		return false, fmt.Errorf("inferd: write dropin: %w", err)
	}
	r.BackendConfigured = "llamacpp"
	r.ModelPath = spec.ModelPath
	return true, nil
}

// copyFile copies src to dst with mode.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) // #nosec G304 -- caller-controlled paths inside our extraction tree
	if err != nil {
		return fmt.Errorf("inferd: open %s: %w", src, err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("inferd: mkdir %s: %w", filepath.Dir(dst), err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode) // #nosec G304
	if err != nil {
		return fmt.Errorf("inferd: create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("inferd: copy %s -> %s: %w", src, dst, err)
	}
	return out.Close()
}

// escapeForUnit handles paths containing spaces / quotes for the
// systemd ExecStart line.
func escapeForUnit(s string) string {
	if !strings.ContainsAny(s, " \t\"") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// assetNameFor returns the inferd release asset filename for
// (version, platform). Patterns track inferd's release.yml — they
// use Rust target triples, not Go GOOS-GOARCH.
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

// platformSupported reports whether the current GOOS-GOARCH appears
// in inferd's release matrix.
func platformSupported(platform string) bool {
	return assetNameFor("v0.0.0", platform) != ""
}

// currentPlatform returns the GOOS-GOARCH key thlibo uses for its
// inferd-platform decisions.
func currentPlatform() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// inferdBinName returns the platform-correct binary name.
func inferdBinName() string {
	if runtime.GOOS == "windows" {
		return "inferd-daemon.exe"
	}
	return "inferd-daemon"
}

// ErrInferdUnsupported is returned by InstallInferd when the current
// platform isn't in the release matrix.
var ErrInferdUnsupported = errors.New("inferd: platform not supported")
