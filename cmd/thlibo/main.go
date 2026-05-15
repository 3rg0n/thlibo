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
	"context"
	"fmt"
	"os"

	"github.com/3rg0n/thlibo/cmd/thlibo/casecmd"
	"github.com/3rg0n/thlibo/cmd/thlibo/compresscmd"
	"github.com/3rg0n/thlibo/cmd/thlibo/execcmd"
	"github.com/3rg0n/thlibo/cmd/thlibo/installcmd"
	"github.com/3rg0n/thlibo/cmd/thlibo/pullcmd"
	"github.com/3rg0n/thlibo/cmd/thlibo/rewritecmd"
	"github.com/3rg0n/thlibo/cmd/thlibo/shorthandcmd"
	"github.com/3rg0n/thlibo/cmd/thlibo/uninstallcmd"
	"github.com/3rg0n/thlibo/internal/logx"
	"github.com/3rg0n/thlibo/internal/update"
	"github.com/3rg0n/thlibo/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	// Background update check. Fires on every CLI invocation but:
	//   - cooldown-gated (default 24h) via ~/.thlibo/state/update-check.json
	//   - runs in a detached goroutine, never blocks the subcommand
	//   - silent on network failure (logged at debug level only)
	//   - suppressed by THLIBO_NO_UPDATE=1, THLIBO_UPDATE_INTERVAL=0,
	//     and for untagged dev builds
	//
	// "version" subcommand is hit before the check fires (user
	// asking "what version am I on" shouldn't spawn a network call).
	if os.Args[1] != "version" && os.Args[1] != "-v" && os.Args[1] != "--version" {
		r := &update.Runner{
			Current: version.Tag,
			Out:     os.Stderr,
			Logger:  logx.New("thlibo-update", "", 0),
		}
		_ = r.Run(context.Background())
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
	case "case":
		os.Exit(casecmd.Run(os.Args[2:]))
	case "shorthand":
		os.Exit(shorthandcmd.Run(os.Args[2:]))
	case "shorthand-hook":
		// Internal subcommand invoked by the Write/Edit PreToolUse
		// hook scripts. Reads the tool envelope from stdin, decides
		// whether to rewrite, emits hookSpecificOutput JSON. Always
		// exits 0 — never breaks Claude Code on failure.
		os.Exit(shorthandcmd.RunWriteHook(os.Args[2:]))
	case "version", "-v", "--version":
		fmt.Println(version.Tag)
		os.Exit(0)
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
  thlibo case <file>         Build a compressed case directory under
                             ~/.thlibo/cases/ for a large log file.
                             Invoked by the Read PreToolUse hook and
                             by the /caselog skill.
  thlibo shorthand <file>    Compress LLM-facing prose (SKILL.md,
                             CLAUDE.md, agents.md, system prompts)
                             into token-efficient shorthand. Eval
                             checklist gates: NEVER/MUST/DO NOT,
                             code fences, frontmatter, URLs, paths,
                             versions, thresholds preserved verbatim.
                             Use --in-place to rewrite the file
                             (.orig backup), --validate to gate CI.
  thlibo version             Print the build tag and exit.
  thlibo help                Show this message.

Environment:
  THLIBO_DISABLED=1          Per-session kill switch; hooks pass
                             through unchanged when set.
  THLIBO_NO_UPDATE=1         Disable the background update check.
  THLIBO_UPDATE_INTERVAL=0   Alternate way to disable the check; or
                             a Go duration like "168h" to change the
                             cooldown (default: 24h).
`)
}
