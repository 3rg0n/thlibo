// Package inferd is thlibo's owned client for the inferd inference
// daemon's IPC wire protocol (v0.4, ADR 0021). thlibo speaks the wire
// directly rather than depending on inferd's reference Go client:
//
//	thlibo (this package)  ><  ipc  ><  inferd  ><  llama.cpp
//
// The contract is the wire protocol — docs/protocol-v2.md in the inferd
// repo — not inferd's client source. The protocol is frozen and carries
// an in-band wire_version (see WireVersion) that fails loudly on
// mismatch, which is exactly the condition under which implementing
// against a spec (rather than vendoring a client) is correct. Depending
// on inferd's client previously kept thlibo pinned to the removed v1
// surface; owning the wire decouples thlibo's release from inferd's
// client cadence.
//
// Scope: the **generation** surface only (length-prefixed, type-tagged
// framing). thlibo does not use embeddings, so the NDJSON embed socket
// is not implemented here. Text-only requests today; image/audio
// attachments (the BLOB-frame path) are reserved for the PDF-OCR work
// and intentionally absent.
//
// Two contracts this package preserves from the prior internal/inferdcli:
//
//   - Passive readiness (ADR 0006). Each Post does a fresh dial. A
//     connect failure is inferd's "not ready" signal — surfaced as
//     ErrBackendNotReady so the middleware fails open to passthrough.
//   - Stream collapse. The daemon streams response frames; Post drains
//     to the terminal frame and returns the concatenated text.
package inferd

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// WireVersion is the wire-format version this client speaks (ADR 0021).
// Stamped on every request; the daemon rejects a mismatch with a
// wire_version_unsupported error frame and closes the connection.
const WireVersion uint32 = 1

// MaxFrameBytes is the 64 MiB per-frame payload cap (THREAT_MODEL F-5).
// Enforced on the decoded length prefix before any payload is allocated.
const MaxFrameBytes = 64 << 20

// frame-type tags for the length-prefixed framing (ADR 0021 §2).
const (
	frameJSON byte = 0x01
	frameBlob byte = 0x02 // raw decoded media bytes (image RGB, etc.)
)

// Role is a conversation role in a Message. Lowercase on the wire.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn in the v2 conversation. Content is the text of
// the turn; ImageRefs, when non-empty, adds image content blocks
// referencing attachment IDs present in Request.Attachments (the bytes
// ride in BLOB frames, not here — protocol-v2.md §3.4/§3.5). A turn may
// carry text, images, or both; ordering is text block first, then
// images, matching how the daemon's mtmd path interleaves them.
type Message struct {
	Role      Role
	Content   string
	ImageRefs []string // attachment IDs (see Request.Attachments)
}

// MarshalJSON renders a Message as a protocol-v2 MessageV2 whose content
// is a text block (when Content != "") followed by one image block per
// ImageRef (protocol-v2.md §3.3/§3.4). At least one block is always
// emitted (content MUST be non-empty per spec); a Message with neither
// text nor images marshals as a single empty text block.
func (m Message) MarshalJSON() ([]byte, error) {
	type contentBlock struct {
		Type         string `json:"type"`
		Text         string `json:"text,omitempty"`
		AttachmentID string `json:"attachment_id,omitempty"`
	}
	blocks := make([]contentBlock, 0, 1+len(m.ImageRefs))
	if m.Content != "" || len(m.ImageRefs) == 0 {
		blocks = append(blocks, contentBlock{Type: "text", Text: m.Content})
	}
	for _, id := range m.ImageRefs {
		blocks = append(blocks, contentBlock{Type: "image", AttachmentID: id})
	}
	type messageV2 struct {
		Role    Role           `json:"role"`
		Content []contentBlock `json:"content"`
	}
	return json.Marshal(messageV2{Role: m.Role, Content: blocks})
}

// Attachment is one binary payload referenced by a Message image block.
// Per ADR 0016 the consumer decodes media to raw bytes before sending;
// the daemon links no image codec. RGB is width*height*3 interleaved
// octets (no alpha). The bytes do NOT travel in the request JSON — they
// ride in a 0x02 BLOB frame keyed by ID (protocol-v2.md §3.5/§3.7).
type Attachment struct {
	ID     string
	Width  uint32
	Height uint32
	RGB    []byte // width*height*3, no alpha
}

// marshalMeta renders the attachment's JSON metadata object (no bytes)
// for the request frame's attachments[] array (protocol-v2.md §3.5).
func (a Attachment) marshalMeta() ([]byte, error) {
	return json.Marshal(struct {
		Kind   string `json:"kind"`
		ID     string `json:"id"`
		Width  uint32 `json:"width"`
		Height uint32 `json:"height"`
	}{Kind: "image", ID: a.ID, Width: a.Width, Height: a.Height})
}

// Tool is a tool definition the model may call (protocol-v2.md §3.6).
// InputSchema is a JSON Schema object. v2 has no GBNF grammar; the
// daemon does not enforce InputSchema against emitted arguments — that
// is the consumer's job when the tool_use result returns.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Request is a generation request (protocol-v2.md §3.2). WireVersion is
// set by Post; callers leave it zero. Pointer sampling fields
// distinguish "omitted" (daemon default) from "explicit zero".
//
// Attachments carries image (etc.) payloads referenced by Message
// image blocks. Its RGB bytes are NOT serialised into the request JSON
// (a custom MarshalJSON emits metadata only); client.Post writes each
// attachment's bytes as a separate BLOB frame after the request frame.
type Request struct {
	WireVersion    uint32          `json:"wire_version"`
	ID             string          `json:"id,omitempty"`
	Messages       []Message       `json:"messages"`
	Attachments    []Attachment    `json:"attachments,omitempty"`
	Tools          []Tool          `json:"tools,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	Temperature    *float64        `json:"temperature,omitempty"`
	TopP           *float64        `json:"top_p,omitempty"`
	TopK           *int            `json:"top_k,omitempty"`
	MaxTokens      *int            `json:"max_tokens,omitempty"`
	Stream         *bool           `json:"stream,omitempty"`
}

// MarshalJSON renders the request frame body. Attachments are emitted
// as metadata-only objects (kind/id/width/height) — the RGB bytes ride
// in BLOB frames (protocol-v2.md §3.5). A dedicated wire struct keeps
// the byte-carrying Attachment type out of the JSON.
func (r Request) MarshalJSON() ([]byte, error) {
	metas := make([]json.RawMessage, 0, len(r.Attachments))
	for _, att := range r.Attachments {
		m, err := att.marshalMeta()
		if err != nil {
			return nil, err
		}
		metas = append(metas, m)
	}
	return json.Marshal(struct {
		WireVersion    uint32            `json:"wire_version"`
		ID             string            `json:"id,omitempty"`
		Messages       []Message         `json:"messages"`
		Attachments    []json.RawMessage `json:"attachments,omitempty"`
		Tools          []Tool            `json:"tools,omitempty"`
		ResponseFormat *ResponseFormat   `json:"response_format,omitempty"`
		Temperature    *float64          `json:"temperature,omitempty"`
		TopP           *float64          `json:"top_p,omitempty"`
		TopK           *int              `json:"top_k,omitempty"`
		MaxTokens      *int              `json:"max_tokens,omitempty"`
		Stream         *bool             `json:"stream,omitempty"`
	}{
		WireVersion:    r.WireVersion,
		ID:             r.ID,
		Messages:       r.Messages,
		Attachments:    metas,
		Tools:          r.Tools,
		ResponseFormat: r.ResponseFormat,
		Temperature:    r.Temperature,
		TopP:           r.TopP,
		TopK:           r.TopK,
		MaxTokens:      r.MaxTokens,
		Stream:         r.Stream,
	})
}

// ResponseFormat constrains generation to a structured output
// (protocol-v2.md §3.2a). The daemon translates Schema to an engine
// constraint (GBNF for llamacpp); backends without structured-output
// support ignore it and return unconstrained output — no error. Type is
// currently always "json_schema".
type ResponseFormat struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
}

// blobDescriptor is the JSON frame that precedes each 0x02 BLOB frame,
// correlating the bytes to an attachment by id (protocol-v2.md §3.7).
type blobDescriptor struct {
	Type         string `json:"type"` // always "attachment_blob"
	AttachmentID string `json:"attachment_id"`
	Len          uint64 `json:"len"`
}

// JSONSchemaFormat builds a ResponseFormat for a JSON Schema object.
func JSONSchemaFormat(schema json.RawMessage) *ResponseFormat {
	return &ResponseFormat{Type: "json_schema", Schema: schema}
}

// response is the wire shape of a ResponseV2 frame (protocol-v2.md §4).
// Decoded loosely so unknown block/stop/error variants are ignored
// (forward-compat, §10 invariant 4).
type response struct {
	ID   string `json:"id"`
	Type string `json:"type"` // "frame" | "done" | "error"
	// frame
	Block *responseBlock `json:"block,omitempty"`
	// done
	StopReason string `json:"stop_reason,omitempty"`
	// error
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// responseBlock is the streamed block inside a "frame" response
// (protocol-v2.md §4.1). thlibo consumes text deltas and the structured
// tool_use block; thinking deltas are dropped (the model's thought
// channel is not user-facing here — processors.Strip handles any that
// leak into text).
type responseBlock struct {
	Type string `json:"type"` // "text" | "thinking" | "tool_use"
	// text / thinking
	Delta string `json:"delta,omitempty"`
	// tool_use
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
}

// ToolCall is a structured tool invocation surfaced from a tool_use
// response block. Used by the router in place of the removed GBNF
// grammar + text parsing.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// Result is the outcome of a Post. Text is the concatenated text
// deltas (thought-stripping is the caller's job). ToolCalls holds any
// structured tool_use blocks the model emitted, in arrival order.
type Result struct {
	Text      string
	ToolCalls []ToolCall
}

// writeFrame writes one length-prefixed, type-tagged frame
// (protocol-v2.md §2). LEB128 length prefix (counting payload only),
// then the type byte, then the payload.
func writeFrame(w io.Writer, ftype byte, payload []byte) error {
	if len(payload) > MaxFrameBytes {
		return fmt.Errorf("inferd: frame exceeds %d byte cap", MaxFrameBytes)
	}
	var prefix [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(prefix[:], uint64(len(payload)))
	if _, err := w.Write(prefix[:n]); err != nil {
		return fmt.Errorf("write frame length: %w", err)
	}
	if _, err := w.Write([]byte{ftype}); err != nil {
		return fmt.Errorf("write frame type: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("write frame payload: %w", err)
	}
	return nil
}

// readFrame reads one length-prefixed, type-tagged frame. The 64 MiB
// cap is checked on the decoded length before the payload is allocated
// (protocol-v2.md §2). A clean io.EOF between frames bubbles up
// unchanged so callers can distinguish it from a truncated frame.
func readFrame(r byteReader) (byte, []byte, error) {
	length, err := binary.ReadUvarint(r)
	if err != nil {
		return 0, nil, err // io.EOF between frames bubbles up
	}
	if length > uint64(MaxFrameBytes) {
		return 0, nil, fmt.Errorf("inferd: frame length %d exceeds %d byte cap", length, MaxFrameBytes)
	}
	ftype, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	if ftype != frameJSON && ftype != frameBlob {
		return 0, nil, fmt.Errorf("inferd: unknown frame-type byte 0x%02x", ftype)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return ftype, payload, nil
}

// byteReader is io.Reader + io.ByteReader, satisfied by *bufio.Reader.
// binary.ReadUvarint needs ByteReader; io.ReadFull needs Reader.
type byteReader interface {
	io.Reader
	io.ByteReader
}
