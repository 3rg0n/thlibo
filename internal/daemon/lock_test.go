package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A2: single-instance lock. Second attempt must fail fast with
// ErrAlreadyLocked without disturbing the first.
func TestLockSingleInstance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "thlibod.lock")

	first, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	t.Cleanup(func() { _ = first.Release() })

	second, err := AcquireLock(path)
	if !errors.Is(err, ErrAlreadyLocked) {
		t.Fatalf("second AcquireLock: err=%v (want ErrAlreadyLocked), lock=%v", err, second)
	}
	if second != nil {
		t.Error("second AcquireLock returned a non-nil lock despite error")
	}

	// First lock must still be held (not disturbed by the second attempt).
	third, err := AcquireLock(path)
	if !errors.Is(err, ErrAlreadyLocked) {
		t.Fatalf("third AcquireLock should also fail while first is held: %v", err)
	}
	_ = third
}

func TestLockReleaseAllowsReacquire(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "thlibod.lock")

	first, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	second, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	t.Cleanup(func() { _ = second.Release() })
}

func TestLockWritesPid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "thlibod.lock")

	lock, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	t.Cleanup(func() { _ = lock.Release() })

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	content := strings.TrimSpace(string(b))
	if content == "" {
		t.Error("lock file should contain the holder's PID")
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "thlibod.lock")

	lock, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Errorf("second Release should be a no-op, got %v", err)
	}
}

// Lock.Path() is a debugging aid used by lifecycle code.
func TestLockPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "thlibod.lock")
	lock, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	if lock.Path() != path {
		t.Errorf("Path() = %q, want %q", lock.Path(), path)
	}
}
