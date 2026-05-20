//go:build unix

package install

import (
	"context"
	"errors"
	"net"
)

// openWindowsPipe is unreachable on Unix but the orchestrator file
// references it through a runtime.GOOS switch. Provide a stub that
// errors out so the build compiles without conditional imports.
func openWindowsPipe(_ context.Context, _ string) (net.Conn, error) {
	return nil, errors.New("inferd: openWindowsPipe called on non-Windows host")
}
