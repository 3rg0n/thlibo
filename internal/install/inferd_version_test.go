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

func TestParseSemverTuple_Garbage(t *testing.T) {
	if got := parseSemverTuple("hello"); got != [4]int{0, 0, 0, 0} {
		t.Errorf("parseSemverTuple(garbage) = %v, want [0 0 0 0]", got)
	}
	if got := parseSemverTuple(""); got != [4]int{0, 0, 0, 0} {
		t.Errorf("parseSemverTuple(empty) = %v, want [0 0 0 0]", got)
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
