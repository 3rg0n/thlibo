package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// inferd v0.1.11 plist template (the placeholder-based one). Kept
// inline so the test doesn't depend on a downloaded tarball. If
// inferd's template changes shape (new placeholder, repositioned
// substitution markers), this fixture should track and the
// substitution logic needs a refresh.
const macPlistFixture = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>io.inferd.daemon</string>

    <key>ProgramArguments</key>
    <array>
        <string>__BIN__</string>
        <string>--lock</string>
        <string>__TMPDIR__inferd/inferd.lock</string>
        <string>--uds</string>
        <string>__TMPDIR__inferd/infer.sock</string>
        <string>--admin-addr</string>
        <string>__TMPDIR__inferd/admin.sock</string>
    </array>

    <key>EnvironmentVariables</key>
    <dict>
        <key>INFERD_LOG</key>
        <string>info</string>
        <key>INFERD_LOG_DIR</key>
        <string>__HOME__/Library/Logs/inferd</string>
    </dict>

    <key>RunAtLoad</key>
    <true/>

    <key>StandardOutPath</key>
    <string>__HOME__/Library/Logs/inferd/stdout.log</string>
    <key>StandardErrorPath</key>
    <string>__HOME__/Library/Logs/inferd/stderr.log</string>
</dict>
</plist>
`

func TestCopyMacPlistWithFixesResolvesAllPlaceholders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("plist substitution uses os.UserHomeDir() / os.TempDir() with darwin semantics in mind; the test is meaningful on darwin + linux runners")
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

	// All __FOO__ markers should be resolved.
	if strings.Contains(body, "__BIN__") {
		t.Errorf("__BIN__ still present in output\n%s", body)
	}
	if strings.Contains(body, "__HOME__") {
		t.Errorf("__HOME__ still present in output\n%s", body)
	}
	if strings.Contains(body, "__TMPDIR__") {
		t.Errorf("__TMPDIR__ still present in output\n%s", body)
	}

	// __BIN__ should resolve to spec.BinaryDir/inferd-daemon (or
	// .exe on Windows; we skip Windows above).
	wantBin := filepath.Join(spec.BinaryDir, inferdBinName())
	if !strings.Contains(body, wantBin) {
		t.Errorf("plist missing substituted binary path %q\n%s", wantBin, body)
	}

	// __TMPDIR__ should resolve to a path that ends with the platform
	// separator so __TMPDIR__inferd/... reads as <tmpdir>/inferd/...
	tmpdir := os.TempDir()
	if !strings.HasSuffix(tmpdir, string(os.PathSeparator)) {
		tmpdir += string(os.PathSeparator)
	}
	wantSocket := tmpdir + "inferd/infer.sock"
	if !strings.Contains(body, wantSocket) {
		t.Errorf("plist missing __TMPDIR__-resolved socket path %q\n%s", wantSocket, body)
	}
	wantAdmin := tmpdir + "inferd/admin.sock"
	if !strings.Contains(body, wantAdmin) {
		t.Errorf("plist missing __TMPDIR__-resolved admin path %q\n%s", wantAdmin, body)
	}

	// __HOME__ should resolve to the user's home dir.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	wantLog := filepath.Join(home, "Library", "Logs", "inferd", "stdout.log")
	if !strings.Contains(body, wantLog) {
		t.Errorf("plist missing __HOME__-resolved log path %q\n%s", wantLog, body)
	}
}

func TestCopyMacPlistRejectsLegacyV019Template(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.plist")
	dst := filepath.Join(dir, "out.plist")

	legacy := strings.ReplaceAll(macPlistFixture, "__HOME__", "/Users/USERNAME_HERE")
	if err := os.WriteFile(src, []byte(legacy), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	spec := InferdInstallSpec{BinaryDir: filepath.Join(dir, "fakebin")}
	r := &InferdInstallResult{}
	err := copyMacPlistWithFixes(src, dst, spec, r)
	if err == nil {
		t.Fatal("expected error on legacy USERNAME_HERE template, got nil")
	}
	if !strings.Contains(err.Error(), "v0.1.9") || !strings.Contains(err.Error(), "USERNAME_HERE") {
		t.Errorf("error should mention v0.1.9 + USERNAME_HERE; got: %v", err)
	}
}

func TestCopyMacPlistRejectsUnknownPlaceholder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses os.UserHomeDir which differs on Windows")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "in.plist")
	dst := filepath.Join(dir, "out.plist")

	// Inferd adds a hypothetical new placeholder we don't know
	// about. We should fail loudly rather than silently ship a
	// plist with a literal __FUTURE_THING__ in it.
	withFuture := strings.Replace(macPlistFixture,
		"<key>RunAtLoad</key>",
		"<key>__FUTURE_THING__</key>", 1)
	if err := os.WriteFile(src, []byte(withFuture), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	spec := InferdInstallSpec{BinaryDir: filepath.Join(dir, "fakebin")}
	r := &InferdInstallResult{}
	err := copyMacPlistWithFixes(src, dst, spec, r)
	if err == nil {
		t.Fatal("expected error on unknown placeholder, got nil")
	}
	if !strings.Contains(err.Error(), "__FUTURE_THING__") {
		t.Errorf("error should name the unknown placeholder; got: %v", err)
	}
}

func TestFindLeftoverPlaceholderIgnoresNonPlaceholderUnderscores(t *testing.T) {
	cases := map[string]string{
		"plain text without underscores":                "",
		"some snake_case_var no markers":                "",
		"__GOOD__":                                       "__GOOD__",
		"prefix __ALSO_GOOD__ suffix":                   "__ALSO_GOOD__",
		"__lower__ shouldn't match (lowercase)":         "",
		"__1NUM__ shouldn't match (digits)":             "",
		"__A__ then __B__ — first one wins":             "__A__",
	}
	for in, want := range cases {
		got := findLeftoverPlaceholder(in)
		if got != want {
			t.Errorf("findLeftoverPlaceholder(%q) = %q; want %q", in, got, want)
		}
	}
}
