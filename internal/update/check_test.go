package update

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubFetcher struct {
	rel *release
	err error
}

func (s *stubFetcher) Fetch(ctx context.Context) (*release, error) {
	return s.rel, s.err
}

func TestNewerThan(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.1.0", "v0.2.0", false},
		{"v0.2.0", "v0.2.0", false},
		{"v1.0.0", "v0.9.9", true},
		{"v0.2.1", "v0.2.0", true},
		{"v0.2.0", "v0.2.1", false},
		{"0.2.0", "v0.1.0", true},  // no-v form accepted
		{"v0.2", "v0.1.0", true},   // two-segment accepted
		{"v0.2.0-rc.1", "v0.2.0", false}, // pre-release < release
		{"v0.3.0", "v0.2.0-rc.1", true},
		{"garbage", "v0.1.0", false}, // malformed fails-closed
		{"v0.1.0", "garbage", false},
	}
	for _, tc := range cases {
		if got := newerThan(tc.latest, tc.current); got != tc.want {
			t.Errorf("newerThan(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestCheckDev(t *testing.T) {
	_, err := Check(context.Background(), "dev", &stubFetcher{})
	if !errors.Is(err, ErrDevBuild) {
		t.Fatalf("dev build should short-circuit; got err=%v", err)
	}
}

func TestCheckUpgradeAvailable(t *testing.T) {
	f := &stubFetcher{rel: &release{TagName: "v0.3.0", HTMLURL: "https://example/x"}}
	d, err := Check(context.Background(), "v0.2.0", f)
	if err != nil {
		t.Fatal(err)
	}
	if !d.UpgradeAvailable {
		t.Error("expected UpgradeAvailable=true")
	}
	if d.Latest != "v0.3.0" {
		t.Errorf("latest = %q", d.Latest)
	}
	if d.URL != "https://example/x" {
		t.Errorf("url = %q", d.URL)
	}
}

func TestCheckDraftIgnored(t *testing.T) {
	f := &stubFetcher{rel: &release{TagName: "v0.3.0", Draft: true}}
	d, err := Check(context.Background(), "v0.2.0", f)
	if err != nil {
		t.Fatal(err)
	}
	if d.UpgradeAvailable {
		t.Error("draft release must not trigger upgrade")
	}
}

func TestCheckFetcherError(t *testing.T) {
	f := &stubFetcher{err: errors.New("boom")}
	d, err := Check(context.Background(), "v0.2.0", f)
	if err == nil {
		t.Fatal("expected error")
	}
	if d.Current != "v0.2.0" {
		t.Error("current should be preserved on error")
	}
	if d.UpgradeAvailable {
		t.Error("error path must not set UpgradeAvailable")
	}
	_ = time.Now
}

func TestParseTagMalformedFailsClosed(t *testing.T) {
	for _, s := range []string{"", "v", "vabc", "v1.2.3.4.5", "v-1.0.0", "v1.2.-3"} {
		if _, ok := parseTag(s); ok {
			t.Errorf("parseTag(%q) ok=true, want false", s)
		}
	}
}
