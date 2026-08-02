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

// fingerprintEntry records (size, mtime, mode) for a script entry
// file so the dispatcher can detect swaps between load and dispatch.
// Returns zero-value fingerprint on any stat error; dispatch treats
// a zero fingerprint as "not recorded, skip the check".
func fingerprintEntry(dir, entry string) EntryFingerprint {
	if dir == "" || entry == "" {
		return EntryFingerprint{}
	}
	info, err := os.Stat(filepath.Join(dir, entry))
	if err != nil {
		return EntryFingerprint{}
	}
	return EntryFingerprint{
		Size:  info.Size(),
		ModNs: info.ModTime().UnixNano(),
		Mode:  uint32(info.Mode().Perm()),
	}
}

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

// ShadowWarning is returned as part of the Build warnings set when a
// user-installed processor overrides a built-in of the same name. It
// carries the structured fields so callers can log or display it
// without parsing a formatted string. See THREAT_MODEL.md finding #7.
type ShadowWarning struct {
	Name        string
	BuiltinPath string
	UserPath    string
}

func (w *ShadowWarning) Error() string {
	return fmt.Sprintf("processors: user processor %q shadows built-in (built-in: %s, user: %s)",
		w.Name, w.BuiltinPath, w.UserPath)
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
			// Capture (size, mtime, mode) of the script entry so
			// the dispatcher can detect swaps between load and
			// dispatch — best-effort TOCTOU guard per finding #9.
			if d.Type == KindScript {
				d.EntryFingerprint = fingerprintEntry(d.Origin.DiskDir, d.Entry)
			}
		}
		// User wins: overwrite any existing entry unconditionally when
		// origin is User. When origin is Builtin, only add if not
		// already present (shouldn't happen in practice since scan is
		// called builtin-first). When a user processor shadows a
		// built-in, emit a ShadowWarning so the middleware can surface
		// the silent capability swap (T13 / BV-2).
		if s.Origin == OriginUser {
			if prev, ok := r.byName[d.Name]; ok && prev.Origin.Source == OriginBuiltin {
				warnings = append(warnings, &ShadowWarning{
					Name:        d.Name,
					BuiltinPath: prev.Origin.Path,
					UserPath:    d.Origin.Path,
				})
			}
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

// RoutableNames returns the names the routing model should choose from,
// in the same deterministic order as Names. Processors already covered by
// a fast-path regex, and those explicitly marked `routable: false`, are
// omitted — they remain reachable by fast-path match, explicit chain, or
// hardwired dispatch, but advertising them to the model costs prompt
// tokens for a decision it will never usefully make. See
// Descriptor.RouterEligible.
func (r *Registry) RoutableNames() []string {
	out := make([]string, 0, len(r.order))
	for _, n := range r.order {
		if d := r.byName[n]; d != nil && d.RouterEligible() {
			out = append(out, n)
		}
	}
	return out
}

// MatchFastPath returns the best descriptor whose Match regex hits
// input, or nil.
//
// Precedence, strongest evidence first (ADR 0014, #97):
//
//  1. **Format signatures** — a rooted Match that can only fire at the
//     start of the input (pdf-to-md's `^%PDF-`). This says what the input
//     *is*, so it outranks everything.
//  2. **Line shapes**, script/native — an unrooted Match found anywhere.
//     Deterministic and fast, so still preferred over prompt processors
//     (ADR 0010: native behaves like script for routing).
//  3. **Line shapes**, prompt — broad substrings (`(?i)traceback|fatal`)
//     that most easily fire by accident.
//
// Tiebreak within a tier is stable alphabetical (r.order), except that
// tier 1 prefers a native filter over a script one. Two processors can
// legitimately claim the same format: `pdf-filter` (native, in-process)
// and `pdf-to-md` (Python script) both declare `^%PDF-`, and which wins
// must not depend on how the folders happen to sort — renaming either
// would silently swap the engine. Native wins because it needs no
// interpreter and no installed dependencies, so it can't fail for
// environmental reasons the caller can't see. This is derived from the
// declared type, not a priority field (ADR 0014).
//
// Why tier 1 exists: before it, precedence was "first alphabetical
// script/native hit wins", so `go-test-filter` — whose
// `^(?:ok|FAIL|\?)\s+\S+\s` matches by coincidence inside a compressed
// PDF stream — beat `pdf-to-md` on every PDF containing such a line,
// purely because "g" sorts before "p". A 6.9 MB paper came out as 6.9 MB
// of go-test-filtered PDF instead of 88 KB of markdown (#97).
//
// Binary input is guarded separately: see binaryLooking, which drops
// line-shape filters entirely for inputs that aren't text. Signatures are
// exempt from that guard, since a format signature on a binary container
// is exactly the right match.
func (r *Registry) MatchFastPath(input string) *Descriptor {
	binary := binaryLooking(input)

	var sigNative, sigOther, lineScript, linePrompt *Descriptor
	for _, n := range r.order {
		d := r.byName[n]
		// Tiers 2-3 are line shapes. On binary input they are all
		// coincidence — a text filter has no business rewriting a
		// container — so skip them without even running the regex, which
		// on a multi-megabyte container is the expensive part.
		if binary && !d.MatchIsSignature() {
			continue
		}
		if !d.MatchesFastPath(input) {
			continue
		}
		// Tier 1: a rooted signature is decisive against every line
		// shape, so a hit here ends the tier-2/3 search. Within the tier,
		// a native filter beats a script one; r.order is stable, so the
		// first hit of each kind is a deterministic winner. We can't
		// return immediately: a script signature must still lose to a
		// native one later in the order.
		if d.MatchIsSignature() {
			if d.Type == KindNative {
				if sigNative == nil {
					sigNative = d
				}
				continue
			}
			if sigOther == nil {
				sigOther = d
			}
			continue
		}
		if d.Type == KindScript || d.Type == KindNative {
			if lineScript == nil {
				lineScript = d
			}
			continue
		}
		if linePrompt == nil {
			linePrompt = d
		}
	}
	if sigNative != nil {
		return sigNative
	}
	if sigOther != nil {
		return sigOther
	}
	if lineScript != nil {
		return lineScript
	}
	return linePrompt
}

// BinaryLooking reports whether input should be treated as a binary
// container rather than text. Exported so the middleware can skip the
// routing call for input no text processor should touch: dropping the
// line-shape filters (see MatchFastPath) leaves such input with no
// fast-path match, and falling through to the router would sample the
// container's bytes and spend an inference round-trip to be told
// "none" — worse than the local no-op it replaced, since those bytes
// then leave the process. A signature match still wins first, so
// pdf-to-md is unaffected.
func BinaryLooking(input string) bool { return binaryLooking(input) }

// binaryBySniffLen is how far into the input binaryLooking looks for a NUL
// byte. 8 KiB matches internal/casefile's sniffer, which uses the same
// test for the same reason.
const binaryBySniffLen = 8192

// binaryLooking reports whether input is a binary container rather than
// text. A NUL byte in the first 8 KiB is the classic test: every binary
// format thlibo handles (PDF, zip-based archives, MHTML/OOXML) trips it
// immediately, and valid UTF-8 text never contains one.
//
// This exists because rooted signatures alone don't cover the whole
// problem (#97). A PDF at least *has* a signature processor to claim it;
// a .zip or .tar.gz has none, yet both false-match go-test-filter's line
// shape the same way a PDF does — verified against this repo's own release
// artifacts. Without this guard those inputs get silently line-filtered.
//
// Cheap and conservative in the safe direction: a false "text" verdict
// only restores the previous behaviour, and a false "binary" verdict costs
// a fast-path dispatch that would have been operating on non-text anyway.
func binaryLooking(input string) bool {
	head := input
	if len(head) > binaryBySniffLen {
		head = head[:binaryBySniffLen]
	}
	return strings.IndexByte(head, 0x00) >= 0
}

// MatchCommand returns the first descriptor whose Commands list
// contains argv0. Used by `thlibo rewrite` to decide whether a shell
// command should be wrapped. Iteration order is stable (alphabetical)
// so a deterministic winner is picked if multiple processors declare
// the same command (first one loaded wins; user processors beat
// built-ins because they override by name).
func (r *Registry) MatchCommand(argv0 string) *Descriptor {
	if argv0 == "" {
		return nil
	}
	for _, n := range r.order {
		d := r.byName[n]
		for _, c := range d.Commands {
			if c == argv0 {
				return d
			}
		}
	}
	return nil
}

// MatchCommandLine resolves a processor for a full command token slice
// (e.g. ["go","test","-v","./..."]). It first tries CommandPrefixes
// (multi-token, leading-token match — so "go test" wraps but "go build"
// does not), then falls back to exact argv[0] via MatchCommand. The
// prefix check runs first because it is the more specific signal.
//
// tokens should be the command split on whitespace with argv[0] already
// basename-normalised by the caller (so "/usr/bin/go test" arrives as
// ["go","test",...]). A nil/empty slice returns nil.
func (r *Registry) MatchCommandLine(tokens []string) *Descriptor {
	if len(tokens) == 0 {
		return nil
	}
	for _, n := range r.order {
		d := r.byName[n]
		for _, p := range d.CommandPrefixes {
			pt := strings.Fields(p)
			if len(pt) == 0 || len(pt) > len(tokens) {
				continue
			}
			matched := true
			for i, want := range pt {
				if tokens[i] != want {
					matched = false
					break
				}
			}
			if matched {
				return d
			}
		}
	}
	// No prefix matched — fall back to exact argv[0].
	return r.MatchCommand(tokens[0])
}
