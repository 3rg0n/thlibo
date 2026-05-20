//go:build windows

package inferdcli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// dialNative opens a Windows named pipe. Same retry-on-busy posture
// the inferd client's own DialPipe uses, kept inline so this package
// stays a thin wrapper without depending on inferd's internal pipe
// shim.
func dialNative(ctx context.Context, addr string) (net.Conn, error) {
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	for {
		f, err := os.OpenFile(addr, os.O_RDWR, 0) // #nosec G304 -- addr is an inferd pipe path from DefaultInferenceAddress, a compile-time platform constant; not user input
		if err == nil {
			return &pipeConn{File: f}, nil
		}
		if isPipeBusy(err) && time.Now().Before(deadline) {
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

func isPipeBusy(err error) bool {
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

type pipeConn struct{ *os.File }

func (p *pipeConn) LocalAddr() net.Addr  { return pipeAddr(p.Name()) }
func (p *pipeConn) RemoteAddr() net.Addr { return pipeAddr(p.Name()) }

func (p *pipeConn) SetDeadline(t time.Time) error      { return p.File.SetDeadline(t) }
func (p *pipeConn) SetReadDeadline(t time.Time) error  { return p.File.SetReadDeadline(t) }
func (p *pipeConn) SetWriteDeadline(t time.Time) error { return p.File.SetWriteDeadline(t) }

type pipeAddr string

func (pipeAddr) Network() string  { return "pipe" }
func (a pipeAddr) String() string { return string(a) }
