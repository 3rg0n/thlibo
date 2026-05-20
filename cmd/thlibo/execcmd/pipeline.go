package execcmd

import (
	"github.com/3rg0n/thlibo/internal/inferdcli"
	"github.com/3rg0n/thlibo/internal/processors"
)

// newDispatcher wires a PromptRunner into a processors.Dispatcher so
// prompt processors can reach inferd. This lives separately from
// exec.go to keep the import list there focused on the subcommand
// itself rather than internal wiring.
func newDispatcher(pr processors.PromptRunner) *processors.Dispatcher {
	return &processors.Dispatcher{
		PromptClient: pr,
		// ScriptTimeout defaulting handled inside Dispatcher.Run.
	}
}

// defaultDaemonAddress picks the platform's default inferd inference
// endpoint.
func defaultDaemonAddress() string {
	return inferdcli.DefaultInferenceAddress()
}
