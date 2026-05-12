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

	// Wrap the command. Quoting: the hook passes us the original
	// command as one shell string; we prepend `thlibo exec --` and
	// emit the whole thing. The quoting responsibility is whoever
	// reassembles it (jq / the hook script), not us.
	fmt.Println("thlibo exec -- " + cmd)
	return ExitRewrite
}
