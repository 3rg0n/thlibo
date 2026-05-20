package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// inferd v0.1.9's actual plist template, kept inline so the test
// doesn't depend on a downloaded tarball. If inferd's template
// changes shape, this fixture should track or the substitution
// logic needs a refresh.
const macPlistFixture = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple Computer//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>io.inferd.daemon</string>

    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/inferd-daemon</string>
        <string>--backend</string><string>mock</string>
        <string>--lock</string>
        <string>/Users/USERNAME_HERE/Library/Application Support/inferd/inferd.lock</string>
        <string>--uds</string>
        <string>/Users/USERNAME_HERE/Library/Application Support/inferd/infer.sock</string>
    </array>

    <key>EnvironmentVariables</key>
    <dict>
        <key>INFERD_LOG</key>
        <string>info</string>
        <key>INFERD_LOG_DIR</key>
        <string>/Users/USERNAME_HERE/Library/Logs/inferd</string>
    </dict>

    <key>RunAtLoad</key>
    <true/>

    <key>StandardOutPath</key>
    <string>/Users/USERNAME_HERE/Library/Logs/inferd/stdout.log</string>
    <key>StandardErrorPath</key>
    <string>/Users/USERNAME_HERE/Library/Logs/inferd/stderr.log</string>
</dict>
</plist>
`

func TestCopyMacPlistWithFixesSubstitutesUsername(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("plist substitution is darwin-only; the helper still runs on other OSes for unit-test purposes but home resolution differs")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "in.plist")
	dst := filepath.Join(dir, "out.plist")
	if err := os.WriteFile(src, []byte(macPlistFixture), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	spec := InferdInstallSpec{BinaryDir: filepath.Join(dir, "fakebin")}
	r := &InferdInstallResult{}
	if err := copyMacPlistWithFixes(src, dst, spec, r); err != nil {
		t.Fatalf("copyMacPlistWithFixes: %v", err)
	}

	out, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	body := string(out)

	if strings.Contains(body, "USERNAME_HERE") {
		t.Errorf("plist still contains literal USERNAME_HERE\n%s", body)
	}
	if strings.Contains(body, "/usr/local/bin/inferd-daemon") {
		t.Errorf("plist still references /usr/local/bin/inferd-daemon\n%s", body)
	}
	wantBin := filepath.Join(spec.BinaryDir, inferdBinName())
	if !strings.Contains(body, wantBin) {
		t.Errorf("plist missing substituted binary path %q\n%s", wantBin, body)
	}
	if !strings.Contains(body, "$TMPDIR/inferd/infer.sock") {
		t.Errorf("plist missing $TMPDIR-relative socket path\n%s", body)
	}
	if !strings.Contains(body, "$TMPDIR/inferd/inferd.lock") {
		t.Errorf("plist missing $TMPDIR-relative lock path\n%s", body)
	}
	// The Application Support paths should be gone for the socket
	// and lock; the log path can stay (we don't substitute that one
	// — it's a stable user-readable location, not a wire path).
	if strings.Contains(body, "Application Support/inferd/infer.sock") {
		t.Errorf("plist still references Application Support socket path\n%s", body)
	}
}

func TestCopyMacPlistWithFixesNotesMissingAdminAddr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("plist substitution is darwin-only; uses os.UserHomeDir which differs on Windows")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "in.plist")
	dst := filepath.Join(dir, "out.plist")
	if err := os.WriteFile(src, []byte(macPlistFixture), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	spec := InferdInstallSpec{BinaryDir: filepath.Join(dir, "fakebin")}
	r := &InferdInstallResult{}
	if err := copyMacPlistWithFixes(src, dst, spec, r); err != nil {
		t.Fatalf("copyMacPlistWithFixes: %v", err)
	}

	found := false
	for _, n := range r.Notes {
		if strings.Contains(n, "--admin-addr") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected note about missing --admin-addr; got notes: %v", r.Notes)
	}
}
