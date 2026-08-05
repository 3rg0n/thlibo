//go:build linux

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the Linux autostart backend. Linux-only by build tag,
// matching the file under test — CI runs ubuntu-latest, so these do
// execute (see .github/workflows/ci.yml).
//
// systemd-user is *the* Linux install mechanism (ADR 0005: thlibo
// install probe-or-installs inferd and registers it for autostart), and
// it had no test at all while the Windows backend had a full one. The
// unit body is the artifact users end up with on disk; the hardening
// directives in it are THREAT_MODEL findings #6 and #14, which means a
// silent drop is a security regression that nothing else would catch.

// newInstaller resolves the unit dir from $XDG_CONFIG_HOME, so a test
// can point it at a temp dir and assert on real files.
func testInstaller(t *testing.T) (*linuxInstaller, string) {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)

	inst, err := newLinuxInstaller()
	if err != nil {
		t.Fatalf("newLinuxInstaller: %v", err)
	}
	li, ok := inst.(*linuxInstaller)
	if !ok {
		t.Fatalf("newLinuxInstaller returned %T, want *linuxInstaller", inst)
	}
	want := filepath.Join(cfg, "systemd", "user")
	if li.unitDir != want {
		t.Fatalf("unitDir = %q, want %q", li.unitDir, want)
	}
	return li, want
}

// $XDG_CONFIG_HOME wins; without it the dir falls back to ~/.config.
// Getting this wrong writes the unit somewhere systemd does not read,
// and the failure is silent — the install reports success and the daemon
// never starts at login.
func TestLinuxInstallerUnitDirResolution(t *testing.T) {
	t.Run("XDG_CONFIG_HOME", func(t *testing.T) {
		testInstaller(t)
	})

	t.Run("falls back to ~/.config", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", home)

		inst, err := newLinuxInstaller()
		if err != nil {
			t.Fatalf("newLinuxInstaller: %v", err)
		}
		li := inst.(*linuxInstaller)
		want := filepath.Join(home, ".config", "systemd", "user")
		if li.unitDir != want {
			t.Errorf("unitDir = %q, want %q", li.unitDir, want)
		}
	})
}

// Install writes the unit and reports the right mechanism. The systemctl
// calls are best-effort by design (a bare container has no user bus), so
// Install must succeed and leave the file behind even when they fail —
// that is what makes a later `systemctl --user enable` work.
func TestLinuxInstallWritesUnitFile(t *testing.T) {
	li, unitDir := testInstaller(t)

	spec := AutostartSpec{
		Name:       "cisco.thlibo.daemon",
		DaemonPath: "/home/u/.local/bin/inferd-daemon",
		Args:       []string{"--backend", "llama"},
	}
	if err := li.Install(spec); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if li.Mechanism() != "systemd user unit" {
		t.Errorf("Mechanism() = %q", li.Mechanism())
	}

	path := filepath.Join(unitDir, spec.Name+".service")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("unit file not written: %v", err)
	}
	// 0600: the unit names the daemon binary and its args. Not secret,
	// but it is a file whose contents systemd executes, so it must not
	// be group- or world-writable.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("unit file mode = %o, want 600", perm)
	}
	// The parent dir must not be group-writable either — a writable
	// ~/.config/systemd/user lets any process in the group swap the
	// unit's ExecStart.
	dirInfo, err := os.Stat(unitDir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm&0o022 != 0 {
		t.Errorf("unit dir mode = %o; must not be group/world writable", perm)
	}
}

// The hardening directives are THREAT_MODEL findings #6 (restart storm)
// and #14 (filesystem confinement). Each is asserted by name: a refactor
// that reformatted the unit template and dropped one would otherwise
// pass every other test, and the loss is invisible until an incident.
func TestLinuxUnitBodyCarriesHardening(t *testing.T) {
	li, _ := testInstaller(t)

	body := li.unitBody(AutostartSpec{
		Name:       "cisco.thlibo.daemon",
		DaemonPath: "/usr/local/bin/inferd-daemon",
	})

	for _, want := range []string{
		// Finding #6: cap the restart loop for a persistently-dying daemon.
		"StartLimitIntervalSec=60",
		"StartLimitBurst=3",
		"Restart=on-failure",
		// Finding #14: confine the filesystem. ~/.thlibo read-write,
		// everything else under $HOME read-only, system dirs protected.
		"NoNewPrivileges=true",
		"PrivateDevices=true",
		"PrivateTmp=true",
		"ProtectSystem=strict",
		"ProtectHome=read-only",
		"ReadWritePaths=%h/.thlibo",
		"RuntimeDirectoryMode=0700",
		// Without this the unit is written but never starts at login.
		"WantedBy=default.target",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("unit body is missing %q:\n%s", want, body)
		}
	}
}

// ExecStart is a single systemd line, so DaemonPath and every arg must
// land on it in order. A dropped arg means the daemon starts with
// different configuration than the installer intended.
func TestLinuxUnitBodyExecStart(t *testing.T) {
	li, _ := testInstaller(t)

	body := li.unitBody(AutostartSpec{
		Name:       "cisco.thlibo.daemon",
		DaemonPath: "/opt/inferd/inferd-daemon",
		Args:       []string{"--uds", "/run/user/1000/inferd/inferd.sock", "--backend", "llama"},
		WorkingDir: "/opt/inferd",
	})

	wantExec := "ExecStart=/opt/inferd/inferd-daemon --uds /run/user/1000/inferd/inferd.sock --backend llama"
	if !strings.Contains(body, wantExec) {
		t.Errorf("ExecStart line wrong:\nwant substring: %s\ngot:\n%s", wantExec, body)
	}
	if !strings.Contains(body, "WorkingDirectory=/opt/inferd\n") {
		t.Errorf("WorkingDirectory not emitted:\n%s", body)
	}
}

// An empty WorkingDir must emit no WorkingDirectory= line at all.
// `WorkingDirectory=` with an empty value is a unit parse error, and
// systemd refuses to load the whole file — so the daemon silently never
// starts.
func TestLinuxUnitBodyOmitsEmptyWorkingDir(t *testing.T) {
	li, _ := testInstaller(t)

	body := li.unitBody(AutostartSpec{
		Name:       "cisco.thlibo.daemon",
		DaemonPath: "/usr/local/bin/inferd-daemon",
	})
	if strings.Contains(body, "WorkingDirectory=") {
		t.Errorf("emitted a WorkingDirectory= line for an empty WorkingDir:\n%s", body)
	}
}

// systemdEscape is the Linux counterpart to the Windows quoteIfNeeded,
// and the same class of bug: systemd's ExecStart parser splits on
// whitespace, so an unquoted path with a space becomes two arguments and
// the daemon starts with a truncated path.
func TestSystemdEscape(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"plain path untouched", "/usr/local/bin/inferd-daemon", "/usr/local/bin/inferd-daemon"},
		{"flag untouched", "--backend", "--backend"},
		{"empty untouched", "", ""},
		{"space quoted", "/opt/my apps/inferd", `"/opt/my apps/inferd"`},
		{"tab quoted", "/opt/a\tb", "\"/opt/a\tb\""},
		// A literal quote inside a quoted ExecStart token is escaped with
		// a backslash in systemd's syntax.
		{"embedded quote escaped", `/opt/a"b c`, `"/opt/a\"b c"`},
		// Not metacharacters to systemd's ExecStart parser (no shell is
		// involved unless the line starts with a shell), so quoting them
		// would change the path rather than protect it.
		{"ampersand untouched", "/opt/a&b", "/opt/a&b"},
		{"dollar untouched", "/opt/a$b", "/opt/a$b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := systemdEscape(tc.in); got != tc.want {
				t.Errorf("systemdEscape(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// An arg containing a space must survive into ExecStart quoted, so
// systemd passes it as one argument. This is the end-to-end version of
// the escape test: it is the composition that matters, since unitBody
// joins on a space after escaping.
func TestLinuxUnitBodyQuotesPathsWithSpaces(t *testing.T) {
	li, _ := testInstaller(t)

	body := li.unitBody(AutostartSpec{
		Name:       "cisco.thlibo.daemon",
		DaemonPath: "/opt/my apps/inferd-daemon",
		Args:       []string{"--model", "/home/u/My Models/gemma.gguf"},
	})

	want := `ExecStart="/opt/my apps/inferd-daemon" --model "/home/u/My Models/gemma.gguf"`
	if !strings.Contains(body, want) {
		t.Errorf("spaces not quoted in ExecStart:\nwant substring: %s\ngot:\n%s", want, body)
	}
}

// Status reports presence of the unit file, not systemd's runtime view —
// so it must be false before Install, true after, and false again after
// Uninstall. Anything else makes `thlibo install` misreport what it did.
func TestLinuxStatusTracksUnitFile(t *testing.T) {
	li, _ := testInstaller(t)
	const name = "cisco.thlibo.daemon"

	ok, err := li.Status(name)
	if err != nil {
		t.Fatalf("Status before install: %v", err)
	}
	if ok {
		t.Error("Status = true before Install")
	}

	if err := li.Install(AutostartSpec{Name: name, DaemonPath: "/usr/local/bin/inferd-daemon"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	ok, err = li.Status(name)
	if err != nil {
		t.Fatalf("Status after install: %v", err)
	}
	if !ok {
		t.Error("Status = false after Install")
	}

	if err := li.Uninstall(name); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	ok, err = li.Status(name)
	if err != nil {
		t.Fatalf("Status after uninstall: %v", err)
	}
	if ok {
		t.Error("Status = true after Uninstall")
	}
}

// Uninstall of something never installed must succeed: `thlibo
// uninstall` runs on boxes where install partially failed, and an error
// here would abort the rest of the teardown. os.IsNotExist is swallowed
// deliberately — this pins that.
func TestLinuxUninstallIsIdempotent(t *testing.T) {
	li, _ := testInstaller(t)

	if err := li.Uninstall("never.installed"); err != nil {
		t.Errorf("Uninstall of an absent unit = %v, want nil", err)
	}
	// And twice over a real one.
	const name = "cisco.thlibo.daemon"
	if err := li.Install(AutostartSpec{Name: name, DaemonPath: "/usr/local/bin/inferd-daemon"}); err != nil {
		t.Fatal(err)
	}
	if err := li.Uninstall(name); err != nil {
		t.Fatalf("first Uninstall: %v", err)
	}
	if err := li.Uninstall(name); err != nil {
		t.Errorf("second Uninstall = %v, want nil", err)
	}
}

// Install is documented idempotent: re-running `thlibo install` must
// overwrite the unit rather than fail or append. Re-installing with
// different args must leave exactly one ExecStart, carrying the new
// value — an appended second line would make systemd reject the unit.
func TestLinuxInstallIsIdempotent(t *testing.T) {
	li, unitDir := testInstaller(t)
	const name = "cisco.thlibo.daemon"

	if err := li.Install(AutostartSpec{
		Name: name, DaemonPath: "/usr/local/bin/inferd-daemon",
		Args: []string{"--backend", "mock"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := li.Install(AutostartSpec{
		Name: name, DaemonPath: "/usr/local/bin/inferd-daemon",
		Args: []string{"--backend", "llama"},
	}); err != nil {
		t.Fatalf("second Install: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(unitDir, name+".service")) // #nosec G304 -- t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if n := strings.Count(s, "ExecStart="); n != 1 {
		t.Errorf("unit has %d ExecStart lines after reinstall, want 1:\n%s", n, s)
	}
	if !strings.Contains(s, "--backend llama") {
		t.Errorf("reinstall did not replace the args:\n%s", s)
	}
	if strings.Contains(s, "--backend mock") {
		t.Errorf("reinstall left the previous args behind:\n%s", s)
	}
}
