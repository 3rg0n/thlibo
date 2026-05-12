package ipc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func ptrF(v float64) *float64 { return &v }
func ptrI(v int) *int         { return &v }
func ptrB(v bool) *bool       { return &v }

// A6: sampling defaults are applied when caller omits the field.
func TestResolveAppliesDefaults(t *testing.T) {
	req := Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}
	got, err := req.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Temperature != DefaultTemperature {
		t.Errorf("Temperature = %v, want %v", got.Temperature, DefaultTemperature)
	}
	if got.TopP != DefaultTopP {
		t.Errorf("TopP = %v, want %v", got.TopP, DefaultTopP)
	}
	if got.TopK != DefaultTopK {
		t.Errorf("TopK = %v, want %v", got.TopK, DefaultTopK)
	}
	if got.MaxTokens != DefaultMaxTokens {
		t.Errorf("MaxTokens = %v, want %v", got.MaxTokens, DefaultMaxTokens)
	}
	if !got.Stream {
		t.Errorf("Stream default should be true")
	}
	if got.ImageTokenBudget != 0 {
		t.Errorf("ImageTokenBudget = %v, want 0 (not multimodal)", got.ImageTokenBudget)
	}
}

// A6: caller-supplied values override defaults, including zero values.
func TestResolveOverrides(t *testing.T) {
	req := Request{
		Messages:    []Message{{Role: RoleUser, Content: "hi"}},
		Temperature: ptrF(0.0),
		TopP:        ptrF(0.5),
		TopK:        ptrI(10),
		MaxTokens:   ptrI(42),
		Stream:      ptrB(false),
	}
	got, err := req.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Temperature != 0.0 || got.TopP != 0.5 || got.TopK != 10 || got.MaxTokens != 42 || got.Stream != false {
		t.Errorf("overrides not applied: %+v", got)
	}
}

// A7: image_token_budget must be exactly one of the documented values.
func TestResolveImageTokenBudget(t *testing.T) {
	for _, v := range ValidImageTokenBudgets {
		req := Request{
			Messages:         []Message{{Role: RoleUser, Content: "hi"}},
			ImageTokenBudget: ptrI(v),
		}
		if _, err := req.Resolve(); err != nil {
			t.Errorf("budget %d rejected: %v", v, err)
		}
	}
	for _, bad := range []int{0, 1, 69, 100, 281, 1121, -140} {
		req := Request{
			Messages:         []Message{{Role: RoleUser, Content: "hi"}},
			ImageTokenBudget: ptrI(bad),
		}
		if _, err := req.Resolve(); err == nil {
			t.Errorf("budget %d should have been rejected", bad)
		}
	}
}

func TestResolveRejectsEmptyMessages(t *testing.T) {
	if _, err := (&Request{}).Resolve(); err == nil {
		t.Error("empty messages should be rejected")
	}
}

func TestResolveRejectsBadRole(t *testing.T) {
	req := Request{Messages: []Message{{Role: "hacker", Content: "x"}}}
	if _, err := req.Resolve(); err == nil {
		t.Error("bad role should be rejected")
	}
}

// A5: frame round-trip. Encode every documented response type, decode,
// compare. Also verifies newline termination and that frames can be read
// one-per-line from a bufio.Reader.
func TestFrameRoundTrip(t *testing.T) {
	frames := []Response{
		{ID: "req-1", Type: ResponseStatus, Status: "loading_model"},
		{ID: "req-1", Type: ResponseStatus, Status: "ready"},
		{ID: "req-1", Type: ResponseToken, Content: "Hello"},
		{ID: "req-1", Type: ResponseToken, Content: " world"},
		{ID: "req-1", Type: ResponseDone, Usage: &Usage{PromptTokens: 12, CompletionTokens: 3}},
		{ID: "req-2", Type: ResponseError, Message: "queue full"},
		{ID: AdminID, Type: ResponseStatus, Status: "ready"},
	}

	var buf bytes.Buffer
	for _, f := range frames {
		if err := WriteFrame(&buf, f); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
	}

	// Every frame must end in exactly one \n and contain no embedded \n.
	raw := buf.String()
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	if len(lines) != len(frames) {
		t.Fatalf("got %d lines, want %d", len(lines), len(frames))
	}

	r := bufio.NewReader(&buf)
	for i, want := range frames {
		line, err := r.ReadBytes('\n')
		if err != nil && err != io.EOF {
			t.Fatalf("frame %d read: %v", i, err)
		}
		var got Response
		if err := json.Unmarshal(line, &got); err != nil {
			t.Fatalf("frame %d decode: %v (%q)", i, err, line)
		}
		if got != want && !equalResponse(got, want) {
			t.Errorf("frame %d: got %+v, want %+v", i, got, want)
		}
	}
}

func equalResponse(a, b Response) bool {
	if a.ID != b.ID || a.Type != b.Type || a.Status != b.Status || a.Content != b.Content || a.Message != b.Message {
		return false
	}
	switch {
	case a.Usage == nil && b.Usage == nil:
		return true
	case a.Usage == nil || b.Usage == nil:
		return false
	default:
		return *a.Usage == *b.Usage
	}
}

// A5: request decoder accepts the documented shape, including the new `id`.
func TestReadRequest(t *testing.T) {
	wire := `{"id":"req-7f3a","messages":[{"role":"system","content":"S"},{"role":"user","content":"U"}],"temperature":1.0,"top_p":0.95,"top_k":64,"max_tokens":1000,"stream":true,"image_token_budget":280}` + "\n"
	r := bufio.NewReader(strings.NewReader(wire))
	got, err := ReadRequest(r)
	if err != nil {
		t.Fatalf("ReadRequest: %v", err)
	}
	if got.ID != "req-7f3a" {
		t.Errorf("ID = %q", got.ID)
	}
	if len(got.Messages) != 2 || got.Messages[0].Role != RoleSystem || got.Messages[1].Role != RoleUser {
		t.Errorf("messages wrong: %+v", got.Messages)
	}
	if got.ImageTokenBudget == nil || *got.ImageTokenBudget != 280 {
		t.Errorf("ImageTokenBudget not decoded")
	}
}

func TestReadRequestRejectsGarbage(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("not json\n"))
	if _, err := ReadRequest(r); err == nil {
		t.Error("garbage should produce a decode error")
	}
}
