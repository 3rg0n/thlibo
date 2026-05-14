// Package update implements the auto-update check for thlibo. One
// HTTP GET per cooldown window against api.github.com/repos/.../releases/latest,
// cached to ~/.thlibo/state/update-check.json. When a newer release
// is available, emits a single banner line to stderr pointing the
// user at the install script.
//
// What this package deliberately does NOT do:
//
//   - Download binaries. Upgrade goes through the signed installer
//     (scripts/install.{sh,ps1}); the updater only tells the user
//     to run it. Keeps the attack surface tiny.
//   - Run during daemon boot. thlibod must never reach the network
//     on its own; the check only runs in the user-facing `thlibo`
//     CLI where the user is already operating consciously.
//   - Block the caller. Every check runs in a detached goroutine
//     behind a short context timeout; errors are swallowed into
//     the NDJSON log. A cold network must not delay `thlibo
//     rewrite` (which is on the Claude Code hot path).
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultReleaseAPI is the GitHub REST URL for this repo's latest
// release. Can be overridden via $THLIBO_UPDATE_API for testing or
// for a private mirror.
const DefaultReleaseAPI = "https://api.github.com/repos/3rg0n/thlibo/releases/latest"

// DefaultTimeout caps the HTTP fetch so a slow network doesn't stall
// the calling CLI even briefly. GitHub's API typically responds in
// <300ms; 5s is a wide margin.
const DefaultTimeout = 5 * time.Second

// UserAgent is required by GitHub's API (any non-empty value); we
// send the version so server-side analytics / rate-limit troubleshooting
// can tell releases apart.
const UserAgent = "thlibo-updater"

// Decision is the outcome of one check.
type Decision struct {
	// Current is the tag of the running binary (passed in by the
	// caller; the updater does not read version.Tag itself so tests
	// can drive the comparison).
	Current string
	// Latest is the tag reported by GitHub at check time. Empty if
	// the fetch failed.
	Latest string
	// UpgradeAvailable is true iff Latest is strictly newer than
	// Current under semver-ish ordering.
	UpgradeAvailable bool
	// URL is the human-readable release page URL, for inclusion in
	// the banner we print.
	URL string
	// FetchedAt is the time the GitHub response was received. Used
	// by the runner to respect the cooldown.
	FetchedAt time.Time
}

// Fetcher pulls the latest release metadata. Separate interface so
// tests can substitute a canned responder.
type Fetcher interface {
	Fetch(ctx context.Context) (*release, error)
}

// release is the subset of GitHub's release JSON we look at. Keep
// the shape as narrow as possible so a future GitHub schema change
// in unrelated fields doesn't break us.
type release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
}

// HTTPFetcher is the production Fetcher. Uses http.DefaultClient
// with a per-request context timeout. No authentication — the repo
// is public and we only touch the public endpoint.
type HTTPFetcher struct {
	URL    string
	Client *http.Client
}

// NewHTTPFetcher returns a Fetcher pointing at url (or DefaultReleaseAPI
// when url is empty) with a default-timeout client.
func NewHTTPFetcher(url string) *HTTPFetcher {
	if url == "" {
		url = DefaultReleaseAPI
	}
	return &HTTPFetcher{
		URL:    url,
		Client: &http.Client{Timeout: DefaultTimeout},
	}
}

// Fetch implements Fetcher.
func (f *HTTPFetcher) Fetch(ctx context.Context) (*release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("update: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read up to 512 bytes of the body so the error message is
		// useful without blowing up on a huge payload.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("update: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var r release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return nil, fmt.Errorf("update: decode: %w", err)
	}
	return &r, nil
}

// ErrDevBuild is returned by Check when the caller passes a "dev"
// current tag. Never-nag-developers policy.
var ErrDevBuild = errors.New("update: dev build, skipping check")

// Check asks fetcher for the latest release and compares against
// current. Returns a Decision; errors flow through to the caller
// which typically logs and swallows.
func Check(ctx context.Context, current string, fetcher Fetcher) (Decision, error) {
	if current == "" || current == "dev" {
		return Decision{Current: current, FetchedAt: time.Now().UTC()}, ErrDevBuild
	}
	r, err := fetcher.Fetch(ctx)
	if err != nil {
		return Decision{Current: current, FetchedAt: time.Now().UTC()}, err
	}
	if r.Draft || r.TagName == "" {
		// A draft or otherwise-unnamed release is not a real
		// upgrade target; silently ignore.
		return Decision{
			Current:   current,
			Latest:    r.TagName,
			URL:       r.HTMLURL,
			FetchedAt: time.Now().UTC(),
		}, nil
	}
	return Decision{
		Current:          current,
		Latest:           r.TagName,
		UpgradeAvailable: newerThan(r.TagName, current),
		URL:              r.HTMLURL,
		FetchedAt:        time.Now().UTC(),
	}, nil
}

// newerThan reports whether latest is strictly newer than current
// under semver-ish ordering. Accepts leading "v", ignores trailing
// pre-release suffixes. Any non-numeric component fails-closed
// (returns false) so a malformed tag cannot produce a nag.
func newerThan(latest, current string) bool {
	la, lok := parseTag(latest)
	ca, cok := parseTag(current)
	if !lok || !cok {
		return false
	}
	for i := 0; i < 3; i++ {
		if la[i] != ca[i] {
			return la[i] > ca[i]
		}
	}
	return false
}

// parseTag splits "v1.2.3" / "v1.2.3-rc.1" / "1.2.3" into three
// integers. Pre-release suffixes are ignored (the "-foo" part);
// upgrading from v0.2.0 to v0.3.0-rc.1 is not offered automatically
// — the updater only chases stable-looking semver numbers.
func parseTag(tag string) (parts [3]int, ok bool) {
	tag = strings.TrimPrefix(strings.TrimSpace(tag), "v")
	if tag == "" {
		return
	}
	// Drop pre-release suffix.
	if i := strings.IndexByte(tag, '-'); i >= 0 {
		tag = tag[:i]
	}
	if i := strings.IndexByte(tag, '+'); i >= 0 {
		tag = tag[:i]
	}

	segs := strings.Split(tag, ".")
	if len(segs) < 1 || len(segs) > 3 {
		return
	}
	for i, s := range segs {
		n := atoi(s)
		if n < 0 {
			return parts, false
		}
		parts[i] = n
	}
	return parts, true
}

// atoi is a tiny non-negative base-10 parser. Returns -1 on any
// non-digit content so the caller can fail-closed without importing
// strconv.
func atoi(s string) int {
	if s == "" {
		return -1
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
		if n > 1_000_000 {
			return -1 // runaway
		}
	}
	return n
}
