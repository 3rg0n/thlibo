package install

import "testing"

// TestAssetNameFor locks the inferd release asset names per platform to
// inferd's release.yml matrix. windows-arm64 was added in inferd v0.5.1
// (native aarch64-pc-windows-msvc build) — thlibo must request the
// matching .zip or a Windows-on-ARM install fails "platform not
// supported" even though inferd ships a tarball for it.
func TestAssetNameFor(t *testing.T) {
	cases := map[string]string{
		"linux-amd64":   "inferd-v0.5.1-x86_64-unknown-linux-gnu.tar.gz",
		"linux-arm64":   "inferd-v0.5.1-aarch64-unknown-linux-gnu.tar.gz",
		"darwin-arm64":  "inferd-v0.5.1-aarch64-apple-darwin.tar.gz",
		"windows-amd64": "inferd-v0.5.1-x86_64-pc-windows-msvc.zip",
		"windows-arm64": "inferd-v0.5.1-aarch64-pc-windows-msvc.zip",
	}
	for platform, want := range cases {
		if got := assetNameFor("v0.5.1", platform); got != want {
			t.Errorf("assetNameFor(v0.5.1, %q) = %q, want %q", platform, got, want)
		}
		// The "v" prefix is optional and must produce the same name.
		if got := assetNameFor("0.5.1", platform); got != want {
			t.Errorf("assetNameFor(0.5.1, %q) = %q, want %q (v-prefix should be stripped)", platform, got, want)
		}
	}
}

func TestAssetNameForUnsupported(t *testing.T) {
	for _, platform := range []string{"darwin-amd64", "linux-386", "freebsd-amd64", "windows-386", ""} {
		if got := assetNameFor("v0.5.1", platform); got != "" {
			t.Errorf("assetNameFor(v0.5.1, %q) = %q, want \"\" (unsupported)", platform, got)
		}
		if platformSupported(platform) {
			t.Errorf("platformSupported(%q) = true, want false", platform)
		}
	}
}

func TestPlatformSupported(t *testing.T) {
	for _, platform := range []string{"linux-amd64", "linux-arm64", "darwin-arm64", "windows-amd64", "windows-arm64"} {
		if !platformSupported(platform) {
			t.Errorf("platformSupported(%q) = false, want true", platform)
		}
	}
}
