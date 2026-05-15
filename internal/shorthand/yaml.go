package shorthand

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// IsYAMLContent reports whether s looks like YAML. Heuristic: starts
// with `---\n` (frontmatter / explicit doc), OR the first non-blank
// non-comment line matches a top-level key pattern. Used to gate
// the YAML-aware path — false negatives are fine (we just compress
// flat prose), false positives would feed structural data through
// a prose compressor.
func IsYAMLContent(s string) bool {
	if strings.HasPrefix(s, "---\n") || strings.HasPrefix(s, "---\r\n") {
		return true
	}
	for _, line := range strings.SplitN(s, "\n", 30) {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		// Top-level key heuristic: starts at column 0, has `key: `
		// somewhere before any structural punctuation.
		if line == trim && strings.Contains(line, ": ") {
			return true
		}
		// Bare top-level key (`key:` with nothing after).
		if line == trim && strings.HasSuffix(line, ":") && !strings.Contains(line, " ") {
			return true
		}
		return false
	}
	return false
}

// CompressYAML walks a YAML document and runs e.Backend on each
// scalar value that's prose-shaped (long enough, free-form, not a
// list element of a structural key like `allowed_tools`). Keys,
// short scalars, lists, and code blocks pass through untouched.
//
// Round-trip preserves structure: yaml.v3 emits the same key order,
// indentation, and node styles for everything except the scalars
// we rewrote.
//
// Eval gate runs per-scalar: if any rewrite drops a load-bearing
// token, that scalar reverts. The whole-document Compressed bytes
// only contain rewrites where every per-scalar eval passed.
//
// Result.AlreadyShorthand is true iff zero scalars were eligible
// for rewriting (no long prose anywhere).
func (e *Engine) CompressYAML(ctx context.Context, input string) (*Result, error) {
	if e.Backend == nil {
		return nil, ErrBackendUnavailable
	}

	r := &Result{Original: input, Compressed: input}

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(input), &doc); err != nil {
		return r, fmt.Errorf("shorthand: parse YAML: %w", err)
	}

	rewrites := 0
	failures := []string{}

	walkYAML(&doc, "", func(node *yaml.Node, path string) {
		if !isProseScalar(node, path) {
			return
		}
		original := node.Value

		out, err := e.Backend.Run(ctx, original)
		if err != nil {
			// Backend failure on one scalar shouldn't kill the
			// whole doc; record and move on. The result still
			// reflects every successful rewrite.
			failures = append(failures, fmt.Sprintf("%s: backend error: %v", path, err))
			return
		}

		out = strings.TrimSpace(out)

		// Already-shorthand sentinel — leave the value alone, count
		// it as a no-op rather than a failure.
		if firstLine := strings.SplitN(out, "\n", 2)[0]; strings.TrimSpace(firstLine) == alreadyShorthandSentinel {
			return
		}

		// Per-scalar eval. If the compressed value drops any
		// load-bearing token, keep the original.
		evalFailures := Evaluate(original, out)
		if len(evalFailures) > 0 {
			for _, f := range evalFailures {
				failures = append(failures, fmt.Sprintf("%s: %s", path, f))
			}
			return
		}

		node.Value = out
		// Promote single-line prose to plain style; keep `>` / `|`
		// blocks in their existing style so the output stays
		// readable.
		if node.Style == 0 && !strings.Contains(out, "\n") {
			node.Style = 0 // plain
		}
		rewrites++
	})

	if rewrites == 0 {
		r.AlreadyShorthand = true
		return r, nil
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return r, fmt.Errorf("shorthand: emit YAML: %w", err)
	}
	r.Compressed = string(out)

	// Doc-level eval: every directive/code fence/URL/etc. that was
	// in the original document must still be in the rewritten one.
	// This catches the case where one rewrite passed its per-scalar
	// eval but accidentally dropped a token we only knew was
	// load-bearing in the document context.
	docFailures := Evaluate(input, r.Compressed)
	r.EvalFailures = append(failures, docFailures...)

	if !r.Safe() {
		// Doc-level failure → revert wholesale. The user gets their
		// bytes back, same fail-closed contract as the prose path.
		r.Compressed = input
		return r, nil
	}

	if len(input) > 0 {
		r.ReductionPercent = (1.0 - float64(len(r.Compressed))/float64(len(input))) * 100
	}
	return r, nil
}

// isProseScalar reports whether a YAML node is a prose-shaped value
// worth running through the compressor. Rules:
//
//   - Must be a scalar node.
//   - Must be a value, not a key (walkYAML emits the path of the
//     parent key, so we know whether this node is on a key->value
//     edge or is itself a key).
//   - Style `|` (literal block) or `>` (folded block) is always
//     prose.
//   - Plain or quoted scalars must be ≥80 chars AND contain at
//     least one space (rules out tokens like `cisco.thlibo.daemon`,
//     paths, regex patterns, version strings).
//   - Path must NOT match the structural-key blocklist
//     (`allowed_tools`, `name`, `version`, `model`, `type`,
//     `match`, `commands`) — these are identifiers / lists where
//     even long values are load-bearing identifier text.
func isProseScalar(node *yaml.Node, path string) bool {
	if node.Kind != yaml.ScalarNode {
		return false
	}
	if path == "" {
		// Root scalar — unusual; safer to skip.
		return false
	}
	// Structural-key blocklist: even if the value is long, never
	// rewrite. Match the trailing key segment of the path so
	// `allowed_tools[0]` and `allowed_tools` both hit.
	last := lastPathSegment(path)
	switch last {
	case "name", "version", "model", "type", "match",
		"allowed_tools", "allowed-tools", "allowedTools",
		"commands", "command", "entry", "tools",
		"id", "uuid", "sha", "hash":
		return false
	}

	// Block-scalar style — always prose.
	if node.Style == yaml.LiteralStyle || node.Style == yaml.FoldedStyle {
		return len(node.Value) >= 80
	}

	// Plain / single-quoted / double-quoted: only treat as prose
	// when long AND multi-word. 120-char threshold matches the
	// shortest prose scalars in real-world prompts/*.yaml files;
	// shorter values are usually identifiers, paths, or short
	// labels that aren't worth the round-trip even when they
	// contain spaces (e.g. "code-review coordinator").
	if len(node.Value) < 120 {
		return false
	}
	// Multi-word check — at least 4 spaces, so phrases like
	// "Bash(git diff *)" don't qualify.
	if strings.Count(node.Value, " ") < 4 {
		return false
	}
	return true
}

// walkYAML walks every value-position scalar in doc. The visit
// callback is given the scalar node and a dot-joined path
// (parent_key.child_key, or parent_key[idx] for sequence elements).
// Used by CompressYAML to identify rewrite candidates.
func walkYAML(node *yaml.Node, path string, visit func(*yaml.Node, string)) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			walkYAML(child, path, visit)
		}
	case yaml.MappingNode:
		// Mapping nodes alternate key, value, key, value, ...
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valNode := node.Content[i+1]
			childPath := keyNode.Value
			if path != "" {
				childPath = path + "." + childPath
			}
			walkYAML(valNode, childPath, visit)
		}
	case yaml.SequenceNode:
		for idx, child := range node.Content {
			childPath := fmt.Sprintf("%s[%d]", path, idx)
			walkYAML(child, childPath, visit)
		}
	case yaml.ScalarNode:
		visit(node, path)
	case yaml.AliasNode:
		// Aliases reference an anchor; rewriting them would change
		// other parts of the doc. Skip.
		return
	}
}

func lastPathSegment(p string) string {
	if i := strings.LastIndex(p, "."); i >= 0 {
		p = p[i+1:]
	}
	if i := strings.Index(p, "["); i >= 0 {
		p = p[:i]
	}
	return p
}
