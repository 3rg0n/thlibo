package execcmd

import (
	"github.com/3rg0n/thlibo/internal/ipc"
	"github.com/3rg0n/thlibo/internal/processors"
)

// newDispatcher wires a PromptRunner into a processors.Dispatcher so
// prompt processors can reach the daemon. This lives separately from
// exec.go to keep the import list there focused on the subcommand
// itself rather than internal wiring.
func newDispatcher(pr processors.PromptRunner) *processors.Dispatcher {
	return &processors.Dispatcher{
		PromptClient: pr,
		// ScriptTimeout defaulting handled inside Dispatcher.Run.
	}
}

// defaultDaemonAddress picks the platform's default inference
// endpoint. Daemon lifecycle (Phase 1) uses the same defaults.
func defaultDaemonAddress() string {
	return ipc.DefaultInferenceAddress()
}
