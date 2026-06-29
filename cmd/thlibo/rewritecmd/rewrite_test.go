package rewritecmd

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout runs fn and returns what it wrote to stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	<-done
	return buf.String()
}

// TestRewriteWrapsKnownCommand: a command whose argv[0] matches a
// built-in processor's commands list is wrapped with `thlibo exec --`.
func TestRewriteWrapsKnownCommand(t *testing.T) {
	var got string
	var code int
	out := captureStdout(t, func() {
		code = Run([]string{"git", "status", "--short"})
	})
	got = out
	if code != ExitRewrite {
		t.Errorf("exit code = %d, want %d", code, ExitRewrite)
	}
	// Output shape: <abs-path-to-thlibo> exec -- <original-command>\n
	// We key on the tail since the absolute path differs per run.
	wantSuffix := " exec -- git status --short\n"
	if !strings.HasSuffix(got, wantSuffix) {
		t.Errorf("stdout = %q, want suffix %q", got, wantSuffix)
	}
}

func TestRewriteWrapsNpm(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = Run([]string{"npm", "install"}) })
	if code != ExitRewrite {
		t.Errorf("exit = %d, want %d", code, ExitRewrite)
	}
	if !strings.Contains(out, " exec -- npm install") {
		t.Errorf("stdout = %q", out)
	}
}

func TestRewriteWrapsCargo(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = Run([]string{"cargo", "test"}) })
	if code != ExitRewrite {
		t.Errorf("exit = %d, want %d", code, ExitRewrite)
	}
	if !strings.Contains(out, " exec -- cargo test") {
		t.Errorf("stdout = %q", out)
	}
}

// TestRewriteWrapsScanners: single-purpose linters/scanners added to
// lint-filter's commands list (#43) are wrapped so their output routes
// through the filter. Excludes `go` deliberately (see next test).
func TestRewriteWrapsScanners(t *testing.T) {
	cases := []struct {
		argv []string
		tail string
	}{
		{[]string{"gosec", "./..."}, " exec -- gosec ./..."},
		{[]string{"staticcheck", "./..."}, " exec -- staticcheck ./..."},
		{[]string{"golangci-lint", "run"}, " exec -- golangci-lint run"},
		{[]string{"shellcheck", "script.sh"}, " exec -- shellcheck script.sh"},
	}
	for _, c := range cases {
		t.Run(c.argv[0], func(t *testing.T) {
			var code int
			out := captureStdout(t, func() { code = Run(c.argv) })
			if code != ExitRewrite {
				t.Errorf("%s: exit = %d, want %d (should wrap)", c.argv[0], code, ExitRewrite)
			}
			if !strings.Contains(out, c.tail) {
				t.Errorf("%s: stdout = %q, want to contain %q", c.argv[0], out, c.tail)
			}
		})
	}
}

// TestRewriteWrapsGoTest: `go test` (and `go test -v`) wraps via the
// go-test-filter's command_prefixes (#42 / subcommand-aware matching).
func TestRewriteWrapsGoTest(t *testing.T) {
	for _, argv := range [][]string{
		{"go", "test", "./..."},
		{"go", "test", "-v", "./..."},
		{"go", "test", "-run", "TestX", "./pkg"},
	} {
		t.Run(strings.Join(argv, "_"), func(t *testing.T) {
			var code int
			out := captureStdout(t, func() { code = Run(argv) })
			if code != ExitRewrite {
				t.Errorf("%v: exit = %d, want %d (go test should wrap)", argv, code, ExitRewrite)
			}
			if !strings.Contains(out, " exec -- "+strings.Join(argv, " ")) {
				t.Errorf("%v: stdout = %q", argv, out)
			}
		})
	}
}

// TestRewriteDoesNotWrapOtherGo: `go` is multiplexed — only `go test`
// wraps (via command_prefixes). go build/run/vet must NOT wrap.
// Guards against someone broadening the prefix to a bare `go`.
func TestRewriteDoesNotWrapOtherGo(t *testing.T) {
	for _, argv := range [][]string{
		{"go", "build", "./..."},
		{"go", "run", "main.go"},
		{"go", "vet", "./..."},
		{"go", "generate", "./..."},
	} {
		t.Run(strings.Join(argv, "_"), func(t *testing.T) {
			var code int
			out := captureStdout(t, func() { code = Run(argv) })
			if code != ExitPassthrough {
				t.Errorf("%v: exit = %d, want %d (must not wrap)", argv, code, ExitPassthrough)
			}
			if out != "" {
				t.Errorf("%v: stdout = %q, want empty", argv, out)
			}
		})
	}
}

// TestRewritePassthroughForUnknown: a command whose argv[0] is not in
// any processor's commands list returns exit 1 and writes nothing.
func TestRewritePassthroughForUnknown(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = Run([]string{"ls", "-la"}) })
	if code != ExitPassthrough {
		t.Errorf("exit = %d, want %d", code, ExitPassthrough)
	}
	if out != "" {
		t.Errorf("stdout should be empty, got %q", out)
	}
}

// TestRewritePassthroughForCompound: pipes, &&, ; bail out — we don't
// parse compound shell commands.
func TestRewritePassthroughForCompound(t *testing.T) {
	cases := []string{
		"git status | head -5",
		"git status && git log -1",
		"git status; ls",
		"echo $(git rev-parse HEAD)",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			var code int
			out := captureStdout(t, func() { code = Run(strings.Fields(c)) })
			if code != ExitPassthrough {
				t.Errorf("compound %q: exit = %d, want %d", c, code, ExitPassthrough)
			}
			if out != "" {
				t.Errorf("compound %q: unexpected stdout %q", c, out)
			}
		})
	}
}

// TestRewriteLoopGuard: a command that starts with `thlibo` must never
// be rewritten again — otherwise the hook would loop.
func TestRewriteLoopGuard(t *testing.T) {
	var code int
	out := captureStdout(t, func() {
		code = Run([]string{"thlibo", "exec", "--", "git", "status"})
	})
	if code != ExitPassthrough {
		t.Errorf("thlibo-prefixed cmd must passthrough, exit = %d", code)
	}
	if out != "" {
		t.Errorf("stdout should be empty, got %q", out)
	}
}

// TestRewritePassthroughFromPath: absolute path is resolved to its
// basename, so /usr/bin/git still maps to git-filter.
func TestRewriteResolvesAbsolutePath(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = Run([]string{"/usr/bin/git", "status"}) })
	if code != ExitRewrite {
		t.Errorf("exit = %d, want %d", code, ExitRewrite)
	}
	if !strings.Contains(out, " exec -- /usr/bin/git status") {
		t.Errorf("stdout = %q (want the original command preserved verbatim)", out)
	}
}

// TestRewriteMissingCommand: no argv means internal error.
func TestRewriteMissingCommand(t *testing.T) {
	var code int
	_ = captureStdout(t, func() { code = Run(nil) })
	if code != ExitInternal {
		t.Errorf("exit = %d, want %d", code, ExitInternal)
	}
}
