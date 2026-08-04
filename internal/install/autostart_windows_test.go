//go:build windows

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the Windows autostart backend. Windows-only by build tag,
// matching the file under test — CI runs windows-latest, so these do
// execute (see .github/workflows/ci.yml).
//
// cmdBody generates a batch script that Windows runs at every logon, and
// quoteIfNeeded is the only thing standing between a path containing a
// space or an `&` and a broken — or differently-behaving — command line.
// Both were at 0% coverage. This is the same class of bug as a shell
// injection even though the input is a filesystem path rather than user
// text: `C:\Program Files\x & calc.exe` in a .cmd file is two commands.

func TestQuoteIfNeeded(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain path untouched", `C:\Tools\inferd.exe`, `C:\Tools\inferd.exe`},
		{"flag untouched", "--verbose", "--verbose"},
		{"empty becomes empty quotes", "", `""`},
		{"space quoted", `C:\Program Files\inferd.exe`, `"C:\Program Files\inferd.exe"`},
		// The metacharacters are the point: unquoted, cmd.exe treats each
		// of these as syntax rather than as part of the path.
		{"ampersand quoted", `C:\a&b`, `"C:\a&b"`},
		{"pipe quoted", `C:\a|b`, `"C:\a|b"`},
		{"redirect in quoted", `C:\a<b`, `"C:\a<b"`},
		{"redirect out quoted", `C:\a>b`, `"C:\a>b"`},
		{"caret quoted", `C:\a^b`, `"C:\a^b"`},
		// cmd.exe's escape for a literal quote inside a quoted string is
		// a doubled quote, not a backslash.
		{"embedded quote doubled", `a"b`, `"a""b"`},
		// Backslashes are left alone deliberately — cmd.exe does not treat
		// them as escapes, and doubling them would corrupt every path.
		{"trailing backslash not escaped", `C:\dir\`, `C:\dir\`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := quoteIfNeeded(tc.in); got != tc.want {
				t.Errorf("quoteIfNeeded(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestQuoteIfNeededNeutralisesCommandSeparators is the security-shaped
// assertion: whatever the exact quoting, a path carrying a command
// separator must not leave that separator sitting outside quotes in the
// generated script, because there it would run as a second command at
// logon.
func TestQuoteIfNeededNeutralisesCommandSeparators(t *testing.T) {
	for _, payload := range []string{
		`C:\tmp\x & calc.exe`,
		`C:\tmp\x | calc.exe`,
		`C:\tmp\x&&calc.exe`,
	} {
		got := quoteIfNeeded(payload)
		if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
			t.Errorf("quoteIfNeeded(%q) = %q, want the whole value wrapped in quotes", payload, got)
		}
	}
}

func TestCmdBodyStructure(t *testing.T) {
	w := &windowsInstaller{startupDir: t.TempDir()}
	body := w.cmdBody(AutostartSpec{
		Name:       "cisco.thlibo.daemon",
		DaemonPath: `C:\Program Files\inferd\inferd.exe`,
		Args:       []string{"--socket", `C:\Users\a b\pipe`},
		WorkingDir: `C:\Users\a b`,
	})

	if !strings.HasPrefix(body, "@echo off\r\n") {
		t.Errorf("body must start with @echo off; got %q", firstLine(body))
	}
	// CRLF throughout: a .cmd with bare LF endings is not reliably parsed
	// by cmd.exe.
	if strings.Contains(strings.ReplaceAll(body, "\r\n", ""), "\n") {
		t.Error("body contains a bare LF; .cmd files need CRLF line endings")
	}
	if !strings.Contains(body, `cd /D "C:\Users\a b"`) {
		t.Errorf("working dir not set with quoting; body:\n%s", body)
	}
	if !strings.Contains(body, `start "" /B "C:\Program Files\inferd\inferd.exe"`) {
		t.Errorf("daemon not launched detached+quoted; body:\n%s", body)
	}
	// An arg containing a space must be quoted, or the daemon receives two
	// arguments instead of one.
	if !strings.Contains(body, `--socket "C:\Users\a b\pipe"`) {
		t.Errorf("args not quoted correctly; body:\n%s", body)
	}
	if !strings.HasSuffix(body, "\r\n") {
		t.Error("body must end with a newline or cmd.exe may not run the last line")
	}
}

// TestCmdBodyOmitsWorkingDirWhenEmpty: an empty WorkingDir must not emit a
// bare `cd /D`, which would fail at logon and take the launch with it.
func TestCmdBodyOmitsWorkingDirWhenEmpty(t *testing.T) {
	w := &windowsInstaller{startupDir: t.TempDir()}
	body := w.cmdBody(AutostartSpec{Name: "n", DaemonPath: `C:\inferd.exe`})
	if strings.Contains(body, "cd /D") {
		t.Errorf("emitted a cd with no working dir; body:\n%s", body)
	}
}

// TestCmdBodyQuotesInjectedSeparators: a daemon path carrying a command
// separator must land inside quotes in the emitted script. The path is
// thlibo-resolved rather than user-typed, but this file is executed at
// every logon, so the quoting is worth pinning.
func TestCmdBodyQuotesInjectedSeparators(t *testing.T) {
	w := &windowsInstaller{startupDir: t.TempDir()}
	body := w.cmdBody(AutostartSpec{
		Name:       "n",
		DaemonPath: `C:\tmp\inferd.exe & calc.exe`,
	})
	if !strings.Contains(body, `start "" /B "C:\tmp\inferd.exe & calc.exe"`) {
		t.Errorf("separator-bearing path was not quoted; body:\n%s", body)
	}
}

// TestWindowsInstallerRoundTrip drives Install → Status → Uninstall →
// Status against a temp directory standing in for the Startup folder.
// Covers the idempotency the Installer interface documents.
func TestWindowsInstallerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	w := &windowsInstaller{startupDir: dir}
	spec := AutostartSpec{Name: "cisco.thlibo.daemon", DaemonPath: `C:\inferd.exe`}

	if installed, err := w.Status(spec.Name); err != nil || installed {
		t.Fatalf("Status before install = (%v, %v), want (false, nil)", installed, err)
	}
	if err := w.Install(spec); err != nil {
		t.Fatalf("Install: %v", err)
	}
	shim := filepath.Join(dir, spec.Name+".cmd")
	if _, err := os.Stat(shim); err != nil {
		t.Fatalf("shim not written: %v", err)
	}
	if installed, err := w.Status(spec.Name); err != nil || !installed {
		t.Errorf("Status after install = (%v, %v), want (true, nil)", installed, err)
	}

	// Install is documented as idempotent, so a second call must succeed
	// and leave one valid shim.
	if err := w.Install(spec); err != nil {
		t.Errorf("second Install: %v", err)
	}
	body, err := os.ReadFile(shim)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "@echo off") {
		t.Errorf("re-install corrupted the shim: %q", firstLine(string(body)))
	}

	if err := w.Uninstall(spec.Name); err != nil {
		t.Errorf("Uninstall: %v", err)
	}
	if installed, err := w.Status(spec.Name); err != nil || installed {
		t.Errorf("Status after uninstall = (%v, %v), want (false, nil)", installed, err)
	}
	// Uninstall of something absent is a no-op, not an error: `thlibo
	// uninstall` runs on machines that never completed an install.
	if err := w.Uninstall(spec.Name); err != nil {
		t.Errorf("Uninstall of a missing entry returned %v, want nil", err)
	}
}

// TestWindowsInstallerCreatesStartupDir: Install must create the Startup
// folder when it doesn't exist rather than failing. It normally does exist,
// but a roaming profile or a fresh image may not have it yet.
func TestWindowsInstallerCreatesStartupDir(t *testing.T) {
	nested := filepath.Join(t.TempDir(), "Start Menu", "Programs", "Startup")
	w := &windowsInstaller{startupDir: nested}
	if err := w.Install(AutostartSpec{Name: "n", DaemonPath: `C:\inferd.exe`}); err != nil {
		t.Fatalf("Install into a missing dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(nested, "n.cmd")); err != nil {
		t.Errorf("shim not written after creating the dir: %v", err)
	}
}

func TestWindowsInstallerMechanism(t *testing.T) {
	w := &windowsInstaller{startupDir: t.TempDir()}
	if got := w.Mechanism(); got != "Startup folder" {
		t.Errorf("Mechanism = %q", got)
	}
}

// TestNewWindowsInstallerUsesAppData: the Startup path must be rooted at
// APPDATA when it's set, which is the per-user scope the package doc
// promises (no elevation, no system-wide registration).
func TestNewWindowsInstallerUsesAppData(t *testing.T) {
	t.Setenv("APPDATA", `C:\fake\AppData\Roaming`)
	inst, err := newWindowsInstaller()
	if err != nil {
		t.Fatal(err)
	}
	w, ok := inst.(*windowsInstaller)
	if !ok {
		t.Fatalf("newWindowsInstaller returned %T", inst)
	}
	want := filepath.Join(`C:\fake\AppData\Roaming`,
		"Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	if w.startupDir != want {
		t.Errorf("startupDir = %q, want %q", w.startupDir, want)
	}
}

// TestNewWindowsInstallerFallsBackToHome: with APPDATA unset the installer
// must still resolve a per-user path rather than erroring out.
//
// USERPROFILE is redirected to a temp dir so the assertion can require that
// path as a prefix. Merely checking for an "AppData\Roaming" substring
// would pass even if the function returned a hardcoded constant — the point
// is that it consulted os.UserHomeDir().
func TestNewWindowsInstallerFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APPDATA", "")
	t.Setenv("USERPROFILE", home) // what os.UserHomeDir reads on Windows

	inst, err := newWindowsInstaller()
	if err != nil {
		t.Fatalf("newWindowsInstaller with no APPDATA: %v", err)
	}
	w := inst.(*windowsInstaller)
	want := filepath.Join(home, "AppData", "Roaming",
		"Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	if w.startupDir != want {
		t.Errorf("startupDir = %q, want %q — the home fallback was not used",
			w.startupDir, want)
	}
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
