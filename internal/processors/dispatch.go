package processors

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrEntrySwapped is returned when a script entry's (size, mtime, mode)
// differs from what the registry recorded at load time. Best-effort
// TOCTOU guard. See THREAT_MODEL.md finding #9.
var ErrEntrySwapped = errors.New("dispatch: entry file changed since registry load")

// Dispatcher runs a processor against input and returns the processed
// output. Every error path is wrapped - callers (the middleware main
// flow) decide whether to fall back to the original input.
type Dispatcher struct {
	// PromptClient is called to run a prompt processor through the
	// daemon. It takes the descriptor (for temperature/max_tokens/etc)
	// and the input text, and returns the daemon's response text.
	// Injected so tests can swap in a fake without a real daemon.
	PromptClient PromptRunner

	// ScriptTimeout bounds how long a single script processor run
	// may take. Zero = 30 seconds. Covers B8e (script hangs).
	ScriptTimeout time.Duration
}

// PromptRunner is the interface the dispatcher uses to call out to
// the daemon for prompt processors. The middleware wires this to a
// router.DaemonClient at runtime.
type PromptRunner interface {
	Run(ctx context.Context, d *Descriptor, input string) (string, error)
}

// Run executes a single processor against input. Script processors
// are piped stdin/stdout; prompt processors are sent through the
// daemon via PromptClient.
func (x *Dispatcher) Run(ctx context.Context, d *Descriptor, input string) (string, error) {
	switch d.Type {
	case KindScript:
		return x.runScript(ctx, d, input)
	case KindPrompt:
		if x.PromptClient == nil {
			return "", errors.New("dispatch: no prompt client configured")
		}
		return x.PromptClient.Run(ctx, d, input)
	default:
		return "", fmt.Errorf("dispatch: unknown processor type %q", d.Type)
	}
}

// runScript launches the entry file as a subprocess, pipes input to
// stdin, reads stdout to completion, and returns the captured bytes.
// Non-zero exit => error (B8d). Hang beyond timeout => error (B8e).
func (x *Dispatcher) runScript(ctx context.Context, d *Descriptor, input string) (string, error) {
	dir := d.Origin.DiskDir
	bin, args, err := d.EntryCommand(dir)
	if err != nil {
		return "", err
	}

	// Re-verify the entry fingerprint. If the registry captured one
	// (non-zero ModNs), compare against the current on-disk state; a
	// mismatch means the file was rewritten since load and the
	// dispatcher refuses to run it. Middleware treats this as any
	// other dispatch error and falls back to original input.
	if d.EntryFingerprint.ModNs != 0 {
		info, statErr := os.Stat(filepath.Join(dir, d.Entry))
		if statErr != nil {
			return "", fmt.Errorf("dispatch: %s: stat entry: %w", d.Name, statErr)
		}
		now := EntryFingerprint{
			Size:  info.Size(),
			ModNs: info.ModTime().UnixNano(),
			Mode:  uint32(info.Mode().Perm()),
		}
		if now != d.EntryFingerprint {
			return "", fmt.Errorf("%w: %s/%s", ErrEntrySwapped, d.Name, d.Entry)
		}
	}

	timeout := x.ScriptTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// #nosec G204 -- bin is from our EntryCommand (python3/bash/own file),
	// args is either [d.Entry] resolved under d.Origin.Path dir or empty;
	// both are trusted config values, not user input.
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd := exec.CommandContext(runCtx, bin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("dispatch: %s: timed out after %s", d.Name, timeout)
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("dispatch: %s: exit %d: %s", d.Name, ee.ExitCode(), trimStderr(stderr.Bytes()))
		}
		return "", fmt.Errorf("dispatch: %s: %w", d.Name, err)
	}
	return stdout.String(), nil
}

// RunChain runs the processors in order, feeding each one's stdout
// into the next one's stdin. A failure anywhere in the chain returns
// the error immediately; the middleware then decides fallback.
func (x *Dispatcher) RunChain(ctx context.Context, reg *Registry, names []string, input string) (string, error) {
	cur := input
	for _, name := range names {
		d := reg.Get(name)
		if d == nil {
			return "", fmt.Errorf("dispatch: unknown processor %q in chain", name)
		}
		next, err := x.Run(ctx, d, cur)
		if err != nil {
			return "", err
		}
		cur = next
	}
	return cur, nil
}

// WriteInput is a small helper for tests and adapters that need to
// shuttle input through an io.Reader. Not used by the dispatcher
// directly but kept co-located so both are obvious from one file.
func WriteInput(w io.Writer, input string) error {
	_, err := io.WriteString(w, input)
	return err
}

func trimStderr(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
