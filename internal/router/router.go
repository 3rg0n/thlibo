// Package router asks the daemon which processor should handle a tool
// output. Under the inferd v0.4 wire (ADR 0021) the routing call uses
// the structured tools mechanism (protocol-v2.md §3.6): the request
// carries a `route` tool definition and the model replies with a
// structured `tool_use` block whose `input` is parsed for the processor
// chain.
//
// Note: v2 removed the GBNF `grammar` field that previously *hard*-
// constrained Gemma's tool-call tokens, and the daemon does not enforce
// the tool's input_schema against emitted arguments (protocol-v2.md
// §3.6). So routing output is no longer guaranteed structurally — any
// malformed or absent tool_use, or an unknown processor name, produces
// a passthrough Decision (B8c). The lost hard guarantee's proper home
// is the daemon (constrained decoding), tracked separately; here we
// fail open.
package router

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/3rg0n/thlibo/internal/inferd"
	"github.com/3rg0n/thlibo/internal/processors"
	"github.com/3rg0n/thlibo/internal/promptsan"
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
func Ask(ctx context.Context, client *inferd.Client, reg *processors.Registry, input string) (Decision, error) {
	names := reg.Names()
	if len(names) == 0 {
		return Decision{}, nil
	}

	// Sanitize before truncating: the marker breaks lie at exact
	// substring positions, so truncation after sanitize cannot split
	// a ZWSP-separated pair. See THREAT_MODEL.md finding #3.
	req := buildRouteRequest(reg, truncate(promptsan.Sanitize(input), RouteInput))

	res, err := client.Post(ctx, req)
	if err != nil {
		return Decision{}, err
	}
	return parseRouteResult(res, reg).Decision, nil
}

// buildRouteRequest assembles the v0.5 routing request: the system+user
// messages plus a response_format JSON-Schema constraint
// (protocol-v2.md §3.2a). On a backend that supports structured output
// (llamacpp compiles the schema to GBNF) the model is *guaranteed* to
// emit JSON matching {"processors":[...]} — restoring the hard
// guarantee the removed v0.4 GBNF grammar gave, but daemon-side
// (ADR 0013). Backends without structured-output support ignore the
// field and return unconstrained text, which parseRouteResult treats as
// malformed -> passthrough (fail-open safety net). Low temperature for a
// deterministic classification task; non-streaming for one whole answer.
func buildRouteRequest(reg *processors.Registry, input string) inferd.Request {
	t := 0.0
	maxTok := 128
	s := false
	return inferd.Request{
		ID:             "route",
		Messages:       buildRoutingMessages(reg, input),
		ResponseFormat: inferd.JSONSchemaFormat(routeSchema(reg)),
		Temperature:    &t,
		MaxTokens:      &maxTok,
		Stream:         &s,
	}
}

// buildRoutingMessages constructs the system + user messages the daemon
// sees. Output is constrained to a JSON object via response_format
// (protocol-v2.md §3.2a), so the system message instructs the model to
// emit that object directly and lists the available processors.
func buildRoutingMessages(reg *processors.Registry, input string) []inferd.Message {
	var sysb strings.Builder
	sysb.WriteString("You are a processor router. Given tool output, reply with a JSON\n")
	sysb.WriteString("object {\"processors\": [...]} naming the ordered processor chain.\n")
	sysb.WriteString("Use an empty array to leave the input unchanged.\n\n")
	sysb.WriteString("Available processors:\n")
	for _, n := range reg.Names() {
		d := reg.Get(n)
		desc := strings.TrimSpace(d.Description)
		if desc == "" {
			desc = "(no description)"
		}
		sysb.WriteString("  - ")
		sysb.WriteString(n)
		sysb.WriteString(": ")
		sysb.WriteString(singleLine(desc))
		sysb.WriteString("\n")
	}

	return []inferd.Message{
		{Role: inferd.RoleSystem, Content: sysb.String()},
		{Role: inferd.RoleUser, Content: input},
	}
}

// routeSchema builds the JSON Schema the router constrains output to
// (protocol-v2.md §3.2a): an object with a single `processors` array of
// strings, enumerated to the registered names. On llamacpp this becomes
// a GBNF grammar, so the model's text output is guaranteed valid JSON
// matching this shape. parseRouteResult still validates names against
// the registry and falls open on any mismatch (defence in depth, and
// it covers backends that ignore response_format).
func routeSchema(reg *processors.Registry) json.RawMessage {
	names := reg.Names()
	enum, _ := json.Marshal(names)
	schema := `{"type":"object","properties":{"processors":{"type":"array","items":{"type":"string","enum":` +
		string(enum) +
		`}}},"required":["processors"],"additionalProperties":false}`
	return json.RawMessage(schema)
}

// ParseResult is returned from parseRouteResult. The Decision is what
// dispatch uses; Unknown/Malformed carry diagnostic info for the caller
// to log as a security-relevant event (an unknown name is a signal, not
// noise). See THREAT_MODEL.md finding #12.
type ParseResult struct {
	Decision  Decision
	Unknown   []string // names the model emitted that aren't in the registry
	Malformed bool     // true when the model's output wasn't usable JSON
}

// routeArgs is the parsed shape of the route response JSON.
type routeArgs struct {
	Processors []string `json:"processors"`
}

// parseRouteResult extracts the processor chain from the model's
// schema-constrained JSON text (protocol-v2.md §3.2a). On a
// structured-output backend the text is guaranteed to match routeSchema;
// on a backend that ignored response_format the text may be anything, so
// any unparseable output or unknown processor name produces a
// passthrough decision (B8c) — partial chains are worse than no run
// because they produce an unexpected output shape.
func parseRouteResult(res inferd.Result, reg *processors.Registry) ParseResult {
	text := strings.TrimSpace(res.Text)
	if text == "" {
		return ParseResult{Malformed: true}
	}

	var args routeArgs
	if err := json.Unmarshal([]byte(text), &args); err != nil {
		return ParseResult{Malformed: true}
	}
	if len(args.Processors) == 0 {
		return ParseResult{} // explicit empty chain -> passthrough
	}

	valid := make([]string, 0, len(args.Processors))
	var unknown []string
	for _, name := range args.Processors {
		name = strings.TrimSpace(name)
		if name == "" || reg.Get(name) == nil {
			unknown = append(unknown, name)
			continue
		}
		valid = append(valid, name)
	}
	if len(unknown) > 0 {
		// Any unknown name drops the whole chain (B8c).
		return ParseResult{Unknown: unknown}
	}
	return ParseResult{Decision: Decision{Chain: valid}}
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

// ClientAdapter wraps an inferd.Client so it satisfies the
// middleware's RouterClient interface without the middleware package
// importing router's package-level Ask function.
type ClientAdapter struct {
	Client *inferd.Client
	// OnUnknownProcessor, if set, is called when Gemma names a
	// processor that isn't in the registry. Used by the middleware
	// to log a security-relevant event; logging belongs to the
	// caller so this package stays import-light. See THREAT_MODEL.md
	// finding #12.
	OnUnknownProcessor func(names []string, rawResponse string)
}

// Ask implements middleware.RouterClient.
func (a *ClientAdapter) Ask(ctx context.Context, reg *processors.Registry, input string) (Decision, error) {
	return a.askDetailed(ctx, reg, input)
}

func (a *ClientAdapter) askDetailed(ctx context.Context, reg *processors.Registry, input string) (Decision, error) {
	names := reg.Names()
	if len(names) == 0 {
		return Decision{}, nil
	}
	req := buildRouteRequest(reg, truncate(promptsan.Sanitize(input), RouteInput))

	res, err := a.Client.Post(ctx, req)
	if err != nil {
		return Decision{}, err
	}
	result := parseRouteResult(res, reg)
	if len(result.Unknown) > 0 && a.OnUnknownProcessor != nil {
		a.OnUnknownProcessor(result.Unknown, res.Text)
	}
	return result.Decision, nil
}
