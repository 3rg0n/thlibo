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
		Current:  "v0.2.0",
		Fetcher:  &stubFetcher{rel: &release{TagName: "v0.3.0", HTMLURL: "https://x"}},
		Out:      &out,
		Headless: boolPtr(false), // force interactive so banner goes to Out
	}
	<-r.Run(context.Background())

	if !strings.Contains(out.String(), "update available: v0.2.0 -> v0.3.0") {
		t.Fatalf("banner missing / malformed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "thlibo upgrade") {
		t.Fatalf("banner must reference 'thlibo upgrade':\n%s", out.String())
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
		Current:  "v0.2.0",
		Out:      &out,
		Headless: boolPtr(false), // force interactive so banner goes to Out
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

func boolPtr(b bool) *bool { return &b }

func TestRunnerHeadlessNoticeInjectedOnce(t *testing.T) {
	home := isolateEnv(t)
	statePath := filepath.Join(home, ".thlibo", "state", "update-check.json")

	var stdout bytes.Buffer
	r := &Runner{
		Current:   "v0.7.2",
		Fetcher:   &stubFetcher{rel: &release{TagName: "v0.7.3", HTMLURL: "https://x"}},
		Stdout:    &stdout,
		Headless:  boolPtr(true),
		StatePath: statePath,
	}
	<-r.Run(context.Background())

	if stdout.String() != NoticeLine {
		t.Fatalf("headless notice = %q, want %q", stdout.String(), NoticeLine)
	}

	// Second run within cooldown: HeadlessNotifiedTag is set;
	// notice must NOT fire again.
	stdout.Reset()
	r2 := &Runner{
		Current:   "v0.7.2",
		Fetcher:   &stubFetcher{rel: &release{TagName: "v0.7.3", HTMLURL: "https://x"}},
		Stdout:    &stdout,
		Headless:  boolPtr(true),
		StatePath: statePath,
		Interval:  24 * time.Hour, // keep in cooldown window
	}
	<-r2.Run(context.Background())
	if stdout.String() != "" {
		t.Fatalf("headless notice fired twice; got %q", stdout.String())
	}
}

func TestRunnerHeadlessDoesNotFireBanner(t *testing.T) {
	isolateEnv(t)

	var stderr bytes.Buffer
	r := &Runner{
		Current:  "v0.7.2",
		Fetcher:  &stubFetcher{rel: &release{TagName: "v0.7.3", HTMLURL: "https://x"}},
		Out:      &stderr,
		Headless: boolPtr(true),
	}
	<-r.Run(context.Background())

	if strings.Contains(stderr.String(), "update available") {
		t.Errorf("headless mode must not print banner to stderr; got %q", stderr.String())
	}
}

func TestRunnerInteractiveBannerPointsAtUpgrade(t *testing.T) {
	isolateEnv(t)

	var stderr bytes.Buffer
	r := &Runner{
		Current:  "v0.7.2",
		Fetcher:  &stubFetcher{rel: &release{TagName: "v0.7.3", HTMLURL: "https://x"}},
		Out:      &stderr,
		Headless: boolPtr(false),
	}
	<-r.Run(context.Background())

	banner := stderr.String()
	if !strings.Contains(banner, "thlibo upgrade") {
		t.Errorf("banner must reference 'thlibo upgrade'; got:\n%s", banner)
	}
	if strings.Contains(banner, "curl") {
		t.Errorf("banner must NOT contain raw curl one-liner; got:\n%s", banner)
	}
}

func TestRunnerNoticeLiteralIsConstant(t *testing.T) {
	// Verify NoticeLine contains no format verbs and is byte-identical
	// across invocations (no tag interpolation).
	if NoticeLine != "[thlibo] new update available, run: thlibo upgrade\n" {
		t.Errorf("NoticeLine changed: %q", NoticeLine)
	}
}

// tiny adapter so tests can supply an inline fetch function.
type fetcherFn func(ctx context.Context) (*release, error)

func (f fetcherFn) Fetch(ctx context.Context) (*release, error) { return f(ctx) }

type errorString string

func (e errorString) Error() string { return string(e) }
