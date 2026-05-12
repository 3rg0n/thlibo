//go:build windows

package daemon

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

var errLockHeld = errors.New("lock held")

// Windows doesn't have flock. LockFileEx with LOCKFILE_EXCLUSIVE_LOCK +
// LOCKFILE_FAIL_IMMEDIATELY is the equivalent: exclusive, non-blocking.
// ERROR_LOCK_VIOLATION or ERROR_IO_PENDING means another process holds it.
//
// LockFileEx locks a byte range and *also blocks other reads on that
// range*. We lock one byte at a very high offset (a sentinel well past
// any realistic file size) so the PID region at offset 0 remains readable
// by operators and tests.
const lockSentinelOffset uint32 = 0xFFFFFFFE

func lockFile(f *os.File) error {
	h := windows.Handle(f.Fd())
	ol := &windows.Overlapped{Offset: lockSentinelOffset}
	const flags = windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY
	err := windows.LockFileEx(h, flags, 0, 1, 0, ol)
	if err == nil {
		return nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return errLockHeld
	}
	return err
}

func unlockFile(f *os.File) error {
	h := windows.Handle(f.Fd())
	ol := &windows.Overlapped{Offset: lockSentinelOffset}
	return windows.UnlockFileEx(h, 0, 1, 0, ol)
}
