package update

import (
	"testing"
)

func TestIsHeadlessEnvOverride(t *testing.T) {
	t.Run("THLIBO_HEADLESS=1 forces headless", func(t *testing.T) {
		t.Setenv("THLIBO_HEADLESS", "1")
		t.Setenv("CI", "")
		if !IsHeadless() {
			t.Error("expected headless=true when THLIBO_HEADLESS=1")
		}
	})

	t.Run("THLIBO_HEADLESS=0 beats CI=true", func(t *testing.T) {
		t.Setenv("THLIBO_HEADLESS", "0")
		t.Setenv("CI", "true")
		if IsHeadless() {
			t.Error("expected headless=false when THLIBO_HEADLESS=0, even with CI=true")
		}
	})

	t.Run("CI=true without override is headless", func(t *testing.T) {
		t.Setenv("THLIBO_HEADLESS", "")
		t.Setenv("CI", "true")
		if !IsHeadless() {
			t.Error("expected headless=true when CI=true")
		}
	})
}
