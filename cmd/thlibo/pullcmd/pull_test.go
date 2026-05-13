package pullcmd

import (
	"testing"

	"github.com/3rg0n/thlibo/internal/install"
)

// TestUnknownModel: unknown model name exits ExitUnknownModel.
func TestUnknownModel(t *testing.T) {
	code := Run([]string{"--quiet", "not-a-real-model"})
	if code != ExitUnknownModel {
		t.Errorf("exit = %d, want %d", code, ExitUnknownModel)
	}
}

// TestUnpinnedModelRefusal: a registered model with no pinned SHA
// is refused without --allow-unpinned. We test by swapping in a
// temporary registry entry; we can't use the real default because
// it ships with a pinned hash and the refusal test would try a
// real HuggingFace download.
func TestUnpinnedModelRefusal(t *testing.T) {
	const probe = "probe-unpinned-model"
	orig, hadOrig := knownModels[probe]
	t.Cleanup(func() {
		if hadOrig {
			knownModels[probe] = orig
		} else {
			delete(knownModels, probe)
		}
	})
	knownModels[probe] = install.Model{
		Name:     probe,
		URL:      "https://127.0.0.1:1/probe.gguf", // never dialed: refusal happens before the GET
		Filename: "probe.gguf",
		// ExpectedSHA256 deliberately empty.
	}

	dir := t.TempDir()
	code := Run([]string{"--quiet", "--dir", dir, probe})
	if code != ExitUnpinned {
		t.Errorf("exit = %d, want %d (unpinned refusal)", code, ExitUnpinned)
	}
}

// TestDefaultModelIsPinned: a sanity check that someone didn't
// accidentally commit a source-code regression erasing the pinned
// SHA. Doesn't actually run a download.
func TestDefaultModelIsPinned(t *testing.T) {
	if install.DefaultModel.ExpectedSHA256 == "" {
		t.Error("DefaultModel.ExpectedSHA256 is empty; a release hash must be pinned in internal/install/model.go or via -ldflags at build time")
	}
	if len(install.DefaultModel.ExpectedSHA256) != 64 {
		t.Errorf("DefaultModel.ExpectedSHA256 = %q; expected 64-char hex sha256", install.DefaultModel.ExpectedSHA256)
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
