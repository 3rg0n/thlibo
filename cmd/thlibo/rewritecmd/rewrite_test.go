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
	want := "thlibo exec -- git status --short\n"
	if got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRewriteWrapsNpm(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = Run([]string{"npm", "install"}) })
	if code != ExitRewrite {
		t.Errorf("exit = %d, want %d", code, ExitRewrite)
	}
	if !strings.HasPrefix(out, "thlibo exec -- npm install") {
		t.Errorf("stdout = %q", out)
	}
}

func TestRewriteWrapsCargo(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = Run([]string{"cargo", "test"}) })
	if code != ExitRewrite {
		t.Errorf("exit = %d, want %d", code, ExitRewrite)
	}
	if !strings.HasPrefix(out, "thlibo exec -- cargo test") {
		t.Errorf("stdout = %q", out)
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
	if !strings.HasPrefix(out, "thlibo exec -- /usr/bin/git status") {
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
