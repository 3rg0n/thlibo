package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/3rg0n/thlibo/internal/logx"
)

// DefaultInterval is the minimum time between checks. Users can
// override via $THLIBO_UPDATE_INTERVAL (Go duration; 0 disables).
const DefaultInterval = 24 * time.Hour

// envNoUpdate is the opt-out kill switch. Same naming convention as
// THLIBO_DISABLED / THLIBO_POLICY / THLIBO_UPDATE_INTERVAL.
const (
	envNoUpdate = "THLIBO_NO_UPDATE"
	envInterval = "THLIBO_UPDATE_INTERVAL"
	envAPI      = "THLIBO_UPDATE_API"
)

// state is what we persist between runs. Stored at
// ~/.thlibo/state/update-check.json.
type state struct {
	CheckedAt      time.Time `json:"checked_at"`
	LatestTag      string    `json:"latest_tag"`
	NotifiedTag    string    `json:"notified_tag"` // last tag we printed a banner for
	SeenURL        string    `json:"url"`
	LastErr        string    `json:"last_err,omitempty"`
	LastErrAt      time.Time `json:"last_err_at,omitempty"`
}

// Runner orchestrates the check: read state, decide whether cooldown
// has expired, launch the background fetch, persist the result, print
// the banner on a fresh upgrade notice.
type Runner struct {
	// Current is the binary's own tag. Pass version.Tag here.
	Current string
	// Fetcher is the release metadata source. Nil defaults to a
	// HTTPFetcher against DefaultReleaseAPI (or $THLIBO_UPDATE_API).
	Fetcher Fetcher
	// StatePath is where the cache file lives. Empty = default
	// (~/.thlibo/state/update-check.json).
	StatePath string
	// Out is where the banner is written. Typically os.Stderr so
	// the upgrade notice doesn't pollute a piped stdout.
	Out io.Writer
	// Logger receives structured records for fetch failures and
	// skips. nil is safe.
	Logger *logx.Logger
	// Interval overrides DefaultInterval. Zero means use the
	// environment / default.
	Interval time.Duration
}

// Run performs one check attempt, asynchronously. Returns
// immediately; the actual work runs in a detached goroutine so
// Run is safe to call from a CLI hot path.
//
// The returned done channel closes when the goroutine exits, so
// tests can wait deterministically. Production callers ignore it.
func (r *Runner) Run(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})

	if r.shouldSkipEnv() {
		close(done)
		return done
	}

	go func() {
		defer close(done)
		r.runOnce(ctx)
	}()
	return done
}

// shouldSkipEnv handles the kill switches that don't need filesystem
// state.
func (r *Runner) shouldSkipEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envNoUpdate))) {
	case "1", "true", "on", "yes":
		return true
	}
	if v, ok := parseInterval(os.Getenv(envInterval)); ok && v == 0 {
		return true
	}
	return r.Current == "" || r.Current == "dev"
}

// runOnce is the inside of the goroutine - synchronous, easy to
// unit-test by driving Run and waiting on the returned channel.
func (r *Runner) runOnce(ctx context.Context) {
	interval := r.Interval
	if interval == 0 {
		if v, ok := parseInterval(os.Getenv(envInterval)); ok {
			interval = v
		} else {
			interval = DefaultInterval
		}
	}

	statePath := r.resolveStatePath()
	prev, _ := loadState(statePath) // missing/corrupt -> zero value, silently rechecked

	// Cooldown: skip if we checked recently AND we don't have a
	// pending upgrade to re-notify about.
	if !prev.CheckedAt.IsZero() && time.Since(prev.CheckedAt) < interval {
		// Still re-show the banner for an unacknowledged upgrade
		// (one banner per new tag; NotifiedTag tracks the last).
		if prev.LatestTag != "" && prev.LatestTag != prev.NotifiedTag &&
			newerThan(prev.LatestTag, r.Current) {
			r.printBanner(prev.LatestTag, prev.SeenURL)
			prev.NotifiedTag = prev.LatestTag
			_ = saveState(statePath, prev)
		}
		return
	}

	fetcher := r.Fetcher
	if fetcher == nil {
		fetcher = NewHTTPFetcher(os.Getenv(envAPI))
	}

	fetchCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	decision, err := Check(fetchCtx, r.Current, fetcher)
	next := state{CheckedAt: time.Now().UTC(), NotifiedTag: prev.NotifiedTag}
	switch {
	case errors.Is(err, ErrDevBuild):
		return
	case err != nil:
		next.LastErr = err.Error()
		next.LastErrAt = next.CheckedAt
		if r.Logger != nil {
			r.Logger.Debug("update_check_failed", logx.Err(err))
		}
	default:
		next.LatestTag = decision.Latest
		next.SeenURL = decision.URL
		if decision.UpgradeAvailable && decision.Latest != prev.NotifiedTag {
			r.printBanner(decision.Latest, decision.URL)
			next.NotifiedTag = decision.Latest
		}
	}
	_ = saveState(statePath, next)
}

// printBanner writes the upgrade notice. One line, stderr, easy to
// grep away. Uses ANSI bold if Out is a terminal; otherwise plain.
func (r *Runner) printBanner(latest, url string) {
	w := r.Out
	if w == nil {
		w = os.Stderr
	}
	// Respect NO_COLOR (https://no-color.org/).
	color := os.Getenv("NO_COLOR") == ""
	bold, reset := "", ""
	if color {
		bold = "\x1b[1m"
		reset = "\x1b[0m"
	}
	if url == "" {
		url = "https://github.com/3rg0n/thlibo/releases/latest"
	}
	fmt.Fprintf(w,
		"%s[thlibo] update available: %s -> %s%s  (%s)\n"+
			"          upgrade: curl -fsSL https://raw.githubusercontent.com/3rg0n/thlibo/main/scripts/install.sh | bash\n",
		bold, r.Current, latest, reset, url)
}

// resolveStatePath returns the path to the state file, creating the
// parent directory as needed.
func (r *Runner) resolveStatePath() string {
	if r.StatePath != "" {
		return r.StatePath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "thlibo-update-check.json")
	}
	return filepath.Join(home, ".thlibo", "state", "update-check.json")
}

// loadState reads the cached state from disk. Missing / malformed =
// zero value, no error bubbled up.
func loadState(path string) (state, error) {
	var s state
	buf, err := os.ReadFile(path) // #nosec G304 -- path is thlibo-config-derived
	if err != nil {
		return s, err
	}
	_ = json.Unmarshal(buf, &s)
	return s, nil
}

// saveState writes the cache atomically. 0o600: the state file leaks
// no sensitive data, but same permission policy as the NDJSON log.
func saveState(path string, s state) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// parseInterval accepts "0" / "off" / "never" / a Go duration.
func parseInterval(s string) (time.Duration, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, false
	}
	switch s {
	case "0", "off", "never", "false":
		return 0, true
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, false
	}
	return d, true
}
