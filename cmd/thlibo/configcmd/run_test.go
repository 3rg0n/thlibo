package configcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/3rg0n/thlibo/internal/config"
)

// Run's flag dispatch and the runSet/runReset wrappers. applySet and
// writeConfig are covered directly elsewhere; what these add is the
// mapping from argv to exit code, and — for --reset — the confirmation
// gate, which is the branch standing between a stray keystroke and an
// overwritten config.

// isolateConfig points $THLIBO_CONFIG at a temp file so Run reads and
// writes there instead of ~/.thlibo/config.yaml. HOME is isolated too
// because config.Path() falls back to it.
func isolateConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	p := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("THLIBO_CONFIG", p)
	return p
}

// captureStdout swaps os.Stdout for the duration of fn. Run prints with
// fmt.Print*, so the real descriptor is the only way to see its output.
func captureStdout(t *testing.T, fn func() int) (int, string) {
	t.Helper()

	outPath := filepath.Join(t.TempDir(), "out")
	f, err := os.Create(outPath) // #nosec G304 -- t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = f
	code := fn()
	os.Stdout = orig
	_ = f.Close()

	b, err := os.ReadFile(outPath) // #nosec G304 -- t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	return code, string(b)
}

// withStdin points os.Stdin at a pipe carrying s, for the prompts.
func withStdin(t *testing.T, s string, fn func() int) (int, string) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = w.WriteString(s)
		_ = w.Close()
	}()
	orig := os.Stdin
	os.Stdin = r
	code, out := captureStdout(t, fn)
	os.Stdin = orig
	_ = r.Close()
	return code, out
}

// --path prints the resolved path and exits 0 without reading or
// writing the file. Scripts parse this, so it must be the path alone.
func TestRunPathPrintsResolvedPath(t *testing.T) {
	p := isolateConfig(t)

	code, out := captureStdout(t, func() int { return Run([]string{"--path"}) })
	if code != ExitOK {
		t.Fatalf("exit = %d, want ExitOK(%d)", code, ExitOK)
	}
	if strings.TrimSpace(out) != p {
		t.Errorf("stdout = %q, want %q", strings.TrimSpace(out), p)
	}
	// --path must not create the file: it is the one subcommand safe to
	// run before any config exists.
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("--path created the config file (err=%v)", err)
	}
}

// An unknown flag is exit 2 and must not write: a typo'd flag that fell
// through to interactive mode would block on stdin in a script.
func TestRunUnknownFlagExits2(t *testing.T) {
	p := isolateConfig(t)

	if code, _ := captureStdout(t, func() int { return Run([]string{"--nope"}) }); code != ExitUsage {
		t.Fatalf("exit = %d, want ExitUsage(%d)", code, ExitUsage)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("a usage error wrote the config file (err=%v)", err)
	}
}

// --set persists the value and is readable back through config.Load —
// the round trip is the point, since a write to the wrong path would
// still print success.
func TestRunSetPersistsValue(t *testing.T) {
	p := isolateConfig(t)

	code, out := captureStdout(t, func() int {
		return Run([]string{"--set", "auto_shorthand_on_write=true"})
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want ExitOK(%d)\n%s", code, ExitOK, out)
	}
	if !strings.Contains(out, p) {
		t.Errorf("did not report the file it wrote:\n%s", out)
	}
	if !config.Load().AutoShorthandOnWrite {
		t.Error("value did not survive the round trip through the config file")
	}
}

// --set's failure modes are distinct exit codes: malformed k=v is a
// usage error (2), while a well-formed setting that is invalid is 4.
// Collapsing them would make a typo'd key indistinguishable from a
// typo'd syntax in a script.
func TestRunSetErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		arg  string
		want int
	}{
		{"no equals sign", "auto_shorthand_on_write", ExitUsage},
		// Lowercase-with-underscores and a short value on purpose: a
		// longer synthetic key=value pair trips the pre-commit
		// gitleaks generic-api-key entropy rule.
		{"unknown key", "nope=1", ExitInvalidSetting},
		{"non-boolean for a bool field", "auto_shorthand_on_write=maybe", ExitInvalidSetting},
		{"non-integer for an int field", "auto_shorthand_min_bytes=lots", ExitInvalidSetting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := isolateConfig(t)

			code, _ := captureStdout(t, func() int { return Run([]string{"--set", tc.arg}) })
			if code != tc.want {
				t.Errorf("Run(--set %q) = %d, want %d", tc.arg, code, tc.want)
			}
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("a rejected --set still wrote the config file (err=%v)", err)
			}
		})
	}
}

// An empty value is legitimate, not a malformed pair: `--set
// auto_shorthand_paths=` is how a user clears a glob list. The SplitN
// yields two parts, so this must reach applySet rather than exit 2.
func TestRunSetAcceptsEmptyValue(t *testing.T) {
	isolateConfig(t)

	code, _ := captureStdout(t, func() int {
		return Run([]string{"--set", "auto_shorthand_paths="})
	})
	if code == ExitUsage {
		t.Error("--set key= was treated as a malformed pair; an empty value is how a list is cleared")
	}
}

// --reset without --yes prompts, and anything other than y/yes aborts
// with exit 5 leaving the file untouched. This is the gate that keeps a
// stray `thlibo config --reset` from discarding a user's settings.
func TestRunResetRequiresConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stdin string
		want  int
		kept  bool // true = the user's existing value must survive
	}{
		{"empty line aborts", "\n", ExitAborted, true},
		{"n aborts", "n\n", ExitAborted, true},
		{"no aborts", "no\n", ExitAborted, true},
		{"anything else aborts", "maybe\n", ExitAborted, true},
		{"y confirms", "y\n", ExitOK, false},
		{"yes confirms", "yes\n", ExitOK, false},
		{"YES is case-insensitive", "YES\n", ExitOK, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := isolateConfig(t)
			// A non-default value to reset away from, so "kept" is
			// observable rather than indistinguishable from defaults.
			if err := os.WriteFile(p, []byte("auto_shorthand_min_bytes: 4242\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			code, _ := withStdin(t, tc.stdin, func() int { return Run([]string{"--reset"}) })
			if code != tc.want {
				t.Fatalf("stdin %q: exit = %d, want %d", tc.stdin, code, tc.want)
			}

			got := config.Load().AutoShorthandMinBytes
			if tc.kept && got != 4242 {
				t.Errorf("an aborted reset changed min_bytes to %d; the user's value must survive", got)
			}
			if !tc.kept && got != config.Defaults().AutoShorthandMinBytes {
				t.Errorf("a confirmed reset left min_bytes at %d, want the default %d",
					got, config.Defaults().AutoShorthandMinBytes)
			}
		})
	}
}

// --reset --yes skips the prompt entirely: it must not read stdin, or it
// would hang in CI and in the installer.
func TestRunResetYesSkipsPrompt(t *testing.T) {
	p := isolateConfig(t)
	if err := os.WriteFile(p, []byte("auto_shorthand_min_bytes: 4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Stdin deliberately holds a refusal. With --yes it must be ignored;
	// if the prompt were still read, this would abort.
	code, _ := withStdin(t, "n\n", func() int { return Run([]string{"--reset", "--yes"}) })
	if code != ExitOK {
		t.Fatalf("exit = %d, want ExitOK(%d) — --yes must not consult stdin", code, ExitOK)
	}
	if got := config.Load().AutoShorthandMinBytes; got != config.Defaults().AutoShorthandMinBytes {
		t.Errorf("min_bytes = %d, want the default %d", got, config.Defaults().AutoShorthandMinBytes)
	}
}

// --show reports the active config and the path it came from, and must
// work before any file exists (printing defaults) rather than erroring.
func TestRunShowWorksWithNoConfigFile(t *testing.T) {
	p := isolateConfig(t)

	code, out := captureStdout(t, func() int { return Run([]string{"--show"}) })
	if code != ExitOK {
		t.Fatalf("exit = %d, want ExitOK(%d)", code, ExitOK)
	}
	if !strings.Contains(out, p) {
		t.Errorf("--show did not name the source path:\n%s", out)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("--show created the config file (err=%v)", err)
	}
}
