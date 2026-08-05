package version

import "testing"

// IsDev gates the background update check: a dev build must never nag
// (main.go wires version.Tag into update.Runner.Current, and the runner
// treats a dev tag as "never offer an upgrade"). The empty case matters
// because an -ldflags typo yields "" rather than "dev", and that must
// still count as dev — otherwise a mis-built binary would start
// offering upgrades against a tag it can't compare.
func TestIsDev(t *testing.T) {
	for _, tc := range []struct {
		tag  string
		want bool
	}{
		{"dev", true},
		{"", true},
		{"v0.11.4", false},
		{"v0.11.4-rc.1", false},
		// Not special-cased anywhere: only the exact strings "" and
		// "dev" are dev. A tag that merely contains "dev" is a release.
		{"v0.11.4-dev", false},
		{"development", false},
	} {
		orig := Tag
		Tag = tc.tag
		got := IsDev()
		Tag = orig
		if got != tc.want {
			t.Errorf("Tag=%q: IsDev() = %v, want %v", tc.tag, got, tc.want)
		}
	}
}

// The shipped default must be "dev" so a plain `go build` (no -ldflags)
// is never mistaken for a release.
func TestDefaultTagIsDev(t *testing.T) {
	if Tag != "dev" {
		t.Fatalf("default Tag = %q, want \"dev\" — a plain `go build` must not look like a release", Tag)
	}
	if !IsDev() {
		t.Fatal("default build must report IsDev() == true")
	}
}
