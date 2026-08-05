package installcmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/3rg0n/thlibo/internal/install"
)

// setHome points os.UserHomeDir() at dir. Windows reads USERPROFILE,
// POSIX reads HOME; set both so the tests are cross-platform.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("USERPROFILE", dir)
	t.Setenv("HOME", dir)
}

// capture redirects os.Stdout and os.Stderr for the duration of fn.
// Run prints its plan with fmt.Print* rather than through an injected
// writer, so capturing the real descriptors is the only way to assert on
// what a user sees. Files, not buffers, because os.Stdout must be an
// *os.File.
func capture(t *testing.T, fn func() int) (code int, stdout, stderr string) {
	t.Helper()

	dir := t.TempDir()
	outPath := filepath.Join(dir, "out")
	errPath := filepath.Join(dir, "err")
	outF, err := os.Create(outPath) // #nosec G304 -- t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	errF, err := os.Create(errPath) // #nosec G304 -- t.TempDir()
	if err != nil {
		t.Fatal(err)
	}

	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outF, errF
	code = fn()
	os.Stdout, os.Stderr = origOut, origErr

	_ = outF.Close()
	_ = errF.Close()

	o, err := os.ReadFile(outPath) // #nosec G304 -- t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	e, err := os.ReadFile(errPath) // #nosec G304 -- t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	return code, string(o), string(e)
}

// dryRunArgs is the baseline: --dry-run so nothing is written, and
// --skip-inferd because the inferd path downloads a release tarball from
// GitHub and is not a unit test.
func dryRunArgs(extra ...string) []string {
	return append([]string{"--dry-run", "--skip-inferd"}, extra...)
}

// --dry-run must report the plan and change nothing on disk. This is the
// property that makes it safe to recommend before a real install, so the
// assertion is on the filesystem, not just the exit code.
func TestInstallDryRunWritesNothing(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("THLIBO_PROCESSORS_DIR", filepath.Join(home, ".thlibo", "processors"))

	code, out, _ := capture(t, func() int { return Run(dryRunArgs()) })
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "dry-run: no changes applied") {
		t.Errorf("dry-run did not say so:\n%s", out)
	}

	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("--dry-run created %v under HOME; it must write nothing", names)
	}
}

// An unknown flag is exit 2 and must not proceed to the plan — a typo'd
// flag that fell through to a real install would mutate settings.json.
func TestInstallUnknownFlagExits2(t *testing.T) {
	setHome(t, t.TempDir())

	code, out, _ := capture(t, func() int { return Run([]string{"--nope"}) })
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if strings.Contains(out, "install plan") {
		t.Error("a usage error still printed the install plan")
	}
}

// --skip-hook returns before the Codex/Cursor/Copilot blocks, so pairing
// it with any of those flags silently no-ops that install. Each
// combination must warn by name, and the warning must name the flag the
// user passed rather than a generic message.
func TestInstallSkipHookWarnsPerClientFlag(t *testing.T) {
	for _, tc := range []struct{ flag, want string }{
		{"--codex", "--codex is ignored with --skip-hook"},
		{"--cursor", "--cursor is ignored with --skip-hook"},
		{"--copilot", "--copilot is ignored with --skip-hook"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			setHome(t, t.TempDir())
			t.Setenv("THLIBO_PROCESSORS_DIR", t.TempDir())

			code, _, errOut := capture(t, func() int {
				return Run(dryRunArgs("--skip-hook", tc.flag))
			})
			if code != 0 {
				t.Fatalf("exit = %d, want 0", code)
			}
			if !strings.Contains(errOut, tc.want) {
				t.Errorf("no warning for %s + --skip-hook:\n%s", tc.flag, errOut)
			}
		})
	}
}

// Without --skip-hook there must be no warning: a spurious "ignored"
// message on a working install would train users to ignore it.
func TestInstallNoSkipHookWarningWhenHookInstalled(t *testing.T) {
	setHome(t, t.TempDir())
	t.Setenv("THLIBO_PROCESSORS_DIR", t.TempDir())

	_, _, errOut := capture(t, func() int { return Run(dryRunArgs("--codex")) })
	if strings.Contains(errOut, "ignored with --skip-hook") {
		t.Errorf("warned about --skip-hook when it was not passed:\n%s", errOut)
	}
}

// The plan must report the actual paths that will be touched, and say
// "(skipped)" for what won't. This is the whole value of --dry-run: a
// plan that omitted the settings file would hide the one write that
// modifies a file the user already owns.
func TestInstallDryRunPlanReportsTargets(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	procDir := t.TempDir()
	t.Setenv("THLIBO_PROCESSORS_DIR", procDir)

	_, out, _ := capture(t, func() int { return Run(dryRunArgs()) })

	for _, want := range []string{
		procDir,
		filepath.Join(home, ".thlibo", "hooks", "thlibo-rewrite.sh"),
		filepath.Join(home, ".claude", "settings.json"),
		"codex hooks:    (skipped; use --codex to install)",
		"cursor hooks:   (skipped; use --cursor to install)",
		"copilot hooks:  (skipped; use --copilot to install)",
		"inferd:         (skipped; --skip-inferd)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan is missing %q:\n%s", want, out)
		}
	}
}

// --skip-hook reports the settings file as skipped rather than naming a
// path it will not write.
func TestInstallSkipHookPlanSaysSettingsSkipped(t *testing.T) {
	setHome(t, t.TempDir())
	t.Setenv("THLIBO_PROCESSORS_DIR", t.TempDir())

	_, out, _ := capture(t, func() int { return Run(dryRunArgs("--skip-hook")) })
	if !strings.Contains(out, "settings file:  (skipped)") {
		t.Errorf("plan does not mark settings as skipped under --skip-hook:\n%s", out)
	}
}

// --inferd-version is reported in the plan, so a user can see the pin
// took effect before committing to the install.
func TestInstallPlanReportsInferdPin(t *testing.T) {
	setHome(t, t.TempDir())
	t.Setenv("THLIBO_PROCESSORS_DIR", t.TempDir())

	// Not --skip-inferd here: the pin line and the skip line are
	// mutually exclusive branches, and this asserts the pin one.
	_, out, _ := capture(t, func() int {
		return Run([]string{"--dry-run", "--inferd-version", "v1.2.3"})
	})
	if !strings.Contains(out, "inferd:         pinned to v1.2.3") {
		t.Errorf("plan does not report the version pin:\n%s", out)
	}
}

// The client flags name their real target paths in the plan, and honour
// an explicit override. The defaults are the ones documented in
// CLAUDE.md; a drift here means an install writes somewhere the docs
// don't mention.
func TestInstallPlanReportsClientHookPaths(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("THLIBO_PROCESSORS_DIR", t.TempDir())

	_, out, _ := capture(t, func() int {
		return Run(dryRunArgs("--codex", "--cursor", "--copilot"))
	})

	for _, want := range []string{
		filepath.Join(home, ".codex", "config.toml"),
		filepath.Join(home, ".cursor", "hooks.json"),
		filepath.Join(home, ".copilot", "hooks", "thlibo.json"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan is missing client hook path %q:\n%s", want, out)
		}
	}

	// Overrides win over the defaults.
	override := filepath.Join(t.TempDir(), "custom-hooks.json")
	_, out2, _ := capture(t, func() int {
		return Run(dryRunArgs("--cursor", "--cursor-hooks", override))
	})
	if !strings.Contains(out2, override) {
		t.Errorf("--cursor-hooks override not reflected in the plan:\n%s", out2)
	}
}

// --processors-dir and --hook-dir override the ~/.thlibo defaults. The
// installer's own default resolution goes through
// install.DefaultProcessorsDir, so this also pins that the flag wins over
// the env var.
func TestInstallFlagsOverrideDefaultDirs(t *testing.T) {
	setHome(t, t.TempDir())
	envDir := t.TempDir()
	t.Setenv("THLIBO_PROCESSORS_DIR", envDir)

	flagProc := t.TempDir()
	flagHooks := t.TempDir()

	_, out, _ := capture(t, func() int {
		return Run(dryRunArgs("--processors-dir", flagProc, "--hook-dir", flagHooks))
	})
	if !strings.Contains(out, flagProc) {
		t.Errorf("--processors-dir not honoured:\n%s", out)
	}
	if strings.Contains(out, envDir) {
		t.Errorf("$THLIBO_PROCESSORS_DIR leaked into a plan that passed --processors-dir:\n%s", out)
	}
	if !strings.Contains(out, filepath.Join(flagHooks, "thlibo-rewrite.sh")) {
		t.Errorf("--hook-dir not honoured:\n%s", out)
	}
}

// The env var is the fallback when no flag is given — install.go calls
// DefaultProcessorsDir(), which reads it.
func TestInstallProcessorsDirEnvIsTheFallback(t *testing.T) {
	setHome(t, t.TempDir())
	envDir := t.TempDir()
	t.Setenv("THLIBO_PROCESSORS_DIR", envDir)

	_, out, _ := capture(t, func() int { return Run(dryRunArgs()) })
	if !strings.Contains(out, envDir) {
		t.Errorf("$THLIBO_PROCESSORS_DIR not used as the default:\n%s", out)
	}
	if envDir != install.DefaultProcessorsDir() {
		t.Errorf("DefaultProcessorsDir() = %q, want the env value %q", install.DefaultProcessorsDir(), envDir)
	}
}

// A real (non-dry-run) install with --skip-hook and --skip-inferd is the
// narrowest path that actually writes: it mirrors the built-in
// processors and stops. Worth covering because it is the only exit-4
// branch reachable without a network or a settings.json merge, and
// because "mirrored" must mean real files.
func TestInstallSkipHookMirrorsProcessorsOnly(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	procDir := filepath.Join(t.TempDir(), "processors")
	t.Setenv("THLIBO_PROCESSORS_DIR", procDir)

	code, out, errOut := capture(t, func() int {
		return Run([]string{"--skip-hook", "--skip-inferd", "--processors-dir", procDir})
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "mirrored built-in processors") {
		t.Errorf("did not report mirroring:\n%s", out)
	}
	if !strings.Contains(out, "install complete") {
		t.Errorf("did not report completion:\n%s", out)
	}

	// The mirror must have produced the built-ins on disk.
	if _, err := os.Stat(filepath.Join(procDir, "compress", "processor.md")); err != nil {
		t.Errorf("compress processor not mirrored: %v", err)
	}

	// --skip-hook means settings.json is untouched.
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("--skip-hook wrote settings.json (stat err = %v)", err)
	}
}

// A processors dir that cannot be created is exit 4. Uses a path whose
// parent is a regular file, which no OS will let MkdirAll traverse.
func TestInstallMirrorFailureExits4(t *testing.T) {
	setHome(t, t.TempDir())

	blocker := filepath.Join(t.TempDir(), "iam-a-file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	procDir := filepath.Join(blocker, "processors")

	code, _, errOut := capture(t, func() int {
		return Run([]string{"--skip-hook", "--skip-inferd", "--processors-dir", procDir})
	})
	if code != 4 {
		t.Fatalf("exit = %d, want 4 (mirror failure)\nstderr:\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "mirror processors") {
		t.Errorf("stderr does not identify the failing stage:\n%s", errOut)
	}
}

// hasTimeoutBinary gates a macOS-only coreutils hint. Only its agreement
// with PATH is meaningful, and it must not panic on a PATH with no
// timeout at all — which is what an empty PATH simulates.
func TestHasTimeoutBinaryFollowsPATH(t *testing.T) {
	t.Setenv("PATH", "")
	if hasTimeoutBinary() {
		t.Error("hasTimeoutBinary() = true with an empty PATH")
	}
}

// reportInferdInstall's branches are mutually exclusive and each prints
// a different sentence; a mis-ordered switch would report "already
// running" for a fresh download. Table over the four states plus the
// version-unknown fallbacks.
func TestReportInferdInstallNamesTheBranch(t *testing.T) {
	for _, tc := range []struct {
		name string
		ir   install.InferdInstallResult
		want []string
		deny []string
	}{
		{
			name: "used existing with version",
			ir:   install.InferdInstallResult{UsedExisting: true, ResolvedVersion: "v1.0.0"},
			want: []string{"inferd v1.0.0 already running"},
			deny: []string{"installed", "found installed"},
		},
		{
			name: "used existing without version",
			ir:   install.InferdInstallResult{UsedExisting: true},
			want: []string{"inferd already running"},
		},
		{
			name: "started existing",
			ir:   install.InferdInstallResult{StartedExisting: true, ResolvedVersion: "v1.1.0"},
			want: []string{"inferd v1.1.0 found installed; started"},
			deny: []string{"already running"},
		},
		{
			name: "installed fresh and reachable",
			ir:   install.InferdInstallResult{InstalledFresh: true, ResolvedVersion: "v1.2.0", Reachable: true},
			want: []string{"inferd v1.2.0 installed and started", "daemon reachable"},
		},
		{
			name: "installed fresh but unreachable",
			ir:   install.InferdInstallResult{InstalledFresh: true, ResolvedVersion: "v1.2.0"},
			want: []string{"not reachable yet"},
			deny: []string{"daemon reachable)"},
		},
		{
			name: "installed fresh with unknown version",
			ir:   install.InferdInstallResult{InstalledFresh: true, Reachable: true},
			want: []string{"(unknown version)"},
		},
		{
			name: "cosign + notes are appended",
			ir: install.InferdInstallResult{
				UsedExisting:   true,
				CosignVerified: true,
				Notes:          []string{"backends copied", "startup entry written"},
			},
			want: []string{"cosign signature verified", "backends copied", "startup entry written"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, out, _ := capture(t, func() int {
				reportInferdInstall(tc.ir)
				return 0
			})
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("output missing %q:\n%s", w, out)
				}
			}
			for _, d := range tc.deny {
				if strings.Contains(out, d) {
					t.Errorf("output should not contain %q:\n%s", d, out)
				}
			}
		})
	}
}

// linuxUserServiceHint is the installer's only warning about a Linux
// host where inferd cannot autostart. On any other platform it must
// return "" — macOS uses a LaunchAgent and Windows a Startup shortcut,
// so systemd advice there is pure noise.
func TestUserServiceHintSilentOffLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("the negative case is only assertable off linux; the linux paths are covered below")
	}
	if got := linuxUserServiceHint(); got != "" {
		t.Errorf("linuxUserServiceHint() on %s = %q, want empty", runtime.GOOS, got)
	}
}

// The hint has to name the consequence, not just the fault. A user who
// reads "systemctl --user failed" has no reason to connect that to
// "compression silently stopped working" — which is exactly what
// happens, because install.Install swallows the systemctl error by
// design and thlibo then fails open on every hook forever.
func TestUserServiceHintExplainsTheSilentFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	hint := linuxUserServiceHint()
	if hint == "" {
		// A working user session is the healthy case: nothing to say.
		// Assert that this really is why it is empty, so a hint that
		// broke outright can't hide behind this skip.
		if !userBusPresent() {
			t.Fatal("hint is empty but there is no user bus; the warning did not fire when it should have")
		}
		t.Skip("systemd user session is healthy on this host")
	}
	for _, want := range []string{
		"inferd will not autostart",
		"falls open",
		"inferd",
	} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint is missing %q — the user needs the consequence, not just the symptom:\n%s", want, hint)
		}
	}
	// Whatever the cause, the hint must leave the user with something to
	// run. An advisory with no command is a dead end.
	if !strings.Contains(hint, "systemctl") && !strings.Contains(hint, "inferd &") {
		t.Errorf("hint offers no actionable command:\n%s", hint)
	}
}

// --skip-inferd means the user is managing the daemon themselves, so
// warning them that it won't autostart is noise.
//
// Both the dry-run and the real path are checked because they are
// separate call sites: an earlier draft gated only the real one, so
// `--dry-run --skip-inferd` warned about a daemon the install it was
// previewing would not have warned about. A plan must not disagree with
// the install it describes.
//
// Runs everywhere: off linux the hint is empty regardless, so the
// assertion holds either way and the test earns no platform skip.
func TestSkipInferdSuppressesTheAutostartWarning(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"real install", []string{"--skip-inferd"}},
		{"dry run", []string{"--dry-run", "--skip-inferd"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setHome(t, t.TempDir())
			argv := append(tc.argv,
				"--processors-dir", filepath.Join(t.TempDir(), "processors"),
				"--hook-dir", filepath.Join(t.TempDir(), "hooks"),
				"--settings", filepath.Join(t.TempDir(), "claude", "settings.json"),
			)

			code, out, errOut := capture(t, func() int { return Run(argv) })
			if code != 0 {
				t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
			}
			if strings.Contains(out, "will not autostart") {
				t.Errorf("--skip-inferd still warned about autostart:\n%s", out)
			}
		})
	}
}

// userBusPresent is the systemd-user reachability probe, and the reason
// the hint can distinguish "no systemd at all" from "systemd but no user
// bus".
//
// The load-bearing case is "address set but socket missing", and it is
// not hypothetical: measured on WSL Ubuntu 26.04, where
// DBUS_SESSION_BUS_ADDRESS is exported as unix:path=/run/user/1000/bus
// while user@1000.service has failed and that socket does not exist. An
// earlier draft of this probe returned true whenever the variable was
// set, which reported healthy on precisely the host the hint exists for.
func TestUserBusPresentStatsTheAddressItIsGiven(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only: the probe is only consulted on linux")
	}

	present := filepath.Join(t.TempDir(), "bus")
	if err := os.WriteFile(present, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		addr string // DBUS_SESSION_BUS_ADDRESS; "" means unset
		xdg  string // XDG_RUNTIME_DIR; "" means unset
		want bool
	}{
		{"address names a socket that exists", "unix:path=" + present, "", true},
		{"address names a socket that does not exist", "unix:path=/nonexistent/bus", "", false},
		// The real 26.04 shape: several comma-separated keys, path among
		// them. Parsing must not depend on path= being first.
		{"address with extra keys", "unix:path=" + present + ",guid=abc123", "", true},
		{"multiple addresses, first unix wins", "unix:path=" + present + ";tcp:host=127.0.0.1", "", true},
		// Not a checkable filesystem path: warning on these would be a
		// false positive on a working host, so they pass.
		{"abstract socket is unverifiable and trusted", "unix:abstract=/tmp/dbus-xyz", "", true},
		{"tcp transport is unverifiable and trusted", "tcp:host=127.0.0.1,port=1234", "", true},
		// Falling back to $XDG_RUNTIME_DIR/bus when no address is set.
		{"no address, xdg has a bus", "", filepath.Dir(present), true},
		{"no address, xdg has no bus", "", t.TempDir(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DBUS_SESSION_BUS_ADDRESS", tc.addr)
			t.Setenv("XDG_RUNTIME_DIR", tc.xdg)
			if got := userBusPresent(); got != tc.want {
				t.Errorf("userBusPresent() with addr=%q xdg=%q = %v, want %v",
					tc.addr, tc.xdg, got, tc.want)
			}
		})
	}
}

// A full install with only --skip-inferd is the path a user actually
// runs, and at 35% coverage of Run it was the largest untested branch in
// the command. Everything it touches is under an isolated HOME, and
// --skip-inferd removes the only step that needs the network.
func TestInstallFullWritesHooksAndSettings(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	procDir := filepath.Join(t.TempDir(), "processors")
	hookDir := filepath.Join(t.TempDir(), "hooks")
	settings := filepath.Join(t.TempDir(), "claude", "settings.json")

	code, out, errOut := capture(t, func() int {
		return Run([]string{
			"--skip-inferd",
			"--processors-dir", procDir,
			"--hook-dir", hookDir,
			"--settings", settings,
		})
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}

	// All six Claude Code hook scripts: Bash + PowerShell for each of
	// exec, read, write. Installed unconditionally — the runtime picks
	// the matcher it uses, so a missing variant breaks that host only.
	for _, name := range []string{
		"thlibo-rewrite.sh", "thlibo-rewrite.ps1",
		"thlibo-read.sh", "thlibo-read.ps1",
		"thlibo-write.sh", "thlibo-write.ps1",
	} {
		if _, err := os.Stat(filepath.Join(hookDir, name)); err != nil {
			t.Errorf("hook script %s not written: %v", name, err)
		}
	}

	// settings.json merged, and the /caselog skill mirrored next to it.
	if _, err := os.Stat(settings); err != nil {
		t.Errorf("settings.json not written: %v", err)
	}
	skill := filepath.Join(filepath.Dir(settings), "skills", "caselog", "SKILL.md")
	if _, err := os.Stat(skill); err != nil {
		t.Errorf("/caselog skill not mirrored: %v", err)
	}

	if !strings.Contains(out, "merged Claude Code settings.json") {
		t.Errorf("did not report the settings merge:\n%s", out)
	}
	// Auto-shorthand writes to the user's own source files, so the
	// installer must say it is off by default. Silence here is how a
	// user gets surprised by rewritten files.
	if !strings.Contains(out, "auto-shorthand is OFF by default") {
		t.Errorf("did not state that Write/Edit auto-shorthand is off:\n%s", out)
	}
}

// Reinstalling over an existing install must be idempotent: exit 0 and
// no .new conflict files, because the hooks are byte-identical to what
// was just written. A spurious conflict on every upgrade would train
// users to ignore the one that matters.
func TestInstallIsIdempotent(t *testing.T) {
	setHome(t, t.TempDir())
	procDir := filepath.Join(t.TempDir(), "processors")
	hookDir := filepath.Join(t.TempDir(), "hooks")
	settings := filepath.Join(t.TempDir(), "claude", "settings.json")
	argv := []string{
		"--skip-inferd",
		"--processors-dir", procDir,
		"--hook-dir", hookDir,
		"--settings", settings,
	}

	if code, out, errOut := capture(t, func() int { return Run(argv) }); code != 0 {
		t.Fatalf("first install exit = %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	code, out, errOut := capture(t, func() int { return Run(argv) })
	if code != 0 {
		t.Fatalf("reinstall exit = %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if strings.Contains(out, "edits preserved") {
		t.Errorf("reinstall reported a conflict against its own unmodified output:\n%s", out)
	}

	entries, err := os.ReadDir(hookDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".new") {
			t.Errorf("reinstall created %s; hooks it wrote itself must not conflict", e.Name())
		}
	}
}

// A user-edited hook must survive reinstall: the new version lands at
// <path>.new and the edit stays in place (CLAUDE.md invariant #6). This
// is the conflict branch the test above asserts does NOT fire, so both
// are needed — otherwise "no conflict" could mean the detection is
// broken rather than that there was nothing to conflict with.
func TestInstallPreservesUserEditedHook(t *testing.T) {
	setHome(t, t.TempDir())
	procDir := filepath.Join(t.TempDir(), "processors")
	hookDir := filepath.Join(t.TempDir(), "hooks")
	settings := filepath.Join(t.TempDir(), "claude", "settings.json")
	argv := []string{
		"--skip-inferd",
		"--processors-dir", procDir,
		"--hook-dir", hookDir,
		"--settings", settings,
	}

	if code, _, errOut := capture(t, func() int { return Run(argv) }); code != 0 {
		t.Fatalf("first install failed: %s", errOut)
	}

	hook := filepath.Join(hookDir, "thlibo-rewrite.sh")
	edited := []byte("#!/usr/bin/env bash\n# my local edit\nexit 0\n")
	if err := os.WriteFile(hook, edited, 0o700); err != nil { // #nosec G306 -- hook must be executable
		t.Fatal(err)
	}

	code, out, errOut := capture(t, func() int { return Run(argv) })
	if code != 0 {
		t.Fatalf("reinstall exit = %d\nstderr:\n%s", code, errOut)
	}
	if !strings.Contains(out, "edits preserved") {
		t.Errorf("reinstall did not report preserving the user's edit:\n%s", out)
	}

	got, err := os.ReadFile(hook) // #nosec G304 -- t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(edited) {
		t.Errorf("user's edit was overwritten:\n%s", got)
	}
	if _, err := os.Stat(hook + ".new"); err != nil {
		t.Errorf("new version not written to %s.new: %v", hook, err)
	}
}

// The three client integrations each write their own files, and each has
// its own exit code on failure (9 codex, 10 cursor, 11 copilot). This
// covers their success paths together, since they are independent
// branches of the same Run.
func TestInstallClientHooksWriteTheirFiles(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	procDir := filepath.Join(t.TempDir(), "processors")
	hookDir := filepath.Join(t.TempDir(), "hooks")
	settings := filepath.Join(t.TempDir(), "claude", "settings.json")

	code, out, errOut := capture(t, func() int {
		return Run([]string{
			"--skip-inferd", "--codex", "--cursor", "--copilot",
			"--processors-dir", procDir,
			"--hook-dir", hookDir,
			"--settings", settings,
		})
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}

	// Codex: an inline hook in config.toml (not a sibling hooks.json —
	// #170 moved it), plus its own hook script.
	codexCfg := filepath.Join(home, ".codex", "config.toml")
	cfg, err := os.ReadFile(codexCfg) // #nosec G304 -- t.TempDir()-rooted HOME
	if err != nil {
		t.Fatalf("codex config.toml not written: %v", err)
	}
	if !strings.Contains(string(cfg), "hooks.PostToolUse") {
		t.Errorf("codex config.toml has no inline PostToolUse hook:\n%s", cfg)
	}
	// The canonical feature flag: without it Codex ignores the hook
	// entirely (see the [features] hooks rename).
	if !strings.Contains(string(cfg), "hooks") {
		t.Errorf("codex config.toml missing the hooks feature flag:\n%s", cfg)
	}
	// Codex requires interactive trust, which the installer cannot do —
	// so it must tell the user, or compression is silently off.
	if !strings.Contains(out, "ACTION REQUIRED") || !strings.Contains(out, "/hooks") {
		t.Errorf("codex install did not surface the /hooks trust step:\n%s", out)
	}

	// Cursor and Copilot each own a JSON file.
	for _, p := range []string{
		filepath.Join(home, ".cursor", "hooks.json"),
		filepath.Join(home, ".copilot", "hooks", "thlibo.json"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s not written: %v", p, err)
		}
	}

	// Per-client hook scripts land in the hook dir.
	for _, name := range []string{
		"thlibo-rewrite-codex.sh",
		"thlibo-rewrite-cursor.sh",
		"thlibo-read-cursor.sh",
	} {
		if _, err := os.Stat(filepath.Join(hookDir, name)); err != nil {
			t.Errorf("client hook script %s not written: %v", name, err)
		}
	}
}
