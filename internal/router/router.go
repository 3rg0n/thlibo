// Package router asks the daemon which processor should handle a tool
// output. The routing call uses a GBNF grammar so the model's response
// is guaranteed to be valid JSON matching a known schema, analogous to
// how the Anthropic API enforces tool_use input_schema.
package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/3rg0n/thlibo/internal/ipc"
	"github.com/3rg0n/thlibo/internal/processors"
)

// Decision is what the router returns to the dispatcher. Chain is
// non-empty for processor chains; the dispatcher runs them in order,
// piping each processor's stdout into the next's stdin.
type Decision struct {
	Chain []string // ordered processor names; empty = pass through
}

// Passthrough reports whether no processor should run.
func (d Decision) Passthrough() bool { return len(d.Chain) == 0 }

// RouteInput is the size-limited view of tool output that goes into
// the router prompt. 200 chars is plenty for the model to identify
// the output's shape and keeps the routing call fast.
const RouteInput = 200

// Ask sends input + registry description to the daemon and returns a
// Decision. Unknown processor names in the response are filtered out
// (B8c: fallback to original input on unknown name).
//
// Any error from the daemon (B8a/B8b) or an empty chain produces a
// Passthrough decision so callers can uniformly treat "route failed"
// and "route said none" the same way.
func Ask(ctx context.Context, client *DaemonClient, reg *processors.Registry, input string) (Decision, error) {
	names := reg.Names()
	if len(names) == 0 {
		return Decision{}, nil
	}

	req := ipc.Request{
		ID:       "route",
		Messages: buildRoutingMessages(reg, truncate(input, RouteInput)),
		Grammar:  buildGrammar(names),
	}
	// Low temperature for a classification task.
	t := 0.0
	req.Temperature = &t
	maxTok := 128
	req.MaxTokens = &maxTok
	s := false
	req.Stream = &s

	raw, _, err := client.Post(ctx, req)
	if err != nil {
		return Decision{}, err
	}
	return parseRoutingResponse(raw, reg), nil
}

// buildRoutingMessages constructs the system + user messages the
// daemon sees. The system prompt spells out the JSON schema and gives
// a small number of few-shot examples so even without the grammar the
// output would be close to valid (belt-and-braces with the grammar).
func buildRoutingMessages(reg *processors.Registry, input string) []ipc.Message {
	var sysb strings.Builder
	sysb.WriteString("You are a processor router. Given tool output, return JSON\n")
	sysb.WriteString("describing which processors should run. Return only JSON, no prose.\n\n")
	sysb.WriteString("Schema:\n")
	sysb.WriteString(`  {"chain": ["processor-name", ...]}` + "\n")
	sysb.WriteString(`  An empty chain ({"chain": []}) means no processor should run.` + "\n\n")
	sysb.WriteString("Available processors:\n")
	for _, n := range reg.Names() {
		d := reg.Get(n)
		desc := strings.TrimSpace(d.Description)
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&sysb, "  - %s: %s\n", n, singleLine(desc))
	}
	sysb.WriteString("\nExamples:\n")
	sysb.WriteString(`  Input: "On branch main\nnothing to commit"` + "\n")
	sysb.WriteString(`  Output: {"chain":["git-filter"]}` + "\n\n")
	sysb.WriteString(`  Input: "compile error at src/main.rs:12: mismatched types"` + "\n")
	sysb.WriteString(`  Output: {"chain":["casefolder"]}` + "\n\n")
	sysb.WriteString(`  Input: "hello world"` + "\n")
	sysb.WriteString(`  Output: {"chain":[]}` + "\n")

	return []ipc.Message{
		{Role: ipc.RoleSystem, Content: sysb.String()},
		{Role: ipc.RoleUser, Content: input},
	}
}

// buildGrammar produces a GBNF that constrains the model's output to
// exactly {"chain":[<zero-or-more-processor-names>]}. llama.cpp
// enforces this token-by-token; no other tokens are legal.
//
// Grammar shape:
//
//	root       ::= "{\"chain\":[" names? "]}"
//	names      ::= name ("," name)*
//	name       ::= "\"" ("compress" | "casefolder" | ...) "\""
//
// Processor names are matched against the registry; only registry
// entries appear in the grammar, so the model physically cannot emit
// an unknown name.
func buildGrammar(names []string) string {
	if len(names) == 0 {
		// No processors -> grammar forces empty chain, which drives
		// passthrough. Matches spec's "none" decision.
		return `root ::= "{\"chain\":[]}"`
	}
	var b strings.Builder
	b.WriteString(`root ::= "{\"chain\":[" chain "]}"` + "\n")
	b.WriteString(`chain ::= name ("," name)* | ""` + "\n")
	b.WriteString(`name ::= "\"" (`)
	for i, n := range names {
		if i > 0 {
			b.WriteString(" | ")
		}
		fmt.Fprintf(&b, "%q", n)
	}
	b.WriteString(`) "\""`)
	return b.String()
}

// parseRoutingResponse is defensive: even with the grammar constraint
// we strip whitespace and fenced-code markers before decoding, because
// the model's first tokens before the grammar kicks in could, in
// edge cases, be spaces or something benign. Any decode failure or
// unknown name produces a passthrough decision (B8c).
func parseRoutingResponse(raw string, reg *processors.Registry) Decision {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	// Locate the first brace, in case there's a leading preamble.
	if i := strings.Index(raw, "{"); i > 0 {
		raw = raw[i:]
	}

	var payload struct {
		Chain []string `json:"chain"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return Decision{} // B8c: malformed -> passthrough
	}

	valid := make([]string, 0, len(payload.Chain))
	for _, name := range payload.Chain {
		name = strings.TrimSpace(name)
		if name == "" || reg.Get(name) == nil {
			// Unknown name -> drop the whole chain (B8c). A partial
			// run is worse than passthrough because it produces
			// unexpected output shape.
			return Decision{}
		}
		valid = append(valid, name)
	}
	return Decision{Chain: valid}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func singleLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.Join(strings.Fields(s), " ")
}

// ErrDaemonUnreachable is returned when the daemon dial fails. Kept
// here for tests that want to distinguish daemon-down from other
// errors; Ask already converts failures to passthrough at the caller
// level.
var ErrDaemonUnreachable = errors.New("router: daemon unreachable")

// ClientAdapter wraps a DaemonClient so it satisfies the middleware's
// RouterClient interface without the middleware package importing
// router's package-level Ask function.
type ClientAdapter struct {
	Client *DaemonClient
}

// Ask implements middleware.RouterClient.
func (a *ClientAdapter) Ask(ctx context.Context, reg *processors.Registry, input string) (Decision, error) {
	return Ask(ctx, a.Client, reg, input)
}
