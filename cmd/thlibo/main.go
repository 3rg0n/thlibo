// Command thlibo is the user-facing CLI. In v0.1 it has four
// subcommands:
//
//	thlibo rewrite <command>   — given a shell command, decide whether
//	                             to wrap it for compression. Called by
//	                             the Claude Code PreToolUse hook.
//	thlibo exec -- <command>   — run <command>, pipe stdout through
//	                             middleware.Process(), emit compressed
//	                             stdout with stderr + exit code preserved.
//	thlibo install             — lay down hook scripts, mirror
//	                             processors, merge settings.json,
//	                             register the daemon for autostart.
//	thlibo pull [name]         — download a GGUF model from its
//	                             pinned URL, verify SHA-256.
//
// The daemon (thlibod) is a separate binary.
package main

import (
	"fmt"
	"os"

	"github.com/3rg0n/thlibo/cmd/thlibo/compresscmd"
	"github.com/3rg0n/thlibo/cmd/thlibo/execcmd"
	"github.com/3rg0n/thlibo/cmd/thlibo/installcmd"
	"github.com/3rg0n/thlibo/cmd/thlibo/pullcmd"
	"github.com/3rg0n/thlibo/cmd/thlibo/rewritecmd"
	"github.com/3rg0n/thlibo/cmd/thlibo/uninstallcmd"
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
	case "compress":
		os.Exit(compresscmd.Run(os.Args[2:]))
	case "install":
		os.Exit(installcmd.Run(os.Args[2:]))
	case "uninstall":
		os.Exit(uninstallcmd.Run(os.Args[2:]))
	case "pull":
		os.Exit(pullcmd.Run(os.Args[2:]))
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
  thlibo compress            Read stdin, compress, write stdout (Codex hook, pipes).
  thlibo install             Mirror processors, wire the Claude Code
                             hook, register the daemon for autostart.
  thlibo uninstall           Reverse install: remove hook entries +
                             scripts, unregister autostart. Pass
                             --purge to also delete ~/.thlibo.
  thlibo pull [name]         Download a GGUF model (default: gemma-4-e4b).
  thlibo help                Show this message.

Environment:
  THLIBO_DISABLED=1          Per-session kill switch; hooks pass
                             through unchanged when set.
`)
}
