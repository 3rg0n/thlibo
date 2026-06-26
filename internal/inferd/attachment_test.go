package inferd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"testing"
)

// capturedFrame is one frame the client wrote to the daemon.
type capturedFrame struct {
	ftype   byte
	payload []byte
}

// attachmentDaemon serves one request, capturing EVERY frame the client
// sends (request JSON + per-attachment descriptor + BLOB), then writes a
// terminal done. The captured frames are delivered on the returned
// channel after the exchange completes.
func attachmentDaemon(t *testing.T, nClientFrames int) (*Client, <-chan []capturedFrame) {
	t.Helper()
	srv, cli := net.Pipe()
	out := make(chan []capturedFrame, 1)
	go func() {
		defer func() { _ = srv.Close() }()
		r := bufio.NewReader(srv)
		frames := make([]capturedFrame, 0, nClientFrames)
		for i := 0; i < nClientFrames; i++ {
			ft, p, err := readFrame(r)
			if err != nil {
				t.Errorf("daemon readFrame %d: %v", i, err)
				out <- frames
				return
			}
			// copy payload — readFrame's slice is reused
			cp := make([]byte, len(p))
			copy(cp, p)
			frames = append(frames, capturedFrame{ftype: ft, payload: cp})
		}
		// terminal done so client.Post returns
		b, _ := json.Marshal(map[string]any{"id": "t1", "type": "done", "stop_reason": "end_turn"})
		_ = writeFrame(srv, frameJSON, b)
		out <- frames
	}()
	return newTestClient(cli), out
}

// TestPostImageAttachmentWire verifies the on-wire shape of an image
// request (protocol-v2.md §3.1/§3.5/§3.7): request JSON frame with
// metadata-only attachments, then a BlobDescriptor JSON frame, then a
// 0x02 BLOB frame carrying the raw RGB.
func TestPostImageAttachmentWire(t *testing.T) {
	rgb := []byte{1, 2, 3, 4, 5, 6} // 2px * 3 bytes (fake 2x1)
	// client sends: request(JSON) + descriptor(JSON) + blob(BLOB) = 3 frames
	cl, framesCh := attachmentDaemon(t, 3)

	req := Request{
		ID: "t1",
		Messages: []Message{{
			Role: RoleUser, Content: "ocr this", ImageRefs: []string{"p1"},
		}},
		Attachments: []Attachment{{ID: "p1", Width: 2, Height: 1, RGB: rgb}},
	}
	if _, err := cl.Post(context.Background(), req); err != nil {
		t.Fatalf("Post: %v", err)
	}
	frames := <-framesCh
	if len(frames) != 3 {
		t.Fatalf("client sent %d frames, want 3 (request, descriptor, blob)", len(frames))
	}

	// Frame 0: request JSON. attachments[] must be metadata-only (no bytes/rgb).
	if frames[0].ftype != frameJSON {
		t.Errorf("frame 0 type 0x%02x, want JSON", frames[0].ftype)
	}
	var reqMsg map[string]any
	if err := json.Unmarshal(frames[0].payload, &reqMsg); err != nil {
		t.Fatalf("frame 0 unmarshal: %v", err)
	}
	atts, ok := reqMsg["attachments"].([]any)
	if !ok || len(atts) != 1 {
		t.Fatalf("attachments not a 1-elem array: %#v", reqMsg["attachments"])
	}
	att0 := atts[0].(map[string]any)
	if att0["kind"] != "image" || att0["id"] != "p1" {
		t.Errorf("attachment meta = %#v, want kind=image id=p1", att0)
	}
	if att0["width"].(float64) != 2 || att0["height"].(float64) != 1 {
		t.Errorf("attachment dims = %vx%v, want 2x1", att0["width"], att0["height"])
	}
	// CRITICAL: raw bytes must NOT be in the JSON (ADR 0016 / no base64).
	for _, k := range []string{"bytes", "rgb", "data"} {
		if _, present := att0[k]; present {
			t.Errorf("attachment JSON leaked raw bytes under %q: %#v", k, att0)
		}
	}
	if bytes.Contains(frames[0].payload, rgb) {
		t.Errorf("request JSON frame contains the raw RGB bytes; must ride in BLOB only")
	}

	// Frame 1: BlobDescriptor JSON.
	if frames[1].ftype != frameJSON {
		t.Errorf("frame 1 type 0x%02x, want JSON (descriptor)", frames[1].ftype)
	}
	var desc map[string]any
	if err := json.Unmarshal(frames[1].payload, &desc); err != nil {
		t.Fatalf("frame 1 unmarshal: %v", err)
	}
	if desc["type"] != "attachment_blob" || desc["attachment_id"] != "p1" {
		t.Errorf("descriptor = %#v, want type=attachment_blob attachment_id=p1", desc)
	}
	if desc["len"].(float64) != float64(len(rgb)) {
		t.Errorf("descriptor len = %v, want %d", desc["len"], len(rgb))
	}

	// Frame 2: raw BLOB with exactly the RGB bytes.
	if frames[2].ftype != frameBlob {
		t.Errorf("frame 2 type 0x%02x, want BLOB (0x02)", frames[2].ftype)
	}
	if !bytes.Equal(frames[2].payload, rgb) {
		t.Errorf("BLOB payload = %v, want %v", frames[2].payload, rgb)
	}
}

// TestPostMultipleAttachmentsOrder verifies descriptor+BLOB pairs are
// emitted in attachments[] order (protocol-v2.md §3.1).
func TestPostMultipleAttachmentsOrder(t *testing.T) {
	a := []byte{0xAA, 0xAA, 0xAA}
	b := []byte{0xBB, 0xBB, 0xBB}
	// request + 2*(descriptor+blob) = 5 frames
	cl, framesCh := attachmentDaemon(t, 5)
	req := Request{
		ID: "t1",
		Messages: []Message{{
			Role: RoleUser, Content: "two", ImageRefs: []string{"a", "b"},
		}},
		Attachments: []Attachment{
			{ID: "a", Width: 1, Height: 1, RGB: a},
			{ID: "b", Width: 1, Height: 1, RGB: b},
		},
	}
	if _, err := cl.Post(context.Background(), req); err != nil {
		t.Fatalf("Post: %v", err)
	}
	frames := <-framesCh
	if len(frames) != 5 {
		t.Fatalf("sent %d frames, want 5", len(frames))
	}
	// frame 1 desc → "a", frame 2 blob a; frame 3 desc → "b", frame 4 blob b
	var d1 map[string]any
	_ = json.Unmarshal(frames[1].payload, &d1)
	if d1["attachment_id"] != "a" {
		t.Errorf("first descriptor id = %v, want a", d1["attachment_id"])
	}
	if !bytes.Equal(frames[2].payload, a) {
		t.Errorf("first blob != a")
	}
	var d3 map[string]any
	_ = json.Unmarshal(frames[3].payload, &d3)
	if d3["attachment_id"] != "b" {
		t.Errorf("second descriptor id = %v, want b", d3["attachment_id"])
	}
	if !bytes.Equal(frames[4].payload, b) {
		t.Errorf("second blob != b")
	}
}

// TestMessageImageBlockShape verifies a Message with ImageRefs marshals
// to a text block followed by image blocks referencing attachment ids.
func TestMessageImageBlockShape(t *testing.T) {
	m := Message{Role: RoleUser, Content: "look", ImageRefs: []string{"x", "y"}}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Role    string `json:"role"`
		Content []struct {
			Type         string `json:"type"`
			Text         string `json:"text"`
			AttachmentID string `json:"attachment_id"`
		} `json:"content"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Content) != 3 {
		t.Fatalf("content blocks = %d, want 3 (text + 2 images)", len(got.Content))
	}
	if got.Content[0].Type != "text" || got.Content[0].Text != "look" {
		t.Errorf("block 0 = %#v, want text/look", got.Content[0])
	}
	if got.Content[1].Type != "image" || got.Content[1].AttachmentID != "x" {
		t.Errorf("block 1 = %#v, want image/x", got.Content[1])
	}
	if got.Content[2].Type != "image" || got.Content[2].AttachmentID != "y" {
		t.Errorf("block 2 = %#v, want image/y", got.Content[2])
	}
}

// TestTextOnlyRequestNoBlobFrames confirms a text-only request still
// sends exactly one frame (no descriptor/blob) — no regression.
func TestTextOnlyRequestNoBlobFrames(t *testing.T) {
	cl, framesCh := attachmentDaemon(t, 1)
	if _, err := cl.Post(context.Background(), Request{
		ID:       "t1",
		Messages: []Message{{Role: RoleUser, Content: "plain"}},
	}); err != nil {
		t.Fatalf("Post: %v", err)
	}
	frames := <-framesCh
	if len(frames) != 1 || frames[0].ftype != frameJSON {
		t.Fatalf("text-only request sent %d frames (want 1 JSON)", len(frames))
	}
	if bytes.Contains(frames[0].payload, []byte("attachments")) {
		t.Errorf("text-only request JSON should omit attachments: %s", frames[0].payload)
	}
}
