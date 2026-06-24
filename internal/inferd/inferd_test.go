package inferd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
)

// newTestClient returns a Client whose Post uses the given
// pre-connected conn instead of dialing. Test seam.
func newTestClient(conn net.Conn) *Client {
	return &Client{
		Address: "test",
		dialFunc: func(context.Context) (net.Conn, error) {
			return conn, nil
		},
	}
}

// errString is a tiny error constructor for table tests.
func errString(s string) error { return stringError(s) }

type stringError string

func (e stringError) Error() string { return string(e) }

// readOneRequestFrame reads a single length-prefixed JSON frame from r
// and returns the decoded generic map. Used by the fake daemon to
// inspect what the client wrote.
func readOneRequestFrame(t *testing.T, r *bufio.Reader) map[string]any {
	t.Helper()
	ftype, payload, err := readFrame(r)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if ftype != frameJSON {
		t.Fatalf("frame type = 0x%02x, want JSON", ftype)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	return m
}

// fakeDaemon serves one request on an in-memory net.Pipe, runs handler
// against the parsed request, and writes the handler's response frames.
// Returns the client side of the pipe wired into a Client.
func fakeDaemon(t *testing.T, handler func(req map[string]any, w io.Writer)) *Client {
	t.Helper()
	srv, cli := net.Pipe()
	go func() {
		defer func() { _ = srv.Close() }()
		r := bufio.NewReader(srv)
		req := readOneRequestFrame(t, r)
		handler(req, srv)
	}()
	// A Client whose Post dials by returning this pre-connected pipe.
	return newTestClient(cli)
}

// writeJSONFrame writes a JSON response frame to w (test helper for the
// fake daemon side).
func writeJSONFrame(t *testing.T, w io.Writer, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := writeFrame(w, frameJSON, b); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
}

func baseReq() Request {
	return Request{
		ID:       "t1",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}
}

// TestPostTextStreamCollapse: text deltas concatenate; done terminates.
func TestPostTextStreamCollapse(t *testing.T) {
	cl := fakeDaemon(t, func(req map[string]any, w io.Writer) {
		if req["wire_version"].(float64) != float64(WireVersion) {
			t.Errorf("wire_version = %v, want %d", req["wire_version"], WireVersion)
		}
		writeJSONFrame(t, w, map[string]any{"id": "t1", "type": "frame", "block": map[string]any{"type": "text", "delta": "Hel"}})
		writeJSONFrame(t, w, map[string]any{"id": "t1", "type": "frame", "block": map[string]any{"type": "text", "delta": "lo"}})
		writeJSONFrame(t, w, map[string]any{"id": "t1", "type": "done", "stop_reason": "end_turn"})
	})
	res, err := cl.Post(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if res.Text != "Hello" {
		t.Errorf("Text = %q, want Hello", res.Text)
	}
}

// TestPostMessageShape: a Message marshals to the v2 single-text-block
// content array (protocol-v2.md §3.3/§3.4).
func TestPostMessageShape(t *testing.T) {
	var captured map[string]any
	cl := fakeDaemon(t, func(req map[string]any, w io.Writer) {
		captured = req
		writeJSONFrame(t, w, map[string]any{"id": "t1", "type": "done", "stop_reason": "end_turn"})
	})
	if _, err := cl.Post(context.Background(), baseReq()); err != nil {
		t.Fatalf("Post: %v", err)
	}
	msgs, ok := captured["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages shape wrong: %#v", captured["messages"])
	}
	m0 := msgs[0].(map[string]any)
	if m0["role"] != "user" {
		t.Errorf("role = %v, want user", m0["role"])
	}
	content, ok := m0["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("content not a 1-element array: %#v", m0["content"])
	}
	cb := content[0].(map[string]any)
	if cb["type"] != "text" || cb["text"] != "hi" {
		t.Errorf("content block = %#v, want text/hi", cb)
	}
}

// TestPostToolUse: a structured tool_use block surfaces in Result.ToolCalls.
func TestPostToolUse(t *testing.T) {
	cl := fakeDaemon(t, func(req map[string]any, w io.Writer) {
		writeJSONFrame(t, w, map[string]any{
			"id": "t1", "type": "frame",
			"block": map[string]any{
				"type": "tool_use", "tool_call_id": "c1", "name": "route",
				"input": map[string]any{"processors": []string{"compress"}},
			},
		})
		writeJSONFrame(t, w, map[string]any{"id": "t1", "type": "done", "stop_reason": "tool_use"})
	})
	res, err := cl.Post(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "route" {
		t.Fatalf("ToolCalls = %#v", res.ToolCalls)
	}
	if !strings.Contains(string(res.ToolCalls[0].Input), "compress") {
		t.Errorf("tool input = %s", res.ToolCalls[0].Input)
	}
}

// TestPostErrorFrame: a terminal error frame becomes a Go error.
func TestPostErrorFrame(t *testing.T) {
	cl := fakeDaemon(t, func(req map[string]any, w io.Writer) {
		writeJSONFrame(t, w, map[string]any{"id": "t1", "type": "error", "code": "wire_version_unsupported", "message": "want 2 got 1"})
	})
	_, err := cl.Post(context.Background(), baseReq())
	if err == nil {
		t.Fatal("expected error from error frame")
	}
	if !strings.Contains(err.Error(), "wire_version_unsupported") {
		t.Errorf("err = %v, want it to name the code", err)
	}
}

// TestFrameRoundTrip: writeFrame → readFrame preserves type + payload,
// including a payload containing a 0x0A byte (the thing newline framing
// could not carry).
func TestFrameRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		ftype   byte
		payload []byte
	}{
		{frameJSON, []byte(`{"a":1}`)},
		{frameBlob, []byte{0x00, 0x0A, 0xFF, 0x0A, 0x7B}},
		{frameJSON, []byte{}},
	} {
		var buf bytes.Buffer
		if err := writeFrame(&buf, tc.ftype, tc.payload); err != nil {
			t.Fatalf("writeFrame: %v", err)
		}
		ft, got, err := readFrame(bufio.NewReader(&buf))
		if err != nil {
			t.Fatalf("readFrame: %v", err)
		}
		if ft != tc.ftype || !bytes.Equal(got, tc.payload) {
			t.Errorf("round-trip: type 0x%02x payload %v, want 0x%02x %v", ft, got, tc.ftype, tc.payload)
		}
	}
}

// TestReadFrameCapRejected: a length prefix over the cap is rejected
// before any payload is read.
func TestReadFrameCapRejected(t *testing.T) {
	var buf bytes.Buffer
	var prefix [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(prefix[:], uint64(MaxFrameBytes)+1)
	buf.Write(prefix[:n])
	buf.WriteByte(frameJSON)
	// no payload written — a correct reader must reject on the length alone
	_, _, err := readFrame(bufio.NewReader(&buf))
	if err == nil {
		t.Fatal("expected cap rejection")
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Errorf("err = %v, want cap message", err)
	}
}

// TestReadFrameUnknownType: a frame-type byte other than 0x01/0x02 is
// malformed.
func TestReadFrameUnknownType(t *testing.T) {
	var buf bytes.Buffer
	var prefix [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(prefix[:], 1)
	buf.Write(prefix[:n])
	buf.WriteByte(0x09) // bogus type
	buf.WriteByte('x')
	_, _, err := readFrame(bufio.NewReader(&buf))
	if err == nil || !strings.Contains(err.Error(), "unknown frame-type") {
		t.Errorf("err = %v, want unknown frame-type", err)
	}
}

// TestPostStreamClosedNoTerminal: EOF before a terminal frame with no
// accumulated text is a not-ready shape (fail-open signal).
func TestPostStreamClosedNoTerminal(t *testing.T) {
	srv, cli := net.Pipe()
	go func() {
		r := bufio.NewReader(srv)
		_, _, _ = readFrame(r) // consume the request
		_ = srv.Close()        // close without any response
	}()
	cl := newTestClient(cli)
	_, err := cl.Post(context.Background(), baseReq())
	if err == nil {
		t.Fatal("expected error on premature close")
	}
}

func TestIsTransientConnect(t *testing.T) {
	transient := []string{
		"dial unix /x: connect: connection refused",
		"open \\\\.\\pipe\\inferd: The system cannot find the file specified.",
		"all pipe instances are busy",
	}
	for _, s := range transient {
		if !isTransientConnect(errString(s)) {
			t.Errorf("isTransientConnect(%q) = false, want true", s)
		}
	}
	if isTransientConnect(errString("some unrelated bug")) {
		t.Error("unrelated error should not be transient")
	}
}
