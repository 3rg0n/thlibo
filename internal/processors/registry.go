// Package processors loads processor descriptors from
// ~/.thlibo/processors/ and from built-ins embedded in the binary,
// merges them (user wins over builtin of the same name), and exposes
// lookup + fast-path matching helpers for the middleware router.
package processors

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Registry is the set of known processors after scanning both built-in
// and user sources. Registry operations are read-only after Build
// returns; concurrent readers do not need to lock.
type Registry struct {
	byName map[string]*Descriptor
	order  []string // stable iteration (alphabetical) for deterministic router prompts
}

// Source bundles a filesystem to scan with an optional on-disk root
// so script processors can resolve their entry files. diskRoot may be
// "" for embed.FS-only sources (e.g. compiled-in built-ins not yet
// mirrored to disk); script processors from such sources will error
// at dispatch time, which the middleware treats as a fallback signal.
type Source struct {
	FS       fs.FS
	DiskRoot string       // absolute path, or "" if FS is not backed by disk
	Origin   OriginSource // Builtin or User
}

// Build scans user + builtin sources, merges them (user overrides
// builtin with the same name), and returns the registry plus any
// non-fatal warnings (B8g: quarantined processors whose descriptors
// failed to parse). A descriptor parse error is NOT a build failure:
// the offending processor is skipped so one broken folder doesn't
// deny the whole middleware.
func Build(builtin fs.FS, user fs.FS) (*Registry, []error, error) {
	return BuildFromSources(
		Source{FS: builtin, Origin: OriginBuiltin},
		Source{FS: user, Origin: OriginUser},
	)
}

// BuildFromDisk is a convenience constructor for the common case of
// loading builtins and user processors from known on-disk roots. An
// empty path is treated as "not present".
func BuildFromDisk(builtinDir, userDir string) (*Registry, []error, error) {
	b := Source{Origin: OriginBuiltin}
	if builtinDir != "" {
		abs, err := filepath.Abs(builtinDir)
		if err != nil {
			return nil, nil, err
		}
		b.FS = os.DirFS(abs)
		b.DiskRoot = abs
	}
	u := Source{Origin: OriginUser}
	if userDir != "" {
		abs, err := filepath.Abs(userDir)
		if err != nil {
			return nil, nil, err
		}
		u.FS = os.DirFS(abs)
		u.DiskRoot = abs
	}
	return BuildFromSources(b, u)
}

// BuildFromSources is the full-control entry point; callers supply
// each source's FS, disk root, and origin. Used by Build,
// BuildFromDisk, and by adapters that want to mix embed.FS builtins
// with on-disk user processors.
func BuildFromSources(sources ...Source) (*Registry, []error, error) {
	r := &Registry{byName: make(map[string]*Descriptor)}
	var warnings []error
	for _, s := range sources {
		if s.FS == nil {
			continue
		}
		if errs := r.scan(s); errs != nil {
			warnings = append(warnings, errs...)
		}
	}
	for name := range r.byName {
		r.order = append(r.order, name)
	}
	sort.Strings(r.order)
	return r, warnings, nil
}

// scan walks one source. Each top-level directory is a processor
// candidate; parse errors are collected and returned but do not abort
// the scan.
func (r *Registry) scan(s Source) []error {
	entries, err := fs.ReadDir(s.FS, ".")
	if err != nil {
		return []error{fmt.Errorf("processors: read %s root: %w", s.Origin, err)}
	}
	var warnings []error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		d, err := loadOne(s.FS, e.Name(), s.Origin)
		if err != nil {
			warnings = append(warnings, err)
			continue
		}
		if d == nil {
			continue // no descriptor file; silently skip
		}
		// Record the on-disk directory for script processor dispatch.
		if s.DiskRoot != "" {
			d.Origin.DiskDir = filepath.Join(s.DiskRoot, e.Name())
		}
		// User wins: overwrite any existing entry unconditionally when
		// origin is User. When origin is Builtin, only add if not
		// already present (shouldn't happen in practice since scan is
		// called builtin-first).
		if s.Origin == OriginUser {
			r.byName[d.Name] = d
		} else if _, ok := r.byName[d.Name]; !ok {
			r.byName[d.Name] = d
		}
	}
	return warnings
}

// loadOne reads the descriptor file(s) in one processor folder and
// returns a *Descriptor. Precedence per spec §"Descriptor rules":
//
//	processor.yaml present -> script processor, entry required
//	processor.md present   -> prompt processor, body is system prompt
//	both present           -> yaml wins (type=script), md body becomes
//	                          the system prompt (useful for hybrid)
//	neither present        -> folder ignored (returns nil, nil)
func loadOne(fsys fs.FS, dir string, origin OriginSource) (*Descriptor, error) {
	yamlPath := path.Join(dir, "processor.yaml")
	mdPath := path.Join(dir, "processor.md")

	yamlBytes, yamlErr := fs.ReadFile(fsys, yamlPath)
	mdBytes, mdErr := fs.ReadFile(fsys, mdPath)

	var d *Descriptor
	var err error
	switch {
	case yamlErr == nil && mdErr == nil:
		d, err = ParseYAML(yamlBytes, Origin{Source: origin, Path: yamlPath})
		if err == nil {
			// Use the md body as the system prompt for hybrid processors.
			_, body, ferr := splitFrontmatter(mdBytes)
			if ferr == nil {
				d.SystemPrompt = strings.TrimSpace(string(body))
			}
		}
	case yamlErr == nil:
		d, err = ParseYAML(yamlBytes, Origin{Source: origin, Path: yamlPath})
	case mdErr == nil:
		d, err = ParseMarkdown(mdBytes, Origin{Source: origin, Path: mdPath})
	default:
		// Neither file found -> silently skip.
		return nil, nil
	}
	return d, err
}

// Get returns the descriptor registered under name, or nil.
func (r *Registry) Get(name string) *Descriptor { return r.byName[name] }

// Len reports the number of registered processors.
func (r *Registry) Len() int { return len(r.byName) }

// Names returns processor names in deterministic order.
func (r *Registry) Names() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// MatchFastPath returns the first descriptor whose Match regex hits
// input, or nil. Iteration order is stable (alphabetical).
func (r *Registry) MatchFastPath(input string) *Descriptor {
	for _, n := range r.order {
		d := r.byName[n]
		if d.MatchesFastPath(input) {
			return d
		}
	}
	return nil
}
