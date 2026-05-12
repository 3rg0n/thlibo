// Package rewritecmd implements `thlibo rewrite`.
//
// Exit-code protocol (matches RTK's convention so hook scripts are
// portable between the two products):
//
//	0 + stdout  Rewrite found. stdout = new command.
//	1           No wrapper for argv[0]. Pass through unchanged.
//	2           Deny rule matched (reserved for v0.2).
//	3 + stdout  Ask rule matched (reserved for v0.2).
//	>=64        Internal error. Hook should pass through silently.
//
// The subcommand joins all argv-after-rewrite into a single shell
// command so callers don't have to worry about quoting. The hook
// always invokes us as `thlibo rewrite "$CMD"` (one argument, already
// reassembled by jq).
package rewritecmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/3rg0n/thlibo/internal/middleware"
	"github.com/3rg0n/thlibo/internal/shellcmd"
)

// Exit codes. Kept as named constants so tests and hook scripts agree.
const (
	ExitRewrite     = 0
	ExitPassthrough = 1
	ExitDeny        = 2  // reserved for v0.2
	ExitAsk         = 3  // reserved for v0.2
	ExitInternal    = 64 // internal error; hook must treat as passthrough
)

// Run executes the rewrite logic and returns the exit code. argv is
// what follows `thlibo rewrite` on the command line.
func Run(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "thlibo rewrite: missing command")
		return ExitInternal
	}
	cmd := strings.Join(argv, " ")

	argv0, ok := shellcmd.Argv0(cmd)
	if !ok {
		// Compound or empty — leave to Claude Code.
		return ExitPassthrough
	}
	base := shellcmd.Basename(argv0)

	// Special case: if the command already starts with `thlibo ...`
	// we're looking at our own rewrite output — avoid a rewrite loop.
	if base == "thlibo" {
		return ExitPassthrough
	}

	userDir := os.Getenv("THLIBO_PROCESSORS_DIR") // tests override default
	reg, _, err := middleware.BuildRegistry(userDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "thlibo rewrite: registry: %v\n", err)
		return ExitInternal
	}

	d := reg.MatchCommand(base)
	if d == nil {
		return ExitPassthrough
	}

	// Wrap the command. We emit the absolute path to our own
	// executable rather than a bare `thlibo` so the rewritten
	// command runs successfully in Claude Code's Bash tool, which
	// does NOT inherit the parent shell's PATH modifications at
	// tool-execution time. Confirmed by a live `claude -p` smoke
	// test on Windows.
	//
	// Path normalisation: on Windows the os.Executable() returns a
	// backslash path. Bash -c will eat backslashes as escapes if
	// we don't quote-or-convert, so we convert to forward slashes.
	// (Same fix as the claudecode adapter applies to the hook path.)
	self := selfPath()
	fmt.Println(self + " exec -- " + cmd)
	return ExitRewrite
}

// selfPath returns the path to the currently-running thlibo binary
// in a form the hook's bash can exec. Errors fall back to the bare
// name `thlibo` so the old behaviour still works if PATH is set.
func selfPath() string {
	p, err := os.Executable()
	if err != nil {
		return "thlibo"
	}
	// Bash-friendly: forward slashes, quoted if the path contains a
	// space. Windows program-files installs typically have spaces.
	p = forwardSlashes(p)
	if needsShellQuoting(p) {
		return `"` + p + `"`
	}
	return p
}

func forwardSlashes(p string) string {
	out := make([]byte, len(p))
	for i := 0; i < len(p); i++ {
		if p[i] == '\\' {
			out[i] = '/'
		} else {
			out[i] = p[i]
		}
	}
	return string(out)
}

func needsShellQuoting(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '"' || c == '\'' || c == '$' || c == '`' {
			return true
		}
	}
	return false
}
