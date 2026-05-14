// Package version exposes the build-time release tag of the running
// binary. Value is set via `go build -ldflags "-X
// github.com/3rg0n/thlibo/internal/version.Tag=v0.2.0"` in
// .github/workflows/release.yml.
//
// A development build (plain `go build` from a working tree) leaves
// Tag as "dev"; the updater treats that value as "never offer an
// upgrade" so developers aren't nagged when working against main.
package version

// Tag is the semver release tag this binary was built from, including
// the leading "v". Overridden at build time.
var Tag = "dev"

// IsDev reports whether this is an untagged development build.
func IsDev() bool {
	return Tag == "" || Tag == "dev"
}
