package middleware

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// TestStructuredConfigSkipsRouter is the #129 regression test.
//
// `compress` is the router's declared general fallback and its mandatory
// output shape is a group-by-signature summary, so a config file with no
// dedicated filter used to come back as a description of itself with the
// original bytes discarded. Measured on ~/.cursor/hooks.json: 2610 bytes
// in, 962 out, and the result was not JSON. An agent that reads a config
// that way and then edits it corrupts the file.
//
// The assertions that matter are byte-exact passthrough and rt.called == 0.
func TestStructuredConfigSkipsRouter(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"claude settings.json", jsonConfigFixture()},
		{"codex config.toml", tomlConfigFixture()},
		{"github workflow yaml", yamlConfigFixture()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.raw) < MinBytesForRouting {
				t.Fatalf("fixture is %d bytes — under the short-circuit "+
					"threshold, so this test would pass for the wrong reason",
					len(tc.raw))
			}
			// A never-matching filter keeps the registry non-empty without
			// claiming the input. A matching one would be wrong here: the
			// fast path runs first by design, which
			// TestFastPathStillWinsOverStructuredSkip covers separately.
			reg := withScriptProcessor(t, "narrow-filter", `(?m)^never-matches-this\s`)
			rt := &fakeRouter{}
			p := newPipeline(reg, rt)

			out, inv := p.decide(context.Background(), tc.raw)

			if out != tc.raw {
				t.Errorf("config must pass through byte-exact: got %d bytes, want %d",
					len(out), len(tc.raw))
			}
			if rt.called != 0 {
				t.Errorf("router called %d times on a config document, want 0", rt.called)
			}
			if inv.Outcome != "passthrough" {
				t.Errorf("outcome = %q, want passthrough", inv.Outcome)
			}
		})
	}
}

// TestFastPathStillWinsOverStructuredSkip pins the ordering. har-filter and
// ndjson-filter legitimately take JSON, so the structured check runs after
// MatchFastPath and must never shadow a filter whose `match` regex fired.
// Without this, adding the #129 guard would silently disable them.
func TestFastPathStillWinsOverStructuredSkip(t *testing.T) {
	raw := jsonConfigFixture()
	if !strings.HasPrefix(raw, "{") {
		t.Fatalf("fixture is not a JSON object")
	}
	// Matches the fixture's opening brace, standing in for har-filter's
	// rooted JSON signature.
	reg := withScriptProcessor(t, "json-claimer", `(?m)^\{`)
	rt := &fakeRouter{}
	p := newPipeline(reg, rt)

	out, _ := p.decide(context.Background(), raw)

	if !strings.HasPrefix(out, "FILTERED:") {
		t.Errorf("fast-path filter did not run on whole-JSON input — the "+
			"structured skip shadowed it; got %.40q", out)
	}
	if rt.called != 0 {
		t.Errorf("router called %d times after a fast-path hit, want 0", rt.called)
	}
}

// TestOrdinaryLogStillReachesRouter is the control that keeps the guard
// honest: a false positive here would make thlibo a silent no-op, which is
// the #106 failure class. `compress`'s own output is the sharpest case —
// it is key=value shaped, so a looser TOML rule classified it as config.
func TestOrdinaryLogStillReachesRouter(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"plain log lines", strings.Repeat("ordinary log line, nothing structured\n", 200)},
		{"level prefixed log", strings.Repeat("INFO: served request in 12ms\nWARN: retrying\n", 100)},
		{"compress output", strings.Repeat("sig=some-shape\nlevel=info\ncount=3\n", 100)},
		{"env dump", strings.Repeat("PATH=/usr/bin:/bin\nHOME=/home/x\n", 100)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.raw) < MinBytesForRouting {
				t.Fatalf("fixture too small: %d", len(tc.raw))
			}
			reg := withScriptProcessor(t, "narrow-filter", `(?m)^never-matches-this\s`)
			rt := &fakeRouter{}
			p := newPipeline(reg, rt)

			if _, _ = p.decide(context.Background(), tc.raw); rt.called != 1 {
				t.Errorf("router called %d times, want 1 — the structured "+
					"guard swallowed ordinary tool output", rt.called)
			}
		})
	}
}

// jsonConfigFixture is shaped like ~/.claude/settings.json: a hook table
// with one group per matcher.
func jsonConfigFixture() string {
	var b strings.Builder
	b.WriteString("{\n  \"model\": \"opus\",\n  \"hooks\": {\n    \"PreToolUse\": [\n")
	for i := 0; i < 30; i++ {
		if i > 0 {
			b.WriteString(",\n")
		}
		b.WriteString("      {\"matcher\": \"Tool")
		b.WriteString(string(rune('A' + i%26)))
		b.WriteString("\", \"hooks\": [{\"type\": \"command\", ")
		b.WriteString("\"command\": \"C:/Users/x/.thlibo/hooks/thlibo-rewrite.ps1\"}]}")
	}
	b.WriteString("\n    ]\n  }\n}\n")
	return b.String()
}

// tomlConfigFixture is shaped like ~/.codex/config.toml, including the
// inline [[hooks.PostToolUse]] representation and a [hooks.state] table.
func tomlConfigFixture() string {
	var b strings.Builder
	b.WriteString("# codex config\nmodel = \"opus\"\napproval_policy = \"on-request\"\n\n")
	b.WriteString("[features]\nhooks = true\n\n")
	for i := 0; i < 30; i++ {
		b.WriteString("[[hooks.PostToolUse]]\n")
		b.WriteString("command = [\"thlibo\", \"compress\"]\n")
		b.WriteString("timeout_ms = 15000\n")
		b.WriteString("enabled = true\n\n")
	}
	return b.String()
}

// yamlConfigFixture is shaped like a GitHub Actions workflow: a mapping
// whose values nest, which is what separates config from log output.
func yamlConfigFixture() string {
	var b strings.Builder
	b.WriteString("name: CI\non:\n  push:\n    branches: [main]\njobs:\n")
	for i := 0; i < 30; i++ {
		// Distinct keys: yaml.v3 rejects a duplicate mapping key, which
		// would make the fixture unparseable rather than unstructured.
		b.WriteString("  job")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(":\n    runs-on: ubuntu-latest\n    steps:\n")
		b.WriteString("      - uses: actions/checkout@v5\n")
		b.WriteString("      - run: go test ./...\n")
	}
	return b.String()
}
