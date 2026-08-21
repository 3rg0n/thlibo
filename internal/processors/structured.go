package processors

import (
	"encoding/json"
	"strings"

	"gopkg.in/yaml.v3"
)

// StructuredDocument reports whether input is one complete structured
// configuration document — JSON, YAML, or TOML.
//
// Why the middleware needs this (#129): `compress` is a prompt processor
// whose mandatory output shape is a group-by-signature summary
// (sig= / level= / count= / sample= / keys= / tail=). It is also the
// router's declared general fallback, so a config file with no dedicated
// filter lands there and the model replaces the document with a
// description of it. The original bytes are gone. An agent that reads a
// config that way and then edits it corrupts the file. Measured on
// ~/.cursor/hooks.json (2610 bytes in, 962 out) and on this repo's
// .github/workflows/ci.yml.
//
// The check belongs after MatchFastPath, never before it: har-filter and
// ndjson-filter legitimately take JSON, and a whole-document parse must
// not shadow a filter whose `match` regex already fired.
//
// Conservative in one direction only. A false negative costs nothing new
// — the input reaches the router exactly as it does today. A false
// positive is the expensive mistake: it turns thlibo into a silent no-op
// for that input, which is the #106 failure class. So each detector below
// demands positive evidence of config shape rather than merely failing to
// disprove it, and structured_test.go asserts no false positive across
// every tool-output fixture in testdata/.
func StructuredDocument(input string) bool {
	if strings.TrimSpace(input) == "" {
		return false
	}
	return jsonDocument(input) || tomlDocument(input) || yamlDocument(input)
}

// jsonDocument reports whether input is one whole JSON object or array.
// The leading-byte test matters as much as the parse: a bare number or
// quoted string is valid JSON but is not a document, and requiring a
// container costs nothing since no config is a bare scalar. Tool output
// is never valid whole JSON, so this detector has no realistic false
// positive.
func jsonDocument(input string) bool {
	t := strings.TrimSpace(input)
	if t[0] != '{' && t[0] != '[' {
		return false
	}
	return json.Valid([]byte(t))
}

// tomlDocument reports whether every meaningful line of input is a TOML
// table header or key/value pair.
//
// This module has no TOML parser, and taking a dependency to answer a
// yes/no question is not worth it, so this classifies line shapes
// instead. Deliberately strict: one unclassifiable line disqualifies the
// whole document, which keeps the error on the false-negative side.
func tomlDocument(input string) bool {
	lines := strings.Split(input, "\n")
	tables, pairs, quoted := 0, 0, 0
	for i := 0; i < len(lines); i++ {
		ln := strings.TrimSpace(lines[i])
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		// [table] and [[array-of-tables]] headers close on their own line.
		if strings.HasPrefix(ln, "[") {
			if !strings.HasSuffix(ln, "]") {
				return false
			}
			tables++
			continue
		}
		eq := indexTOMLAssign(ln)
		if eq < 0 || strings.TrimSpace(ln[:eq]) == "" {
			return false
		}
		val := strings.TrimSpace(ln[eq+1:])
		if !tomlValueTyped(val) {
			return false
		}
		span, ok := tomlValueSpan(lines, i, val)
		if !ok {
			return false
		}
		i += span
		pairs++
		if val[0] == '"' || val[0] == '\'' || val[0] == '[' || val[0] == '{' {
			quoted++
		}
	}
	// A table header is TOML's own distinguishing syntax, so one is enough.
	// Without a header, demand two pairs and at least one string, array, or
	// inline-table value. `retries=3` / `timeout=30` is a plausible metrics
	// dump, and passing it through would be a silent no-op; no real TOML
	// config is entirely bare numbers.
	return tables > 0 || (pairs >= 2 && quoted > 0)
}

// tomlValueTyped reports whether val opens a valid TOML value. TOML has no
// bare-word values, which is what separates a config from generic
// key=value tool output: `level=info` and `PATH=/usr/bin` are not TOML,
// `hooks = true` and `model = "opus"` are. Without this check, `compress`'s
// own sig=/level=/count= output classifies as TOML.
func tomlValueTyped(val string) bool {
	if val == "" {
		return false
	}
	switch val {
	case "true", "false", "inf", "-inf", "+inf", "nan":
		return true
	}
	switch val[0] {
	case '"', '\'', '[', '{', '+', '-':
		return true
	}
	return val[0] >= '0' && val[0] <= '9' // number or datetime
}

// indexTOMLAssign returns the offset of the key/value `=` in ln, ignoring
// one inside a quoted key and stopping at a comment. Returns -1 when the
// line carries no assignment.
func indexTOMLAssign(ln string) int {
	var quote byte
	for i := 0; i < len(ln); i++ {
		switch c := ln[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '=':
			return i
		case c == '#':
			return -1
		}
	}
	return -1
}

// tomlValueSpan returns how many additional lines the value beginning on
// lines[i] consumes. TOML values span lines in three shapes: triple-
// quoted strings, bracketed arrays, and inline tables. A construct left
// open at end of input means this is not a TOML document.
func tomlValueSpan(lines []string, i int, val string) (int, bool) {
	switch {
	case strings.HasPrefix(val, `"""`):
		return spanUntilDelim(lines, i, val, `"""`)
	case strings.HasPrefix(val, `'''`):
		return spanUntilDelim(lines, i, val, `'''`)
	}
	depth := bracketDepth(val)
	if depth < 0 {
		return 0, false
	}
	if depth == 0 {
		return 0, true
	}
	for n := 1; i+n < len(lines); n++ {
		depth += bracketDepth(lines[i+n])
		if depth <= 0 {
			return n, true
		}
	}
	return 0, false
}

// spanUntilDelim handles a triple-quoted value, which may close on its
// own opening line.
func spanUntilDelim(lines []string, i int, first, delim string) (int, bool) {
	if strings.Contains(first[len(delim):], delim) {
		return 0, true
	}
	for n := 1; i+n < len(lines); n++ {
		if strings.Contains(lines[i+n], delim) {
			return n, true
		}
	}
	return 0, false
}

// bracketDepth returns the net bracket and brace depth of s, ignoring
// quoted spans and stopping at a comment.
func bracketDepth(s string) int {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '[', c == '{':
			depth++
		case c == ']', c == '}':
			depth--
		case c == '#':
			return depth
		}
	}
	return depth
}

// yamlDocument reports whether input is a YAML mapping that nests.
//
// YAML is a superset of plain text, which makes a bare "does it parse"
// test unusable here: one prose line containing a colon parses as a
// one-key mapping, and a run of `LEVEL: message` log lines parses as a
// several-key mapping. Requiring nesting is what separates the two. Real
// config nests — some value is itself a mapping or a sequence — and a run
// of log lines does not.
func yamlDocument(input string) bool {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(input), &doc); err != nil {
		return false
	}
	root := &doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) != 1 {
			return false
		}
		root = root.Content[0]
	}
	// Content holds alternating keys and values, so 4 entries is two pairs.
	if root.Kind != yaml.MappingNode || len(root.Content) < 4 {
		return false
	}
	for i := 1; i < len(root.Content); i += 2 {
		switch root.Content[i].Kind {
		case yaml.MappingNode, yaml.SequenceNode:
			return true
		}
	}
	return false
}
