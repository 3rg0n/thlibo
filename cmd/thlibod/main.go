// Command thlibod is the thlibo inference daemon. It loads the
// Gemma 4 E4B model via llamafile once, stays warm, and serves
// inference requests over IPC. It has no knowledge of processors,
// routing, or AI clients.
//
// v0.1 run modes:
//
//	thlibod                  Run in the foreground (console/dev mode)
//	thlibod --help           Usage
//	thlibod install          (Windows) install as a service — Phase 6
//	thlibod uninstall        (Windows) remove the service — Phase 6
//	thlibod run              Internal: Windows SCM entry point
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/3rg0n/thlibo/internal/daemon"
	"github.com/3rg0n/thlibo/internal/ipc"
	"github.com/3rg0n/thlibo/internal/logx"
)

// exit codes — kept small and named so operator scripts can key on them.
const (
	exitOK          = 0
	exitUsage       = 2
	exitConfigError = 3
	exitStartError  = 4
)

type flags struct {
	enginePath    string
	engineArgs    string
	ctxSize       int
	stopTokens    string
	lockPath      string
	inferenceAddr string
	adminAddr     string
	group         string
	useTCP        bool
	readyTimeout  time.Duration
	stopTimeout   time.Duration
	verbose       bool
}

// Gemma 4 inference defaults. Context window and stop token come
// from the official model card + capability docs. We pass them to
// llamafile via `-c <n>` for the context cap and repeated
// `--stop <token>` flags for the turn-boundary tokens so generation
// doesn't ramble into the next chat-template stanza. Overridable at
// runtime via -ctx and -stop. See task #13 / the release notes for
// why these specific values.
//
// Reference: https://ai.google.dev/gemma/docs/core/model_card_4 and
// the chat template at
// https://github.com/google/gemma/blob/main/prompt.py
const (
	defaultCtxSize = 32768
	// #nosec G101 -- not a credential; these are Gemma 4's native
	// chat-template turn-boundary tokens, passed to llamafile via
	// --stop so generation cuts cleanly. Public values from
	// https://ai.google.dev/gemma/docs/core/model_card_4.
	defaultStopTokens    = "<turn|>,<end_of_turn>"
	gemmaContextMaxBytes = 128 * 1024
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "help", "-h", "--help":
			printUsage()
			os.Exit(exitOK)
		}
	}
	code := runConsole(os.Args[1:])
	os.Exit(code)
}

// runConsole is the v0.1 entry point: no service-control plumbing
// yet. Returns an exit code so it's straightforward to unit-test
// later.
func runConsole(argv []string) int {
	// Boot-path logger so early failures land in the NDJSON audit
	// trail, not just stderr. Reuses the component name the daemon
	// uses once it's up. See THREAT_MODEL.md finding #10.
	bootLog := logx.New("thlibod", "", 0)

	f, err := parseFlags(argv)
	if err != nil {
		bootLog.Error("flag_parse_failed", logx.Err(err))
		fmt.Fprintln(os.Stderr, "thlibod:", err)
		return exitConfigError
	}

	cfg, err := buildConfig(f)
	if err != nil {
		bootLog.Error("config_build_failed", logx.Err(err))
		fmt.Fprintln(os.Stderr, "thlibod:", err)
		return exitConfigError
	}

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	bootLog.Info("starting",
		logx.Str("engine", f.enginePath),
		logx.Str("lock", cfg.LockPath),
		logx.Str("infer", cfg.InferenceEndpoint.Address),
		logx.Str("admin", cfg.AdminEndpoint.Address))
	if f.verbose {
		log.Printf("starting: engine=%s lock=%s infer=%s admin=%s",
			f.enginePath, cfg.LockPath, cfg.InferenceEndpoint.Address, cfg.AdminEndpoint.Address)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d, err := daemon.Start(ctx, cfg)
	if err != nil {
		bootLog.Error("daemon_start_failed", logx.Err(err))
		fmt.Fprintln(os.Stderr, "thlibod:", err)
		return exitStartError
	}
	bootLog.Info("ready",
		logx.Str("infer", d.InferenceAddr().String()),
		logx.Str("admin", d.AdminAddr().String()))
	log.Printf("ready: inference=%s admin=%s", d.InferenceAddr(), d.AdminAddr())

	// Wait for SIGINT/SIGTERM (and Ctrl-Break on Windows).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	sig := <-sigCh
	bootLog.Info("signal_received", logx.Str("signal", sig.String()))
	log.Printf("signal %v: shutting down", sig)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), f.stopTimeout)
	defer stopCancel()
	if err := d.Stop(stopCtx); err != nil {
		bootLog.Error("daemon_stop_failed", logx.Err(err))
		fmt.Fprintln(os.Stderr, "thlibod stop:", err)
		return exitStartError
	}
	bootLog.Info("stopped_cleanly")
	log.Printf("stopped cleanly")
	return exitOK
}

func parseFlags(argv []string) (*flags, error) {
	fs := flag.NewFlagSet("thlibod", flag.ContinueOnError)
	f := &flags{}

	fs.StringVar(&f.enginePath, "engine", defaultEnginePath(), "path to the llamafile-style engine binary")
	fs.StringVar(&f.engineArgs, "engine-args", "", "extra arguments to pass to the engine (space-separated, appended after -ctx/-stop)")
	fs.IntVar(&f.ctxSize, "ctx", defaultCtxSize, "context window size in tokens (passed to engine as -c <n>)")
	fs.StringVar(&f.stopTokens, "stop", defaultStopTokens, "comma-separated stop tokens (each passed as --stop <t>)")
	fs.StringVar(&f.lockPath, "lock", "", "lock file path (default: platform-specific)")
	fs.StringVar(&f.inferenceAddr, "infer-addr", "", "inference endpoint (default: platform-specific)")
	fs.StringVar(&f.adminAddr, "admin-addr", "", "admin endpoint (default: platform-specific)")
	fs.StringVar(&f.group, "group", "thlibo-users", "Unix group that owns the inference socket (Unix only)")
	fs.BoolVar(&f.useTCP, "tcp", false, "bind on 127.0.0.1:47320 instead of the native Unix socket / named pipe")
	fs.DurationVar(&f.readyTimeout, "ready-timeout", 30*time.Second, "how long to wait for the engine to become ready")
	fs.DurationVar(&f.stopTimeout, "stop-timeout", 5*time.Second, "how long to wait for a clean shutdown")
	fs.BoolVar(&f.verbose, "v", false, "verbose logging")

	fs.Usage = printUsage
	if err := fs.Parse(argv); err != nil {
		return nil, err
	}

	if f.lockPath == "" {
		f.lockPath = defaultLockPath()
	}
	if f.inferenceAddr == "" {
		f.inferenceAddr = ipc.DefaultInferenceAddress()
	}
	if f.adminAddr == "" {
		f.adminAddr = ipc.DefaultAdminAddress()
	}
	if f.useTCP {
		f.inferenceAddr = ipc.DefaultTCPFallbackAddress
		// Admin stays on its normal transport so permissions are
		// consistent; operators who need a TCP admin socket can set
		// it explicitly.
	}

	if f.enginePath == "" {
		return nil, fmt.Errorf("engine path is required (set -engine or install thlibo-engine on PATH)")
	}
	return f, nil
}

func buildConfig(f *flags) (daemon.Config, error) {
	// Build the engine argv. Order: context-window first, then each
	// stop token, then any operator-supplied extra args. The
	// operator wins on trailing duplicates because llamafile honours
	// last-value-wins.
	var engineArgs []string
	if f.ctxSize > 0 {
		engineArgs = append(engineArgs, "-c", fmt.Sprintf("%d", f.ctxSize))
	}
	for _, tok := range splitCommaList(f.stopTokens) {
		if tok == "" {
			continue
		}
		engineArgs = append(engineArgs, "--stop", tok)
	}
	engineArgs = append(engineArgs, splitFields(f.engineArgs)...)

	infer := ipc.EndpointConfig{
		Kind:    ipc.EndpointInference,
		Address: f.inferenceAddr,
		Group:   f.group,
		UseTCP:  f.useTCP || looksLikeTCP(f.inferenceAddr),
	}
	adminEP := ipc.EndpointConfig{
		Kind:    ipc.EndpointAdmin,
		Address: f.adminAddr,
		UseTCP:  looksLikeTCP(f.adminAddr),
	}

	return daemon.Config{
		LockPath: f.lockPath,
		EngineCmd: func() *exec.Cmd {
			// #nosec G204,G702 — enginePath is a daemon config value
			// set by the operator via a flag or installer default,
			// never user-controllable at runtime. This is the whole
			// point of the daemon: it spawns the engine it was told
			// to spawn.
			// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
			return exec.Command(f.enginePath, engineArgs...)
		},
		InferenceEndpoint: infer,
		AdminEndpoint:     adminEP,
		ReadyPollTimeout:  f.readyTimeout,
		StopTimeout:       f.stopTimeout,
		Logger:            logx.New("thlibod", "", 0),
	}, nil
}

func splitFields(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// splitCommaList splits a comma-separated token list, trimming
// whitespace around each entry. Used for the -stop flag so the
// operator can pass `--stop "<turn|>,<end_of_turn>"` instead of
// repeating the flag. Empty entries are dropped.
func splitCommaList(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			t := trimSpace(cur)
			if t != "" {
				out = append(out, t)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	t := trimSpace(cur)
	if t != "" {
		out = append(out, t)
	}
	return out
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	return s[i:j]
}

func looksLikeTCP(addr string) bool {
	// Crude: a colon that isn't preceded by a backslash (Windows
	// named-pipe style) means host:port.
	for i := 0; i < len(addr); i++ {
		if addr[i] == ':' {
			if i > 0 && addr[i-1] == '\\' {
				return false
			}
			// Windows drive-letter C:/... is not TCP either.
			if i == 1 {
				return false
			}
			return true
		}
	}
	return false
}

// defaultLockPath returns the v0.1 lock file path.
func defaultLockPath() string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.TempDir(), "thlibod.lock")
	case "darwin":
		// /var/run is root-owned; $TMPDIR is per-user and always writable.
		return filepath.Join(os.TempDir(), "thlibo", "thlibod.lock")
	default:
		return "/run/thlibo/thlibod.lock"
	}
}

// defaultEnginePath returns the OS-specific default location where
// the engine binary lives after `thlibo install`. Since no install
// has happened yet, the default is relative to the running thlibod
// binary: <exe-dir>/thlibo-engine(.exe).
func defaultEnginePath() string {
	self, err := os.Executable()
	if err != nil {
		return "thlibo-engine"
	}
	dir := filepath.Dir(self)
	name := "thlibo-engine"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}

func printUsage() {
	fmt.Fprint(os.Stderr, `thlibod — thlibo inference daemon

Usage:
  thlibod [flags]                Run in the foreground.
  thlibod help                   Show this message.

Flags:
  -engine <path>                 Engine binary path.
                                 Default: <thlibod-dir>/thlibo-engine[.exe].
  -engine-args "<args>"          Extra args appended after -ctx/-stop.
  -ctx N                         Context window tokens (default 32768,
                                 passed to engine as -c N).
  -stop "<t1>,<t2>,..."          Comma-separated stop tokens, each
                                 passed as --stop <t> (default:
                                 "<turn|>,<end_of_turn>").
  -lock <path>                   Lock file. Default is platform-specific.
  -infer-addr <addr>             Inference endpoint. Default is platform-specific.
  -admin-addr <addr>             Admin endpoint. Default is platform-specific.
  -group <name>                  Unix group for the inference socket (default: thlibo-users).
  -tcp                           Use 127.0.0.1:47320 TCP fallback instead of native IPC.
  -ready-timeout <duration>      How long to wait for engine ready (default: 30s).
  -stop-timeout <duration>       How long to wait for clean shutdown (default: 5s).
  -v                             Verbose logging.
`)
}
