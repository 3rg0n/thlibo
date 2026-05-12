package daemon

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

// ErrAlreadyLocked is returned by AcquireLock when another thlibod is
// already holding the lock for this machine.
var ErrAlreadyLocked = errors.New("daemon: lock already held by another thlibod")

// Lock is a single-instance lock backed by a file plus an OS-level
// exclusive advisory lock (flock on Unix, LockFileEx on Windows). The file
// is left on disk when released, with its contents cleared; this lets
// operators see "was a daemon ever here?" without needing to grep logs.
type Lock struct {
	path string
	f    *os.File
}

// AcquireLock creates or opens path and takes an exclusive OS-level lock
// on it. If another process already holds the lock, returns
// ErrAlreadyLocked without disturbing that process. The current PID is
// written into the file so humans can identify the holder.
//
// The caller must call Release (typically via defer) to drop the lock and
// clean up the file contents.
func AcquireLock(path string) (*Lock, error) {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return nil, fmt.Errorf("daemon: create lock dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("daemon: open lock file: %w", err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		if errors.Is(err, errLockHeld) {
			return nil, ErrAlreadyLocked
		}
		return nil, fmt.Errorf("daemon: acquire lock: %w", err)
	}

	// Truncate and write our PID so an operator can `cat` the file.
	if err := f.Truncate(0); err != nil {
		_ = unlockFile(f)
		_ = f.Close()
		return nil, fmt.Errorf("daemon: truncate lock: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		_ = unlockFile(f)
		_ = f.Close()
		return nil, fmt.Errorf("daemon: seek lock: %w", err)
	}
	if _, err := f.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		_ = unlockFile(f)
		_ = f.Close()
		return nil, fmt.Errorf("daemon: write pid: %w", err)
	}
	return &Lock{path: path, f: f}, nil
}

// Release drops the OS-level lock and closes the file. Safe to call once.
// Subsequent calls are no-ops. The lock file is left on disk (with cleared
// contents); the next daemon start will truncate and rewrite it.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	// Best-effort: clear contents so a stale file can't confuse operators.
	_ = l.f.Truncate(0)
	unlockErr := unlockFile(l.f)
	closeErr := l.f.Close()
	l.f = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

// Path returns the lock file path.
func (l *Lock) Path() string { return l.path }

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}
