package update

import (
	"testing"
)

// clearAutoSignals sets every auto-headless signal to "" so a test
// can assert the exact one it cares about. Without this, an inherited
// CLAUDECODE=1 (running under Claude Code) or CI=true (running in
// GitHub Actions) would mask the test.
func clearAutoSignals(t *testing.T) {
	t.Helper()
	t.Setenv("THLIBO_HEADLESS", "")
	for _, k := range headlessAutoSignals {
		t.Setenv(k, "")
	}
}

func TestIsHeadlessEnvOverride(t *testing.T) {
	t.Run("THLIBO_HEADLESS=1 forces headless", func(t *testing.T) {
		clearAutoSignals(t)
		t.Setenv("THLIBO_HEADLESS", "1")
		if !IsHeadless() {
			t.Error("expected headless=true when THLIBO_HEADLESS=1")
		}
	})

	t.Run("THLIBO_HEADLESS=0 beats every auto-signal", func(t *testing.T) {
		clearAutoSignals(t)
		t.Setenv("THLIBO_HEADLESS", "0")
		// Set every auto-signal — explicit override must still win.
		for _, k := range headlessAutoSignals {
			t.Setenv(k, "1")
		}
		if IsHeadless() {
			t.Error("expected headless=false when THLIBO_HEADLESS=0, even with every auto-signal set")
		}
	})

	t.Run("CI=true without override is headless", func(t *testing.T) {
		clearAutoSignals(t)
		t.Setenv("CI", "true")
		if !IsHeadless() {
			t.Error("expected headless=true when CI=true")
		}
	})
}

// TestIsHeadlessAutoSignals covers every entry in headlessAutoSignals
// individually so a regression that drops a signal is caught.
func TestIsHeadlessAutoSignals(t *testing.T) {
	for _, key := range headlessAutoSignals {
		key := key
		t.Run(key+" set marks headless", func(t *testing.T) {
			clearAutoSignals(t)
			t.Setenv(key, "1")
			if !IsHeadless() {
				t.Errorf("expected headless=true when %s is set", key)
			}
		})
	}
}
