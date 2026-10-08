package pdf

import (
	"bytes"
	"compress/lzw"
	"compress/zlib"
	"errors"
	"fmt"
	"testing"
)

func zlibBytes(t *testing.T, payload []byte, closeStream bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if closeStream {
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	} else if err := zw.Flush(); err != nil { // sync-flush; no final block/checksum
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Some PDF producers terminate a deflate stream with a sync-flush (00 00 FF FF)
// and omit the final block + Adler-32 checksum. decompress must return the
// bytes decoded before the truncation rather than discarding the whole stream
// (which previously failed every such document with "unexpected EOF").
func TestDecompressSyncFlushTruncation(t *testing.T) {
	want := bytes.Repeat([]byte("xref-entry"), 64)
	got, err := decompress(zlibBytes(t, want, false), maxDecodedStreamSize)
	if err != nil {
		t.Fatalf("decompress returned error on sync-flush stream: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("recovered %d bytes, want %d", len(got), len(want))
	}
}

// A stream truncated before any block decodes (here: a valid zlib header and
// nothing else) is real corruption, not a recoverable missing tail. decompress
// must surface the error rather than return an empty stream as if it were valid.
func TestDecompressTruncatedBeforeAnyBlock(t *testing.T) {
	headerOnly := zlibBytes(t, []byte("payload"), false)[:2] // zlib header, no deflate blocks
	got, err := decompress(headerOnly, maxDecodedStreamSize)
	if err == nil {
		t.Fatalf("expected error on header-only stream, got %d bytes", len(got))
	}
}

// TestFilterOutputIsBounded is THREAT_MODEL #32: a stream that decodes past
// the limit is refused with ErrStreamTooLarge, never truncated (a truncated
// content stream parses as a shorter, wrong page), and output of exactly the
// limit is still accepted. Limits are tiny so the bomb is cheap to build;
// the production constants are the same code path with a bigger number.
func TestFilterOutputIsBounded(t *testing.T) {
	const limit = 1024
	exact := bytes.Repeat([]byte{'A'}, limit)
	over := bytes.Repeat([]byte{'A'}, limit+1)

	lzwBytes := func(payload []byte) []byte {
		var buf bytes.Buffer
		w := lzw.NewWriter(&buf, lzw.MSB, 8)
		if _, err := w.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	for _, tc := range []struct {
		filter      Name
		exact, over []byte
	}{
		{"FlateDecode", zlibBytes(t, exact, true), zlibBytes(t, over, true)},
		// A sync-flushed bomb takes the ErrUnexpectedEOF tolerance path in
		// decompress; the limit must still win.
		{"FlateDecode", zlibBytes(t, exact, false), zlibBytes(t, over, false)},
		{"LZWDecode", lzwBytes(exact), lzwBytes(over)},
		// 256 `z`s decode to exactly 1024 zero bytes; one more is over.
		{"ASCII85Decode", bytes.Repeat([]byte{'z'}, limit/4), bytes.Repeat([]byte{'z'}, limit/4+1)},
	} {
		t.Run(string(tc.filter), func(t *testing.T) {
			got, err := applyFilterLimit(tc.exact, tc.filter, nil, limit)
			if err != nil || len(got) != limit {
				t.Fatalf("at the limit: got %d bytes, err %v; want %d bytes, nil", len(got), err, limit)
			}
			got, err = applyFilterLimit(tc.over, tc.filter, nil, limit)
			if !errors.Is(err, ErrStreamTooLarge) {
				t.Fatalf("over the limit: got %d bytes, err %v; want ErrStreamTooLarge", len(got), err)
			}
		})
	}
}

// TestDocumentDecodeBudget: the per-stream cap alone does not bound memory,
// because a Reader caches every object it resolves. Once a Reader has decoded
// its whole budget, the next filtered stream is refused rather than decoded.
func TestDocumentDecodeBudget(t *testing.T) {
	payload := bytes.Repeat([]byte("BT /F1 12 Tf (x) Tj ET\n"), 8)
	z := zlibBytes(t, payload, true)
	stream := fmt.Sprintf("<< /Length %d /Filter /FlateDecode >>\nstream\n%s\nendstream", len(z), z)
	data := rawPDF([]string{"<< /Type /Catalog >>", stream, stream}, "")

	r, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	// Leave room for exactly one stream.
	r.decoded = maxDecodedDocumentSize - int64(len(payload))

	first, ok := r.Resolve(Ref{Num: 2}).(*Stream)
	if !ok || !bytes.Equal(first.Data, payload) {
		t.Fatalf("first stream within budget: got %#v", r.Resolve(Ref{Num: 2}))
	}
	if r.decoded != maxDecodedDocumentSize {
		t.Fatalf("budget charged %d, want %d", r.decoded, int64(maxDecodedDocumentSize))
	}
	if s, ok := r.Resolve(Ref{Num: 3}).(*Stream); ok {
		t.Fatalf("second stream past budget decoded %d bytes; want it refused", len(s.Data))
	}
}
