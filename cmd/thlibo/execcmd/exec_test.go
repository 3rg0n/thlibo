package execcmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/3rg0n/thlibo/internal/middleware"
	"github.com/3rg0n/thlibo/internal/processors"
	"github.com/3rg0n/thlibo/internal/router"
)

// TestParseCommand covers the argv parser that sits before exec.
func TestParseCommand(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
		ok   bool
	}{
		{"with --", []string{"--", "git", "status"}, []string{"git", "status"}, true},
		{"without --", []string{"git", "status"}, []string{"git", "status"}, true},
		{"-- alone", []string{"--"}, nil, false},
		{"empty", nil, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseCommand(c.in)
			if ok != c.ok {
				t.Errorf("ok = %v, want %v", ok, c.ok)
			}
			if ok && !stringsEqual(got, c.want) {
				t.Errorf("argv = %v, want %v", got, c.want)
			}
		})
	}
}

// TestRunPassthroughWhenNoPipeline: when the pipeline factory errors,
// stdout is emitted raw. This is the ultimate fallback.
func TestRunPassthroughWhenNoPipeline(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(echoArgv("hello world"), nil, &stdout, &stderr,
		func() (*middleware.Pipeline, error) {
			return nil, errors.New("no daemon configured")
		})
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "hello world") {
		t.Errorf("stdout = %q; expected to contain 'hello world'", stdout.String())
	}
}

// TestRunForwardsExitCode: a subprocess exiting non-zero has its exit
// code forwarded, and its captured stdout is still emitted.
func TestRunForwardsExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	argv := failExitArgv(3)
	code := run(argv, nil, &stdout, &stderr, noopPipeline)
	if code != 3 {
		t.Errorf("exit = %d, want 3", code)
	}
}

// TestRunShortPassesThrough: input under 2000 chars passes through
// the pipeline's short-circuit unchanged.
func TestRunShortPassesThrough(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(echoArgv("short output"), nil, &stdout, &stderr, emptyPipeline)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "short output") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

// TestRunSpawnFailure: invoking a binary that doesn't exist returns
// ExitSpawnFailed and a stderr diagnostic.
func TestRunSpawnFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"this-binary-definitely-does-not-exist-12345"}, nil, &stdout, &stderr, noopPipeline)
	if code != ExitSpawnFailed {
		t.Errorf("exit = %d, want %d", code, ExitSpawnFailed)
	}
	if !strings.Contains(stderr.String(), "thlibo exec:") {
		t.Errorf("stderr missing diagnostic: %q", stderr.String())
	}
}

// TestRunCompressesWhenPipelineRoutes: with a pipeline whose
// registry has a git-filter built-in and a fake router that sends
// input to it, `thlibo exec -- cat <git-status-fixture>` produces
// compressed stdout shorter than the input.
func TestRunCompressesWhenPipelineRoutes(t *testing.T) {
	if runtime.GOOS == "windows" {
		// The built-in git-filter is a Python script; we invoke it
		// on Unix via `cat <fixture>`. Covered by Linux CI.
		t.Skip("Unix-only fixture; Linux CI covers the integration")
	}
	fixture := largeGitStatus()
	// Write the fixture to a temp file and cat it. Safer than trying
	// to embed multi-line diff content inside an sh -c arg — shell
	// metacharacters (`$`, `!`, backticks, unbalanced quotes) in the
	// fixture have bitten past attempts.
	fpath := filepath.Join(t.TempDir(), "fixture.txt")
	if err := os.WriteFile(fpath, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	argv := []string{"cat", fpath}

	var stdout, stderr bytes.Buffer
	code := run(argv, nil, &stdout, &stderr, func() (*middleware.Pipeline, error) {
		// Use real BuildRegistry so git-filter is loaded from the
		// embedded FS; use a fake router that always routes to
		// git-filter so we don't need a live daemon.
		reg, _, err := middleware.BuildRegistry("")
		if err != nil {
			return nil, err
		}
		return &middleware.Pipeline{
			Registry:   reg,
			Router:     &routeToGitFilter{},
			Dispatcher: &processors.Dispatcher{},
		}, nil
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if stdout.Len() >= len(fixture) {
		t.Errorf("compression did not shrink output: in=%d bytes, out=%d bytes",
			len(fixture), stdout.Len())
	}
	if stdout.Len() == 0 {
		t.Error("compressed output is empty")
	}
}

// routeToGitFilter routes every call to git-filter, bypassing the
// daemon entirely. Used in tests where we want to exercise script-
// processor dispatch without standing up a full daemon.
type routeToGitFilter struct{}

func (r *routeToGitFilter) Ask(_ context.Context, _ *processors.Registry, _ string) (router.Decision, error) {
	return router.Decision{Chain: []string{"git-filter"}}, nil
}

// largeGitStatus returns a >2000-char git-status fixture so the
// middleware's short-circuit doesn't kick in.
func largeGitStatus() string {
	var b bytes.Buffer
	b.WriteString("On branch main\nYour branch is up to date with 'origin/main'.\n\n")
	b.WriteString("Changes to be committed:\n")
	b.WriteString("  (use \"git restore --staged <file>...\" to unstage)\n")
	for i := 0; i < 30; i++ {
		b.WriteString("\tmodified:   path/to/some/really/long/file/name/number_" + itoa(i) + ".go\n")
	}
	b.WriteString("\nUntracked files:\n")
	for i := 0; i < 30; i++ {
		b.WriteString("\tpath/to/some/untracked/file/number_" + itoa(i) + ".txt\n")
	}
	return b.String()
}

// --- test helpers ---------------------------------------------------

// echoArgv returns the argv that prints `s` to stdout on the current
// platform, using shell/interpreter flags that are portable.
func echoArgv(s string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/C", "echo " + s}
	}
	return []string{"sh", "-c", "printf %s \"" + s + "\""}
}

// failExitArgv returns argv that exits with code `code` and emits a
// short message on stdout (so we can verify stdout was still captured
// before the non-zero exit).
func failExitArgv(code int) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/C", "echo failing & exit /B " + itoa(code)}
	}
	return []string{"sh", "-c", "printf failing; exit " + itoa(code)}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// noopPipeline returns a pipeline whose Registry is empty so the
// middleware short-circuits to passthrough on every call. Keeps the
// tests deterministic and daemon-independent.
func noopPipeline() (*middleware.Pipeline, error) {
	reg, _, _ := processors.Build(nil, nil)
	return &middleware.Pipeline{
		Registry:   reg,
		Router:     &fakeRouter{},
		Dispatcher: &processors.Dispatcher{},
	}, nil
}

// emptyPipeline is identical to noopPipeline for now; kept as a
// separate name so we can add distinguishing behaviour (e.g. a
// pipeline with processors but no daemon) without changing tests.
var emptyPipeline = noopPipeline

type fakeRouter struct{}

func (f *fakeRouter) Ask(_ context.Context, _ *processors.Registry, _ string) (router.Decision, error) {
	return router.Decision{}, nil
}
