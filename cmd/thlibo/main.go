// Command thlibo is the user-facing CLI. It has three subcommands in
// v0.1:
//
//	thlibo rewrite <command>   — given a shell command, decide whether
//	                             to wrap it for compression. Called by
//	                             the Claude Code PreToolUse hook.
//	thlibo exec -- <command>   — run <command>, pipe stdout through
//	                             middleware.Process(), emit compressed
//	                             stdout with stderr + exit code preserved.
//	thlibo install             — (Phase 6) lay down hook scripts and
//	                             wire settings.json.
//
// The daemon (thlibod) is a separate binary.
package main

import (
	"fmt"
	"os"

	"github.com/3rg0n/thlibo/cmd/thlibo/execcmd"
	"github.com/3rg0n/thlibo/cmd/thlibo/rewritecmd"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "rewrite":
		os.Exit(rewritecmd.Run(os.Args[2:]))
	case "exec":
		os.Exit(execcmd.Run(os.Args[2:]))
	case "install":
		fmt.Fprintln(os.Stderr, "thlibo install: not yet implemented (Phase 6)")
		os.Exit(1)
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "thlibo: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `thlibo — AI tool output compressor

Usage:
  thlibo rewrite <command>   Decide whether to wrap a shell command.
                             Exit 0 + rewritten command on stdout = wrap.
                             Exit 1 = passthrough.
  thlibo exec -- <command>   Run <command>, compress stdout, return.
  thlibo install             Set up Claude Code hook + service (Phase 6).
  thlibo help                Show this message.
`)
}
