// Package router asks the daemon which processor should handle a tool
// output. The routing call uses Gemma 4's native tool-call format
// (see https://ai.google.dev/gemma/docs/capabilities/text/function-calling-gemma4)
// constrained by a GBNF grammar, so the model's response is guaranteed
// to conform to the trained-for token pattern:
//
//	<|tool_call>call:route{processors:[<|"|>name<|"|>,...]}<tool_call|>
package router

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	inferd "github.com/3rg0n/inferd/clients/go"
	"github.com/3rg0n/thlibo/internal/inferdcli"
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
func Ask(ctx context.Context, client *inferdcli.Client, reg *processors.Registry, input string) (Decision, error) {
	names := reg.Names()
	if len(names) == 0 {
		return Decision{}, nil
	}

	// Sanitize before truncating: the marker breaks lie at exact
	// substring positions, so truncation after sanitize cannot split
	// a ZWSP-separated pair. See THREAT_MODEL.md finding #3.
	req := inferd.Request{
		ID:       "route",
		Messages: buildRoutingMessages(reg, truncate(promptsan.Sanitize(input), RouteInput)),
		Grammar:  buildGrammar(names),
	}
	// Low temperature for a classification task.
	t := 0.0
	req.Temperature = &t
	maxTok := 128
	req.MaxTokens = &maxTok
	s := false
	req.Stream = &s

	raw, err := client.Post(ctx, req)
	if err != nil {
		return Decision{}, err
	}
	return parseRoutingResponse(raw, reg), nil
}

// buildRoutingMessages constructs the system + user messages the
// daemon sees. The system content embeds a Gemma 4 tool declaration
// in the model's native format. The chat template in the GGUF will
// wrap this into `<|turn>system\n<|tool>declaration:...<tool|><turn|>`
// at tokenize time.
//
// We inject the declaration as plain text rather than relying on a
// separate `tools` parameter because the daemon protocol carries a
// messages array, and llamafile's /completion endpoint renders the
// chat template directly. The tool-call output is still grammar-
// enforced, so a misrendered declaration would just produce a valid
// empty chain (passthrough) rather than garbage.
func buildRoutingMessages(reg *processors.Registry, input string) []inferd.Message {
	var sysb strings.Builder
	sysb.WriteString(toolDeclaration(reg))
	sysb.WriteString("\n\nYou are a processor router. Given tool output, emit exactly one\n")
	sysb.WriteString("tool call to `route` with the ordered processor chain. Emit an\n")
	sysb.WriteString("empty processors array to pass the input through unchanged.\n\n")
	sysb.WriteString("Available processors:\n")
	for _, n := range reg.Names() {
		d := reg.Get(n)
		desc := strings.TrimSpace(d.Description)
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&sysb, "  - %s: %s\n", n, singleLine(desc))
	}

	return []inferd.Message{
		{Role: inferd.RoleSystem, Content: sysb.String()},
		{Role: inferd.RoleUser, Content: input},
	}
}

// toolDeclaration builds Gemma's tool-declaration block for the `route`
// tool. Format is the one documented in the function-calling
// capability doc; the chat template will wrap it with `<|tool>` tags.
func toolDeclaration(reg *processors.Registry) string {
	// The declaration is embedded as Gemma's declaration:name{...}
	// syntax. Quoting uses Gemma's `<|"|>` string delimiters.
	return `<|tool>declaration:route{description:<|"|>Route tool output through the processor chain. Pass an empty processors array to leave the input unchanged.<|"|>,parameters:{properties:{processors:{description:<|"|>Ordered list of processor names to run, piped stdout->stdin.<|"|>,type:<|"|>ARRAY<|"|>,items:{type:<|"|>STRING<|"|>} } },required:[<|"|>processors<|"|>],type:<|"|>OBJECT<|"|>} }<tool|>`
}

// buildGrammar produces a GBNF that forces Gemma's native tool-call
// output for `route`. The model is restricted to exactly one tool
// call whose `processors` argument is an array of registry names (or
// empty). The emitted tokens match the spec §Router tool-call format.
//
// Grammar shape (GBNF):
//
//	root       ::= "<|tool_call>call:route{processors:[" chain "]}<tool_call|>"
//	chain      ::= "" | name ("," name)*
//	name       ::= "<|\"|>" ("compress" | "casefolder" | ...) "<|\"|>"
//
// With the empty-registry case, chain is forced to "".
func buildGrammar(names []string) string {
	var b strings.Builder
	b.WriteString(`root ::= "<|tool_call>call:route{processors:[" chain "]}<tool_call|>"` + "\n")
	if len(names) == 0 {
		b.WriteString(`chain ::= ""`)
		return b.String()
	}
	b.WriteString(`chain ::= "" | name ("," name)*` + "\n")
	b.WriteString(`name ::= "<|\"|>" (`)
	for i, n := range names {
		if i > 0 {
			b.WriteString(" | ")
		}
		fmt.Fprintf(&b, "%q", n)
	}
	b.WriteString(`) "<|\"|>"`)
	return b.String()
}

// toolCallRE matches Gemma's native tool-call pattern for our `route`
// tool. We do not require a leading-bytes match because llamafile's
// response may include surrounding whitespace from the chat template.
var toolCallRE = regexp.MustCompile(
	`<\|tool_call>call:route\{processors:\[(.*?)\]\}<tool_call\|>`,
)

// argValueRE extracts one string argument from Gemma's `<|"|>...<|"|>`
// delimited form. The capability doc's own extract_tool_calls uses a
// similar pattern; we're less permissive here since we only expect
// processor-name strings.
var argValueRE = regexp.MustCompile(`<\|"\|>([^<]*)<\|"\|>`)

// ParseResult is returned from parseRoutingResponseDetailed. The
// Decision is what dispatch uses; Unknown/Malformed carry diagnostic
// info for the caller to log as a security-relevant event (an unknown
// name in a grammar-constrained response is a signal, not noise). See
// THREAT_MODEL.md finding #12.
type ParseResult struct {
	Decision  Decision
	Unknown   []string // names Gemma emitted that aren't in the registry
	Malformed bool     // true when the tool-call envelope itself was unreadable
}

// parseRoutingResponse preserves the (raw, reg) -> Decision signature
// for existing callers; see parseRoutingResponseDetailed for the
// diagnostics-rich form.
func parseRoutingResponse(raw string, reg *processors.Registry) Decision {
	return parseRoutingResponseDetailed(raw, reg).Decision
}

// parseRoutingResponseDetailed extracts the processor chain from
// Gemma's native tool-call output AND surfaces any unknown names or
// envelope parse failures. Any parse failure or unknown name produces
// a passthrough decision (B8c) - partial chains are worse than no
// run because they produce unexpected output shape.
func parseRoutingResponseDetailed(raw string, reg *processors.Registry) ParseResult {
	m := toolCallRE.FindStringSubmatch(raw)
	if m == nil {
		return ParseResult{Malformed: true} // B8c: no tool call -> passthrough
	}

	inner := strings.TrimSpace(m[1])
	if inner == "" {
		return ParseResult{} // Empty chain -> explicit passthrough
	}

	matches := argValueRE.FindAllStringSubmatch(inner, -1)
	if matches == nil {
		return ParseResult{Malformed: true}
	}

	valid := make([]string, 0, len(matches))
	var unknown []string
	for _, mm := range matches {
		name := strings.TrimSpace(mm[1])
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

// ClientAdapter wraps an inferdcli.Client so it satisfies the
// middleware's RouterClient interface without the middleware package
// importing router's package-level Ask function.
type ClientAdapter struct {
	Client *inferdcli.Client
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
	req := inferd.Request{
		ID:       "route",
		Messages: buildRoutingMessages(reg, truncate(promptsan.Sanitize(input), RouteInput)),
		Grammar:  buildGrammar(names),
	}
	t := 0.0
	req.Temperature = &t
	maxTok := 128
	req.MaxTokens = &maxTok
	s := false
	req.Stream = &s

	raw, err := a.Client.Post(ctx, req)
	if err != nil {
		return Decision{}, err
	}
	result := parseRoutingResponseDetailed(raw, reg)
	if len(result.Unknown) > 0 && a.OnUnknownProcessor != nil {
		a.OnUnknownProcessor(result.Unknown, raw)
	}
	return result.Decision, nil
}
