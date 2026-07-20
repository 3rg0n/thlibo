package middleware

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/3rg0n/thlibo/internal/processors"
	"github.com/3rg0n/thlibo/internal/telemetry"
)

// captureRecorder records the invocations it receives so tests can
// assert the middleware classified the run correctly. It is the
// in-memory stand-in for the OTel recorder.
type captureRecorder struct {
	got []telemetry.Invocation
}

func (c *captureRecorder) RecordInvocation(inv telemetry.Invocation) { c.got = append(c.got, inv) }
func (c *captureRecorder) Shutdown(context.Context) error            { return nil }

// TestTelemetryShortCircuit: a sub-threshold input records a
// short_circuit / passthrough invocation with exact byte counts and no
// processor.
func TestTelemetryShortCircuit(t *testing.T) {
	rec := &captureRecorder{}
	p := &Pipeline{Telemetry: rec, ToolName: "Bash"}

	in := "tiny" // < MinBytesForRouting
	var out bytes.Buffer
	if err := p.Process(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if len(rec.got) != 1 {
		t.Fatalf("want 1 recorded invocation, got %d", len(rec.got))
	}
	inv := rec.got[0]
	if inv.Path != telemetry.PathShortCircuit || inv.Outcome != telemetry.OutcomePassthrough {
		t.Errorf("path/outcome = %q/%q, want short_circuit/passthrough", inv.Path, inv.Outcome)
	}
	if inv.Tool != "Bash" {
		t.Errorf("tool = %q, want Bash", inv.Tool)
	}
	if inv.BytesIn != len(in) || inv.BytesOut != len(in) {
		t.Errorf("bytes in/out = %d/%d, want %d/%d", inv.BytesIn, inv.BytesOut, len(in), len(in))
	}
	if inv.Processor != "" {
		t.Errorf("short-circuit must have no processor, got %q", inv.Processor)
	}
}

// TestTelemetryNoProcessorsPassthrough: above threshold but empty
// registry → passthrough path recorded.
func TestTelemetryNoProcessorsPassthrough(t *testing.T) {
	rec := &captureRecorder{}
	reg, _, err := processors.BuildFromSources() // empty
	if err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{Registry: reg, Telemetry: rec, ToolName: "Read"}

	in := strings.Repeat("x", MinBytesForRouting+10)
	var out bytes.Buffer
	if err := p.Process(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if len(rec.got) != 1 {
		t.Fatalf("want 1 invocation, got %d", len(rec.got))
	}
	if rec.got[0].Path != telemetry.PathPassthrough {
		t.Errorf("path = %q, want passthrough", rec.got[0].Path)
	}
	if out.String() != in {
		t.Error("passthrough must return input verbatim")
	}
}

// TestTelemetryNilRecorderNoPanic: the disabled default (nil Telemetry)
// must not panic and must not change behaviour.
func TestTelemetryNilRecorderNoPanic(t *testing.T) {
	p := &Pipeline{} // Telemetry nil
	var out bytes.Buffer
	if err := p.Process(context.Background(), strings.NewReader("tiny"), &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "tiny" {
		t.Errorf("nil-recorder passthrough changed output: %q", out.String())
	}
}

// TestTelemetryNeverEmitsRawInput is the privacy guard: no recorded
// attribute value may contain the raw input bytes. The middleware emits
// only sizes, enums, and processor labels — never content.
func TestTelemetryNeverEmitsRawInput(t *testing.T) {
	rec := &captureRecorder{}
	p := &Pipeline{Telemetry: rec, ToolName: "Bash"}

	// A distinctive secret in the input; assert it never appears in any
	// recorded string field.
	secret := "SUPER-SECRET-TOKEN-abc123"
	in := secret + strings.Repeat(" filler", 500) // push over threshold
	var out bytes.Buffer
	if err := p.Process(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	for _, inv := range rec.got {
		for _, field := range []string{inv.Tool, inv.Path, inv.Outcome, inv.Processor, inv.Kind, inv.Fallback} {
			if strings.Contains(field, secret) {
				t.Fatalf("telemetry attribute leaked raw input: %q", field)
			}
		}
	}
}
