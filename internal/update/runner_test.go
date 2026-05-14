package update

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Helper: isolate each test's env + home so the runner's filesystem
// state doesn't escape the test's TempDir.
func isolateEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)         // Unix
	t.Setenv("USERPROFILE", home)  // Windows
	t.Setenv(envNoUpdate, "")
	t.Setenv(envInterval, "")
	t.Setenv(envAPI, "")
	t.Setenv("NO_COLOR", "1") // plain output so banner assertions are stable
	return home
}

func TestRunnerBannerOnFreshUpgrade(t *testing.T) {
	home := isolateEnv(t)

	var out bytes.Buffer
	r := &Runner{
		Current: "v0.2.0",
		Fetcher: &stubFetcher{rel: &release{TagName: "v0.3.0", HTMLURL: "https://x"}},
		Out:     &out,
	}
	<-r.Run(context.Background())

	if !strings.Contains(out.String(), "update available: v0.2.0 -> v0.3.0") {
		t.Fatalf("banner missing / malformed:\n%s", out.String())
	}
	// State persisted with NotifiedTag so we don't re-banner.
	statePath := filepath.Join(home, ".thlibo", "state", "update-check.json")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	var s state
	_ = json.Unmarshal(raw, &s)
	if s.NotifiedTag != "v0.3.0" {
		t.Errorf("NotifiedTag = %q, want v0.3.0", s.NotifiedTag)
	}
}

func TestRunnerCooldownSuppressesRecheck(t *testing.T) {
	home := isolateEnv(t)

	// Pre-seed state: checked 1h ago, no upgrade.
	statePath := filepath.Join(home, ".thlibo", "state", "update-check.json")
	_ = os.MkdirAll(filepath.Dir(statePath), 0o750)
	seed := state{
		CheckedAt:   time.Now().Add(-1 * time.Hour).UTC(),
		LatestTag:   "v0.2.0",
		NotifiedTag: "v0.2.0",
	}
	buf, _ := json.Marshal(seed)
	_ = os.WriteFile(statePath, buf, 0o600)

	called := false
	stub := &stubFetcher{rel: &release{TagName: "v0.3.0"}}

	r := &Runner{
		Current: "v0.2.0",
		Fetcher: fetcherFn(func(ctx context.Context) (*release, error) {
			called = true
			return stub.Fetch(ctx)
		}),
	}
	<-r.Run(context.Background())

	if called {
		t.Error("fetcher called within cooldown window; should have been suppressed")
	}
}

func TestRunnerRebannersUnacknowledgedUpgradeWithinCooldown(t *testing.T) {
	home := isolateEnv(t)

	// State says: checked recently, latest=v0.3.0, but never
	// notified (NotifiedTag empty). Runner should print a banner
	// without refetching.
	statePath := filepath.Join(home, ".thlibo", "state", "update-check.json")
	_ = os.MkdirAll(filepath.Dir(statePath), 0o750)
	seed := state{
		CheckedAt: time.Now().Add(-1 * time.Hour).UTC(),
		LatestTag: "v0.3.0",
		SeenURL:   "https://example",
	}
	buf, _ := json.Marshal(seed)
	_ = os.WriteFile(statePath, buf, 0o600)

	called := false
	var out bytes.Buffer
	r := &Runner{
		Current: "v0.2.0",
		Out:     &out,
		Fetcher: fetcherFn(func(ctx context.Context) (*release, error) {
			called = true
			return &release{TagName: "v0.3.0"}, nil
		}),
	}
	<-r.Run(context.Background())

	if called {
		t.Error("fetcher should NOT be called on re-banner path")
	}
	if !strings.Contains(out.String(), "update available: v0.2.0 -> v0.3.0") {
		t.Errorf("re-banner missing:\n%s", out.String())
	}
}

func TestRunnerKillSwitchEnv(t *testing.T) {
	isolateEnv(t)
	t.Setenv(envNoUpdate, "1")

	called := false
	r := &Runner{
		Current: "v0.2.0",
		Fetcher: fetcherFn(func(ctx context.Context) (*release, error) {
			called = true
			return nil, nil
		}),
	}
	<-r.Run(context.Background())
	if called {
		t.Error("THLIBO_NO_UPDATE=1 must suppress the check")
	}
}

func TestRunnerDevBuildNeverChecks(t *testing.T) {
	isolateEnv(t)
	called := false
	r := &Runner{
		Current: "dev",
		Fetcher: fetcherFn(func(ctx context.Context) (*release, error) {
			called = true
			return nil, nil
		}),
	}
	<-r.Run(context.Background())
	if called {
		t.Error("dev build must skip the fetch")
	}
}

func TestRunnerIntervalZeroDisables(t *testing.T) {
	isolateEnv(t)
	t.Setenv(envInterval, "0")

	called := false
	r := &Runner{
		Current: "v0.2.0",
		Fetcher: fetcherFn(func(ctx context.Context) (*release, error) {
			called = true
			return nil, nil
		}),
	}
	<-r.Run(context.Background())
	if called {
		t.Error("THLIBO_UPDATE_INTERVAL=0 must suppress the check")
	}
}

func TestRunnerFetchFailureIsSilent(t *testing.T) {
	home := isolateEnv(t)

	var out bytes.Buffer
	r := &Runner{
		Current: "v0.2.0",
		Fetcher: fetcherFn(func(ctx context.Context) (*release, error) {
			return nil, errorString("network down")
		}),
		Out: &out,
	}
	<-r.Run(context.Background())

	if out.String() != "" {
		t.Errorf("fetch failure must not write to Out; got %q", out.String())
	}
	// State still updated so the cooldown kicks in - we don't
	// retry every invocation when the network's down.
	statePath := filepath.Join(home, ".thlibo", "state", "update-check.json")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("state must be written even on error: %v", err)
	}
	var s state
	_ = json.Unmarshal(raw, &s)
	if s.LastErr == "" {
		t.Error("LastErr should be populated on failure")
	}
}

// tiny adapter so tests can supply an inline fetch function.
type fetcherFn func(ctx context.Context) (*release, error)

func (f fetcherFn) Fetch(ctx context.Context) (*release, error) { return f(ctx) }

type errorString string

func (e errorString) Error() string { return string(e) }
