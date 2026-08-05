package upgradecmd

import (
	"os"
	"testing"
)

// stubInstaller replaces the network-reaching installer dispatch for the
// duration of a test and reports whether it was called plus what
// THLIBO_VERSION was in the environment at the time — the flag's only
// observable effect on the installer script.
func stubInstaller(t *testing.T, code int) (called *bool, sawVersion *string) {
	t.Helper()
	var wasCalled bool
	var seen string
	orig := runInstaller
	runInstaller = func() int {
		wasCalled = true
		seen = os.Getenv("THLIBO_VERSION")
		return code
	}
	t.Cleanup(func() { runInstaller = orig })
	return &wasCalled, &seen
}

// An unknown flag is a usage error (64, BSD sysexits) and must NOT run
// the installer — a typo'd flag silently upgrading to latest would be
// the worst outcome here.
func TestUpgradeUnknownFlagIsUsageError(t *testing.T) {
	called, _ := stubInstaller(t, 0)

	if code := Run([]string{"--nope"}); code != 64 {
		t.Fatalf("exit = %d, want 64", code)
	}
	if *called {
		t.Error("installer ran despite a usage error")
	}
}

// -h is flag.ErrHelp, which is a successful help request, not a usage
// error: exit 0 and no installer run. Distinguishing this from the 64
// above is the whole reason Run inspects the parse error.
func TestUpgradeHelpExitsZeroWithoutInstalling(t *testing.T) {
	called, _ := stubInstaller(t, 0)

	if code := Run([]string{"-h"}); code != 0 {
		t.Fatalf("exit = %d, want 0 for -h", code)
	}
	if *called {
		t.Error("installer ran for -h")
	}
}

// --version <tag> is a convenience wrapper over THLIBO_VERSION: the flag
// must be visible in the environment by the time the installer script
// runs, since that is the only channel the script reads it from.
func TestUpgradeVersionFlagExportsEnv(t *testing.T) {
	t.Setenv("THLIBO_VERSION", "")
	called, saw := stubInstaller(t, 0)

	if code := Run([]string{"--version", "v0.9.9"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !*called {
		t.Fatal("installer was not run")
	}
	if *saw != "v0.9.9" {
		t.Errorf("THLIBO_VERSION at installer time = %q, want %q", *saw, "v0.9.9")
	}
}

// No --version means no export: the installer must be left to resolve
// "latest" itself rather than being pinned to an empty tag, which the
// script would treat as a literal version and fail on.
func TestUpgradeWithoutVersionFlagLeavesEnvAlone(t *testing.T) {
	t.Setenv("THLIBO_VERSION", "v1.2.3-preexisting")
	called, saw := stubInstaller(t, 0)

	if code := Run(nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !*called {
		t.Fatal("installer was not run")
	}
	if *saw != "v1.2.3-preexisting" {
		t.Errorf("THLIBO_VERSION = %q; an absent --version must not overwrite a caller's value", *saw)
	}
}

// The installer's exit code is Run's exit code, unmodified. A failed
// upgrade must not report success — the user would keep running the old
// binary believing it was replaced.
func TestUpgradePropagatesInstallerFailure(t *testing.T) {
	stubInstaller(t, 1)

	if code := Run(nil); code != 1 {
		t.Fatalf("exit = %d, want 1 — installer failure must propagate", code)
	}
}

// runUnix/runWindows themselves are deliberately untested: both do
// nothing but shell out to the signed installer at a live URL, so a test
// would either hit the network or assert the contents of an exec.Command
// struct. The behaviour worth protecting is upstream of them — argv
// handling, THLIBO_VERSION export, exit-code propagation — and that is
// what the tests above cover through the runInstaller seam.
