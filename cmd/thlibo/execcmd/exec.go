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
	"strings"
	"time"

	"github.com/3rg0n/thlibo/internal/execpolicy"
	"github.com/3rg0n/thlibo/internal/inferdcli"
	"github.com/3rg0n/thlibo/internal/logx"
	"github.com/3rg0n/thlibo/internal/middleware"
	"github.com/3rg0n/thlibo/internal/router"
)

// Exit codes reserved by the subcommand itself. Any other exit code
// is the child's own exit code forwarded verbatim.
const (
	ExitUsage         = 64 // argv parsing failed
	ExitSpawnFailed   = 65 // couldn't start the subprocess at all
	ExitPolicyDenied  = 77 // execpolicy denied the command (EX_NOPERM)
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
	log := logx.New("thlibo-exec", "", 0)
	defer log.Close()

	start := time.Now()
	log.Info("spawn",
		logx.Str("cmd", cmdArgv[0]),
		logx.Int("argc", len(cmdArgv)-1),
	)

	// Belt-and-suspenders check against a user-configurable deny
	// list before we spawn. Claude Code's own permission layer is
	// the primary gate; this is defence-in-depth. See
	// THREAT_MODEL.md finding #22.
	policy, policyErr := execpolicy.Load(execpolicy.DefaultPath())
	if policyErr != nil {
		log.Warn("policy_load_failed", logx.Err(policyErr))
		fmt.Fprintf(stderr, "thlibo exec: policy file invalid: %v\n", policyErr)
		// Fail closed on parse error: the user meant to restrict
		// something and we can't tell what.
		return ExitPolicyDenied
	}
	if policy.Evaluate(cmdArgv[0]) == execpolicy.DecisionDeny {
		log.Warn("policy_deny",
			logx.Str("cmd", cmdArgv[0]),
			logx.Str("reason", "blocked by ~/.thlibo/policy.yaml"),
		)
		fmt.Fprintf(stderr, "thlibo exec: command %q denied by thlibo policy\n", cmdArgv[0])
		return ExitPolicyDenied
	}

	// #nosec G204 -- cmdArgv comes from the AI client via our hook;
	// this subcommand exists to execute the command the client
	// explicitly asked for. Security comes from the client's own
	// permission prompts + any thlibo deny rules (v0.2).
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd := exec.Command(cmdArgv[0], cmdArgv[1:]...)
	cmd.Stdin = stdin
	cmd.Stderr = stderr

	var captured bytes.Buffer
	cmd.Stdout = &captured

	var childCode int
	if err := cmd.Run(); err != nil {
		code := childExitCode(err)
		if code < 0 {
			// Spawn failed — not even a child exit to forward.
			log.Error("spawn_failed",
				logx.Str("cmd", cmdArgv[0]),
				logx.Err(err),
			)
			fmt.Fprintf(stderr, "thlibo exec: %v\n", err)
			return ExitSpawnFailed
		}
		childCode = code
	}

	rawLen := captured.Len()
	outLen, warnings := emitCompressedAndReport(captured.Bytes(), stdout, mkPipeline, log)

	// Single terminal record per invocation — easy to find in logs.
	reduction := 0.0
	if rawLen > 0 {
		reduction = (1.0 - float64(outLen)/float64(rawLen)) * 100
	}
	log.Info("done",
		logx.Str("cmd", cmdArgv[0]),
		logx.Int("raw_bytes", rawLen),
		logx.Int("out_bytes", outLen),
		logx.Any("reduction_pct", roundTo(reduction, 2)),
		logx.Int("child_exit", childCode),
		logx.Dur("duration", time.Since(start)),
		logx.Str("fallbacks", strings.Join(warnings, "; ")),
	)
	return childCode
}

// emitCompressedAndReport pipes raw through middleware.Process() and
// writes the result to w. Returns the number of output bytes and
// any OnWarning records collected (so the caller can log a single
// terminal record summarising the invocation).
func emitCompressedAndReport(raw []byte, w io.Writer, mkPipeline pipelineFactory, log *logx.Logger) (int, []string) {
	var warnings []string

	p, err := mkPipeline()
	if err != nil {
		log.Warn("pipeline_unavailable", logx.Err(err))
		n, _ := w.Write(raw)
		return n, append(warnings, "pipeline_unavailable:"+err.Error())
	}
	p.OnWarning = func(s string) {
		warnings = append(warnings, s)
		log.Debug("middleware_warning", logx.Str("detail", s))
	}

	var buf bytes.Buffer
	if err := p.Process(context.Background(), bytes.NewReader(raw), &buf); err != nil {
		log.Warn("process_error", logx.Err(err))
		warnings = append(warnings, "process_error:"+err.Error())
	}
	n, _ := w.Write(buf.Bytes())
	return n, warnings
}

// roundTo returns v rounded to prec decimal places. Ad-hoc helper
// so log records don't carry 15-digit floats.
func roundTo(v float64, prec int) float64 {
	shift := 1.0
	for i := 0; i < prec; i++ {
		shift *= 10
	}
	// Round half-away-from-zero.
	if v >= 0 {
		return float64(int64(v*shift+0.5)) / shift
	}
	return float64(int64(v*shift-0.5)) / shift
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

	client := &inferdcli.Client{
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
