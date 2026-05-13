package pullcmd

import "testing"

// TestUnknownModel: unknown model name exits ExitUnknownModel.
func TestUnknownModel(t *testing.T) {
	code := Run([]string{"--quiet", "not-a-real-model"})
	if code != ExitUnknownModel {
		t.Errorf("exit = %d, want %d", code, ExitUnknownModel)
	}
}

// TestUnpinnedDefaultModelRefusal: the default model ships unpinned
// in v0.1. Without --allow-unpinned, `thlibo pull` refuses.
func TestUnpinnedDefaultModelRefusal(t *testing.T) {
	// Point to a temp dir so we never write to ~/.thlibo/models/.
	dir := t.TempDir()
	code := Run([]string{"--quiet", "--dir", dir, "gemma-4-e4b"})
	if code != ExitUnpinned {
		t.Errorf("exit = %d, want %d (unpinned refusal)", code, ExitUnpinned)
	}
}

// TestHumanBytes: quick coverage of the formatter so a regression
// in the suffix logic doesn't ship silently.
func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{2048, "2.0 KiB"},
		{5 * 1024 * 1024, "5.0 MiB"},
		{3 * 1024 * 1024 * 1024, "3.0 GiB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
