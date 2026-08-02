package pdf

// thlibo: this whole file is a local addition. Upstream gopdf has no
// encryption support of any kind — no /Encrypt handling, no crypt
// filter, no password logic. That is not a gap we can ignore, because
// the failure mode is silent: an encrypted PDF still has a readable
// xref and object structure, so Open() succeeds and text extraction
// returns the *ciphertext* of every string and stream. The caller gets
// plausible-looking mojibake with no error to key off.
//
// thlibo's processor contract makes that especially bad: a filter's
// output IS the tool output, so garbage doesn't report a failure, it
// replaces the document (CLAUDE.md invariant #2). Refusing loudly is
// what reaches the raw-bytes fallback.

import "errors"

// ErrEncrypted reports a PDF carrying an /Encrypt dictionary. Callers
// should treat this as "pass the original bytes through", not as a
// parse bug.
//
// This includes empty-password documents. They are common — many
// producers set an owner password to restrict printing while leaving
// the user password blank, so viewers open them without prompting and
// users think of them as unencrypted. The strings and streams are still
// encrypted, so extraction would still be garbage; the absence of a
// prompt says nothing about whether we can read the bytes.
var ErrEncrypted = errors.New("pdf: encrypted document (/Encrypt present)")

// IsEncrypted reports whether the trailer declares encryption.
//
// The check is on the trailer rather than the catalog because /Encrypt
// is a trailer key. We deliberately do not attempt to distinguish
// supported from unsupported crypt filters: we support none, so any
// /Encrypt is a refusal.
func (r *Reader) IsEncrypted() bool {
	_, present := r.Trailer()["Encrypt"]
	return present
}
