//go:build windows

package install

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// openWindowsPipe opens a Windows named pipe to the inferd admin
// socket. Same retry-on-busy posture inferdcli uses; kept inline so
// the install package doesn't import inferdcli (which would create
// a cycle: inferdcli's wire is also used to *talk* to inferd in the
// rest of thlibo).
func openWindowsPipe(ctx context.Context, addr string) (net.Conn, error) {
	deadline := time.Now().Add(2 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	for {
		f, err := os.OpenFile(addr, os.O_RDWR, 0) // #nosec G304 -- addr is the inferd admin pipe path from defaultInferdAdminAddr, a compile-time platform constant; not user input
		if err == nil {
			return &winPipeConn{File: f}, nil
		}
		if isPipeBusyOrMissing(err) && time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(20 * time.Millisecond):
			}
			continue
		}
		return nil, fmt.Errorf("open pipe %s: %w", addr, err)
	}
}

func isPipeBusyOrMissing(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "all pipe instances are busy") ||
		strings.Contains(s, "the system cannot find the file") ||
		strings.Contains(s, "access is denied")
}

type winPipeConn struct{ *os.File }

func (p *winPipeConn) LocalAddr() net.Addr  { return winPipeAddr(p.Name()) }
func (p *winPipeConn) RemoteAddr() net.Addr { return winPipeAddr(p.Name()) }

func (p *winPipeConn) SetDeadline(t time.Time) error      { return p.File.SetDeadline(t) }
func (p *winPipeConn) SetReadDeadline(t time.Time) error  { return p.File.SetReadDeadline(t) }
func (p *winPipeConn) SetWriteDeadline(t time.Time) error { return p.File.SetWriteDeadline(t) }

type winPipeAddr string

func (winPipeAddr) Network() string  { return "pipe" }
func (a winPipeAddr) String() string { return string(a) }
