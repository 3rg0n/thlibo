// Package config loads ~/.thlibo/config.yaml. Used by hooks and
// subcommands that need user-tunable behaviour (auto-shorthand,
// path globs, etc.). Keeps the schema in one place so stage 4's
// interactive setup writes the same shape every other consumer
// reads.
//
// Resolution order (highest wins):
//
//  1. CLI flags
//  2. Environment variables (e.g. $THLIBO_AUTO_SHORTHAND)
//  3. ~/.thlibo/config.yaml (or $THLIBO_CONFIG)
//  4. Compiled-in Defaults()
//
// Loading is deliberately permissive: a missing or malformed
// config returns Defaults() with a warning to stderr — never an
// error. The hooks must keep working when the user hasn't run
// `thlibo config` yet.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the full user-tunable settings shape. Keep additions
// non-breaking: every field has a zero-value default that matches
// historical behaviour.
type Config struct {
	// AutoShorthandOnWrite, when true, intercepts Write/Edit tool
	// calls on matching paths and runs `thlibo shorthand` before
	// the file lands on disk. Eval-gated; failure = passthrough.
	AutoShorthandOnWrite bool `yaml:"auto_shorthand_on_write"`

	// AutoShorthandPaths is the glob list checked against the
	// tool_input.file_path. Empty = use the built-in default set.
	AutoShorthandPaths []string `yaml:"auto_shorthand_paths"`

	// AutoShorthandMinBytes — files under this size are passed
	// through. Compression isn't worth the round-trip on tiny docs.
	AutoShorthandMinBytes int `yaml:"auto_shorthand_min_bytes"`

	// AutoShorthandYAMLProse — when true, YAML files are walked
	// scalar-by-scalar instead of treated as flat text. Only
	// block-scalar prose values get rewritten; structural keys,
	// lists, and `allowed_tools`-style fields stay byte-identical.
	// Stage 3 lands this; the field is plumbed now so the schema
	// stays stable.
	AutoShorthandYAMLProse bool `yaml:"auto_shorthand_yaml_prose"`
}

// Defaults returns the off-by-default settings. Auto-shorthand is
// opt-in: a fresh thlibo install never silently rewrites the user's
// files until they run `thlibo config`.
func Defaults() Config {
	return Config{
		AutoShorthandOnWrite: false,
		AutoShorthandPaths: []string{
			"**/SKILL.md",
			"**/CLAUDE.md",
			"**/AGENTS.md",
			"**/agents.md",
			"**/.claude/skills/**/*.md",
			"**/prompts/*.yaml",
			"**/prompts/*.yml",
		},
		AutoShorthandMinBytes:  500,
		AutoShorthandYAMLProse: false,
	}
}

// Path returns the resolved config path: $THLIBO_CONFIG if set,
// else ~/.thlibo/config.yaml.
func Path() (string, error) {
	if p := os.Getenv("THLIBO_CONFIG"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".thlibo", "config.yaml"), nil
}

// Load reads ~/.thlibo/config.yaml (or $THLIBO_CONFIG) on top of
// Defaults(). Missing or malformed file = Defaults() with a warning.
// Never returns an error — the hooks rely on this never failing.
func Load() Config {
	cfg := Defaults()

	path, err := Path()
	if err != nil {
		fmt.Fprintln(os.Stderr, "thlibo config: cannot resolve home dir:", err)
		return cfg
	}

	data, err := os.ReadFile(path) // #nosec G304 -- $THLIBO_CONFIG is operator-set
	if err != nil {
		// Missing config = use defaults silently. Other read errors
		// (permission denied) get surfaced because they're operator-
		// fixable.
		if !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "thlibo config: read", path+":", err)
		}
		return cfg
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintln(os.Stderr, "thlibo config: parse", path+":", err)
		return Defaults()
	}

	// Re-fill empty slices with defaults so a partial config gets
	// the path globs without requiring the user to spell them out.
	if len(cfg.AutoShorthandPaths) == 0 {
		cfg.AutoShorthandPaths = Defaults().AutoShorthandPaths
	}
	if cfg.AutoShorthandMinBytes <= 0 {
		cfg.AutoShorthandMinBytes = Defaults().AutoShorthandMinBytes
	}

	return cfg
}

// MatchesAutoShorthandPath reports whether path falls within the
// configured glob list. Match is glob-style (** for any directories,
// * for any segment). Path separators are normalised to forward
// slashes so the same config works on Windows and Unix.
func (c Config) MatchesAutoShorthandPath(path string) bool {
	norm := filepath.ToSlash(path)
	for _, g := range c.AutoShorthandPaths {
		if matchGlob(g, norm) {
			return true
		}
	}
	return false
}

// matchGlob is a small ** / * matcher. We don't use filepath.Match
// because it doesn't support ** (cross-directory wildcard) and
// ToSlash-normalises away the OS difference but not the segment
// semantics.
func matchGlob(pattern, path string) bool {
	return globMatch(pattern, path)
}

// globMatch implements the subset of glob we need:
//
//	*     matches any run of non-/ characters
//	**    matches any run of characters including /
//	?     matches a single non-/ character
//
// Anchored at both ends.
func globMatch(pattern, path string) bool {
	// Recursive matcher; pattern length is bounded by config so
	// stack depth is fine.
	for len(pattern) > 0 {
		switch {
		case len(pattern) >= 2 && pattern[:2] == "**":
			// Trim trailing /
			rest := pattern[2:]
			if len(rest) > 0 && rest[0] == '/' {
				rest = rest[1:]
			}
			if rest == "" {
				return true
			}
			for i := 0; i <= len(path); i++ {
				if globMatch(rest, path[i:]) {
					return true
				}
			}
			return false
		case pattern[0] == '*':
			rest := pattern[1:]
			if rest == "" {
				// Last segment must not contain /.
				for _, c := range path {
					if c == '/' {
						return false
					}
				}
				return true
			}
			for i := 0; i <= len(path); i++ {
				if i > 0 && path[i-1] == '/' {
					return false
				}
				if globMatch(rest, path[i:]) {
					return true
				}
			}
			return false
		case pattern[0] == '?':
			if len(path) == 0 || path[0] == '/' {
				return false
			}
			pattern = pattern[1:]
			path = path[1:]
		default:
			if len(path) == 0 || pattern[0] != path[0] {
				return false
			}
			pattern = pattern[1:]
			path = path[1:]
		}
	}
	return path == ""
}
