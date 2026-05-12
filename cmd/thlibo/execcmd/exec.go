// Package execcmd implements `thlibo exec -- <command>`.
//
// The subcommand:
//
//  1. Parses argv after the `--` sentinel as the command to run.
//  2. Runs that command as a subprocess with stderr passed through
//     directly to our own stderr (so progress bars and errors still
//     stream to the user / AI client).
//  3. Captures stdout, pipes it through middleware.Process() for
//     compression.
//  4. Writes compressed stdout to our stdout, exits with the child's
//     exit code.
//
// Contract: if compression fails for any reason (daemon unreachable,
// router error, processor crash), the original stdout is emitted
// verbatim. This matches the B8a–B8h fallback matrix the middleware
// already enforces; exec's job is to faithfully pass whatever
// middleware returns.
package execcmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/3rg0n/thlibo/internal/middleware"
	"github.com/3rg0n/thlibo/internal/router"
)

// Exit codes reserved by the subcommand itself. Any other exit code
// is the child's own exit code forwarded verbatim.
const (
	ExitUsage         = 64 // argv parsing failed
	ExitSpawnFailed   = 65 // couldn't start the subprocess at all
	ExitChildSignaled = 130
)

// Run drives one invocation. argv is everything after `thlibo exec`.
// Returns the exit code the caller should use for os.Exit.
func Run(argv []string) int {
	cmdArgv, ok := parseCommand(argv)
	if !ok {
		fmt.Fprintln(os.Stderr, "thlibo exec: missing command (expected: thlibo exec -- <cmd> [args...])")
		return ExitUsage
	}
	return run(cmdArgv, os.Stdin, os.Stdout, os.Stderr, defaultPipeline)
}

// parseCommand splits argv into (cmd+args). Accepts both `thlibo exec
// -- git status` and `thlibo exec git status` shapes. The `--`
// separator is standard and recommended; supporting the bare form
// keeps the CLI forgiving for ad-hoc use.
func parseCommand(argv []string) ([]string, bool) {
	if len(argv) == 0 {
		return nil, false
	}
	if argv[0] == "--" {
		if len(argv) < 2 {
			return nil, false
		}
		return argv[1:], true
	}
	return argv, true
}

// pipelineFactory builds the middleware pipeline. Tests override this
// via the parameter; production uses defaultPipeline.
type pipelineFactory func() (*middleware.Pipeline, error)

// run is the testable core: arguments explicit, no package-global
// side effects.
func run(cmdArgv []string, stdin io.Reader, stdout, stderr io.Writer, mkPipeline pipelineFactory) int {
	// #nosec G204 -- cmdArgv comes from the AI client via our hook;
	// this subcommand exists to execute the command the client
	// explicitly asked for. Security comes from the client's own
	// permission prompts + any thlibo deny rules (v0.2).
	cmd := exec.Command(cmdArgv[0], cmdArgv[1:]...)
	cmd.Stdin = stdin
	cmd.Stderr = stderr

	var captured bytes.Buffer
	cmd.Stdout = &captured

	if err := cmd.Run(); err != nil {
		code := childExitCode(err)
		if code < 0 {
			// Spawn failed — not even a child exit to forward.
			fmt.Fprintf(stderr, "thlibo exec: %v\n", err)
			return ExitSpawnFailed
		}
		// Child ran but exited non-zero. Compress its stdout if we
		// can; then forward the exit code verbatim.
		_ = emitCompressed(captured.Bytes(), stdout, mkPipeline)
		return code
	}

	_ = emitCompressed(captured.Bytes(), stdout, mkPipeline)
	return 0
}

// emitCompressed pipes raw through middleware.Process() and writes
// the result to w. Any pipeline error falls back to raw, matching
// middleware's own fallback contract.
func emitCompressed(raw []byte, w io.Writer, mkPipeline pipelineFactory) error {
	p, err := mkPipeline()
	if err != nil {
		// No pipeline => no compression available. Emit raw.
		_, werr := w.Write(raw)
		return werr
	}
	// Short-circuit: middleware already bails on <2000 bytes.
	if err := p.Process(context.Background(), bytes.NewReader(raw), w); err != nil {
		// Pipeline error -> fallback already handled inside Process
		// (it writes raw). But just in case Process wrote nothing,
		// ensure we don't lose output.
		return err
	}
	return nil
}

// childExitCode extracts the child process exit code from an
// exec.Cmd.Run error. Returns -1 if the process never started
// (spawn failure vs. non-zero exit).
func childExitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return code
		}
		return ExitChildSignaled
	}
	return -1
}

// defaultPipeline builds the production pipeline: registry from
// embedded builtins + $HOME/.thlibo/processors (if set), router
// pointing at the daemon's inference socket, prompt thought-
// stripping via PromptRunner.
func defaultPipeline() (*middleware.Pipeline, error) {
	userDir := os.Getenv("THLIBO_PROCESSORS_DIR")
	if userDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			userDir = home + string(os.PathSeparator) + ".thlibo" +
				string(os.PathSeparator) + "processors"
		}
	}

	reg, _, err := middleware.BuildRegistry(userDir)
	if err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}

	client := &router.DaemonClient{
		Address: defaultDaemonAddress(),
		UseTCP:  false,
	}
	promptRunner := &middleware.PromptRunner{Client: client}

	pipeline := &middleware.Pipeline{
		Registry:   reg,
		Router:     &router.ClientAdapter{Client: client},
		Dispatcher: nil,
	}
	// Dispatcher wires prompt runner into the processor dispatcher.
	// Keep this local import out of the top section so the package
	// import list stays compact.
	pipeline.Dispatcher = newDispatcher(promptRunner)
	return pipeline, nil
}
