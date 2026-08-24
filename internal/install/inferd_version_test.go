package install

import "testing"

func TestVersionIsOlder(t *testing.T) {
	cases := []struct {
		got, want string
		older     bool
	}{
		{"v0.1.11", "v0.1.13", true},
		{"v0.1.12", "v0.1.13", true},
		{"v0.1.13", "v0.1.13", false},
		{"v0.1.14", "v0.1.13", false},
		{"v0.2.0", "v0.1.13", false},
		{"v1.0.0", "v0.1.13", false},

		{"0.1.11", "v0.1.13", true},
		{"v0.1.11", "0.1.13", true},

		{"0.1.13-rc1", "v0.1.13", false},
		{"v0.1.12-rc4", "v0.1.13", true},

		{"", "v0.1.13", false},

		{"v0.0.0.1", "v0.1.13", true},
		{"v0.1.13.1", "v0.1.13", false},
	}
	for _, tc := range cases {
		got := versionIsOlder(tc.got, tc.want)
		if got != tc.older {
			t.Errorf("versionIsOlder(%q, %q) = %v, want %v", tc.got, tc.want, got, tc.older)
		}
	}
}

// TestVersionIsOlder_Garbage pins the direction the comparator errs in
// when it cannot fingerprint a binary: not-older, so the caller leaves
// the daemon alone.
//
// This replaces an assertion that pinned the opposite. The old test
// asserted parseSemverTuple("hello") == [0,0,0,0] and left it there, so
// an unparseable fingerprint compared older than every real version —
// which contradicted versionIsOlder's own doc comment and was the #132
// defect: `stopInferd()` on a healthy inferd 0.8.0, then a re-download
// and a copy over the live binary, on every install.
//
// parseSemverTuple still zeros a bad component. That is fine as long as
// nothing compares its output without looksLikeVersion first, which is
// why the assertions below are on versionIsOlder, not on the tuple.
func TestVersionIsOlder_Garbage(t *testing.T) {
	notVersions := []string{
		"available)", // the #132 fingerprint: last token of inferd 0.8.0's second line
		"hello",      // no digits at all
		"inferd-0.8", // digits, but not where a version has them
		"0",          // a lone number is not a version
		"2026-08-21", // a date must not read as version 2026
		"profile:",   // a bare word from the build-profile line
		"",           // nothing to fingerprint
		"   ",        // whitespace only
		"1.2.3.4.5",  // more components than the comparator handles
		"v.1.2",      // empty leading component
	}
	for _, v := range notVersions {
		if looksLikeVersion(v) {
			t.Errorf("looksLikeVersion(%q) = true, want false", v)
		}
		if versionIsOlder(v, MinInferdVersion) {
			t.Errorf("versionIsOlder(%q, %q) = true; an unfingerprintable binary must not be flagged for upgrade", v, MinInferdVersion)
		}
	}

	areVersions := []string{"0.8.0", "v0.8.0", "0.1.13-rc1", "v0.0.0.1", "1.0", "1.2.3+build4"}
	for _, v := range areVersions {
		if !looksLikeVersion(v) {
			t.Errorf("looksLikeVersion(%q) = false, want true", v)
		}
	}
}

// TestParseVersionOutput_RealInferd0_8_0 feeds the parser the exact
// stdout of the inferd-daemon 0.8.0 shipped on 2026-08-21, captured on
// Windows with stdout and stderr split (both lines are on stdout, which
// is what exec.Output() reads, so the trailing token really was the tail
// of the second line).
//
// The old reader returned the last whitespace token of the whole dump.
// Against this input that is "available)".
func TestParseVersionOutput_RealInferd0_8_0(t *testing.T) {
	const real = "inferd-daemon 0.8.0\n" +
		"build profile: networked (model-fetch on — ADR 0010 HTTPS model bootstrap available)\n"

	got := parseVersionOutput(real)
	if got != "0.8.0" {
		t.Fatalf("parseVersionOutput(inferd 0.8.0 --version) = %q, want %q", got, "0.8.0")
	}
	// The whole point: this version must not be flagged for upgrade.
	if versionIsOlder(got, MinInferdVersion) {
		t.Errorf("versionIsOlder(%q, %q) = true; a current daemon must take InstallInferd's step-1 early return, not stopInferd()", got, MinInferdVersion)
	}
}

func TestParseVersionOutput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"single line, the pre-0.8.0 shape", "inferd-daemon 0.1.12\n", "0.1.12"},
		{"leading v", "inferd-daemon v0.5.0\n", "v0.5.0"},
		{"release candidate", "inferd-daemon 0.9.0-rc.1\n", "0.9.0-rc.1"},
		{"trailing line holds a number", "inferd-daemon 0.8.0\nbuilt from commit 9f2c1a\n", "0.8.0"},
		// A trailing line that itself looks like a version must not win:
		// first match, and the name/version line comes first.
		{"trailing line looks like a version", "inferd-daemon 0.8.0\nbackend gemma 4.0.1\n", "0.8.0"},
		{"no version anywhere", "usage: inferd-daemon [flags]\n", ""},
		{"empty output", "", ""},
	}
	for _, tc := range cases {
		if got := parseVersionOutput(tc.in); got != tc.want {
			t.Errorf("%s: parseVersionOutput(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// TestMinInferdVersion_Floor pins the active floor so a careless
// constant edit fails fast. Bumping the floor is a deliberate act
// (covered by the doc comment in inferd.go); the constant should not
// drift via copy-paste of test fixtures.
func TestMinInferdVersion_Floor(t *testing.T) {
	const want = "v0.4.0" // floored at the unified-wire migration (inferd ADR 0021)
	if MinInferdVersion != want {
		t.Errorf("MinInferdVersion = %q, want %q (update both the constant's doc comment and this test if intentional)", MinInferdVersion, want)
	}
	// And the constant must still parse cleanly through the comparator.
	if versionIsOlder(MinInferdVersion, MinInferdVersion) {
		t.Errorf("MinInferdVersion compares older than itself; parser broken")
	}
}
