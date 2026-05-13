package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Gemma 4 sampling defaults. Applied when a field is omitted.
const (
	DefaultTemperature = 1.0
	DefaultTopP        = 0.95
	DefaultTopK        = 64
	DefaultMaxTokens   = 1000
)

// ValidImageTokenBudgets is the only set of values the daemon accepts on
// inference requests. Any other value is rejected with an error frame
// before the request reaches llamafile.
var ValidImageTokenBudgets = [...]int{70, 140, 280, 560, 1120}

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// Request is the inference request envelope. A client-generated ID travels
// with every frame of the response stream for correlation and cancellation.
// Pointer fields distinguish "omitted" (apply default) from "zero" (use 0).
type Request struct {
	ID               string    `json:"id,omitempty"`
	Messages         []Message `json:"messages"`
	Temperature      *float64  `json:"temperature,omitempty"`
	TopP             *float64  `json:"top_p,omitempty"`
	TopK             *int      `json:"top_k,omitempty"`
	MaxTokens        *int      `json:"max_tokens,omitempty"`
	Stream           *bool     `json:"stream,omitempty"`
	ImageTokenBudget *int      `json:"image_token_budget,omitempty"`
	// Grammar is a GBNF (llama.cpp grammar) that constrains output
	// token-by-token. Used for structured output (e.g. routing calls).
	// Empty = unconstrained. Forwarded verbatim to llamafile.
	Grammar string `json:"grammar,omitempty"`
}

type ResponseType string

const (
	ResponseStatus ResponseType = "status"
	ResponseToken  ResponseType = "token"
	ResponseDone   ResponseType = "done"
	ResponseError  ResponseType = "error"
)

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Response is a single frame from the daemon. One request produces a
// stream: zero or more status, zero or more token, exactly one terminal
// frame (done or error).
type Response struct {
	ID      string       `json:"id"`
	Type    ResponseType `json:"type"`
	Status  string       `json:"status,omitempty"`
	Content string       `json:"content,omitempty"`
	Usage   *Usage       `json:"usage,omitempty"`
	Message string       `json:"message,omitempty"`
}

// AdminID is the ID used on admin-socket status frames that are not tied to
// an inference request (e.g. the startup loading_model → ready transition).
const AdminID = "admin"

// Resolved holds sampling parameters after defaults have been applied.
// Callers downstream of Resolve see no pointers — every field is set.
type Resolved struct {
	Temperature      float64
	TopP             float64
	TopK             int
	MaxTokens        int
	Stream           bool
	ImageTokenBudget int    // 0 means "not multimodal"
	Grammar          string // empty = unconstrained
}

// Resolve applies Gemma 4 defaults and validates the request. The returned
// Resolved is safe to hand to llamafile without further checking. Stream
// defaults to true because callers invoking the daemon over a socket
// typically want incremental tokens.
func (r *Request) Resolve() (Resolved, error) {
	if len(r.Messages) == 0 {
		return Resolved{}, errors.New("ipc: messages must not be empty")
	}
	for i, m := range r.Messages {
		switch m.Role {
		case RoleSystem, RoleUser, RoleAssistant:
		default:
			return Resolved{}, fmt.Errorf("ipc: messages[%d]: invalid role %q", i, m.Role)
		}
	}

	out := Resolved{
		Temperature: DefaultTemperature,
		TopP:        DefaultTopP,
		TopK:        DefaultTopK,
		MaxTokens:   DefaultMaxTokens,
		Stream:      true,
	}
	if r.Temperature != nil {
		out.Temperature = *r.Temperature
	}
	if r.TopP != nil {
		out.TopP = *r.TopP
	}
	if r.TopK != nil {
		out.TopK = *r.TopK
	}
	if r.MaxTokens != nil {
		out.MaxTokens = *r.MaxTokens
	}
	if r.Stream != nil {
		out.Stream = *r.Stream
	}
	if r.ImageTokenBudget != nil {
		if !validImageBudget(*r.ImageTokenBudget) {
			return Resolved{}, fmt.Errorf("ipc: image_token_budget %d not in %v", *r.ImageTokenBudget, ValidImageTokenBudgets)
		}
		out.ImageTokenBudget = *r.ImageTokenBudget
	}
	out.Grammar = r.Grammar
	return out, nil
}

func validImageBudget(v int) bool {
	for _, ok := range ValidImageTokenBudgets {
		if v == ok {
			return true
		}
	}
	return false
}

// WriteFrame serialises a single response frame followed by a newline.
// Callers must call this once per frame; the caller is responsible for
// flushing or the underlying writer must be unbuffered (e.g. a net.Conn).
func WriteFrame(w io.Writer, f Response) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// MaxRequestBytes caps the size of a single NDJSON request frame.
// Larger frames are rejected with ErrFrameTooLarge so a local client
// cannot exhaust the daemon's heap by writing an unbounded line
// without a newline. See THREAT_MODEL.md finding #5.
const MaxRequestBytes = 64 << 20

// ErrFrameTooLarge is returned when a single NDJSON frame exceeds
// MaxRequestBytes.
var ErrFrameTooLarge = errors.New("ipc: request frame exceeds MaxRequestBytes")

// ReadRequest reads one newline-delimited request from r. io.EOF means the
// peer closed the connection cleanly with no more requests pending.
// Frames larger than MaxRequestBytes return ErrFrameTooLarge - the
// caller should close the connection rather than try to resync.
func ReadRequest(r *bufio.Reader) (Request, error) {
	line, err := readLimitedLine(r, MaxRequestBytes)
	if err != nil {
		if errors.Is(err, ErrFrameTooLarge) {
			return Request{}, err
		}
		if len(line) > 0 && errors.Is(err, io.EOF) {
			return parseRequest(line)
		}
		return Request{}, err
	}
	return parseRequest(line)
}

// readLimitedLine is bufio.Reader.ReadBytes('\n') with a hard cap.
// Returns ErrFrameTooLarge as soon as cap+1 bytes have been read
// without hitting a newline; the partial bytes are discarded.
func readLimitedLine(r *bufio.Reader, limit int) ([]byte, error) {
	buf := make([]byte, 0, 512)
	for {
		b, err := r.ReadByte()
		if err != nil {
			return buf, err
		}
		if b == '\n' {
			buf = append(buf, b)
			return buf, nil
		}
		if len(buf) >= limit {
			return nil, ErrFrameTooLarge
		}
		buf = append(buf, b)
	}
}

func parseRequest(line []byte) (Request, error) {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return Request{}, fmt.Errorf("ipc: decode request: %w", err)
	}
	return req, nil
}
