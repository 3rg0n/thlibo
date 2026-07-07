package processors

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// har-filter: distill an HTTP Archive (.har) capture for an AI client.
//
// A HAR is a big JSON object (log.entries[]) where most bytes are
// response bodies for static assets (JS/CSS/images/fonts) an AI never
// needs, and where secrets hide in query strings, auth headers, cookies,
// and request bodies. This filter turns it into a compact, redacted,
// one-line-per-request log:
//
//	METHOD status url  (mime size time)
//	  <small JSON/error body, redacted>            (only when useful)
//	  auth: <header>=<redacted>                     (only when present)
//
// It drops whole static-asset entries, non-text response bodies, and
// per-entry timing/cache/connection plumbing; redacts query-string
// secrets, auth headers + cookies, request-body credentials, and JWTs /
// key-shaped tokens in kept bodies. Non-HAR input passes through
// unchanged; the engine's monotonic guard returns the original if this
// somehow isn't smaller.

func init() { RegisterNative("har-filter", harFilter) }

const harRedacted = "<redacted>"

// harSecretParam matches query-string / form / cookie keys whose values
// are secrets.
var harSecretParam = regexp.MustCompile(`(?i)^(?:.*[-_.])?(?:authkey|api[-_]?key|access[-_]?token|refresh[-_]?token|id[-_]?token|token|secret|password|passwd|pwd|sig|signature|scid|sessionid|session[-_]?token|auth|bearer|client[-_]?secret|x-api-key)$`)

// harSecretHeader matches request/response header names to redact.
var harSecretHeader = regexp.MustCompile(`(?i)^(?:authorization|cookie|set-cookie|x-api-key|x-auth-token|x-amz-security-token|api-key|proxy-authorization|x-csrf-token|x-xsrf-token)$`)

// harJWT matches a JWT (three base64url segments). High-signal; unlikely
// to false-positive on normal prose.
var harJWT = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}`)

// harLongToken matches a bare long high-entropy token (>=32 chars of
// base64url/hex). Redacted only inside kept JSON bodies.
var harLongToken = regexp.MustCompile(`\b[A-Za-z0-9_-]{32,}\b`)

// harStaticExt / harStaticMime identify pure static-asset entries to
// drop wholesale (keep only XHR/fetch/document/API calls).
var harStaticMime = regexp.MustCompile(`(?i)^(?:image/|font/|text/css|application/javascript|application/x-javascript|text/javascript|application/font|application/wasm|application/octet-stream|audio/|video/)`)

func harStaticPath(u string) bool {
	// strip query, look at extension
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	u = strings.ToLower(u)
	for _, ext := range []string{".js", ".mjs", ".css", ".png", ".jpg", ".jpeg", ".gif", ".webp",
		".svg", ".ico", ".woff", ".woff2", ".ttf", ".otf", ".eot", ".wasm", ".map"} {
		if strings.HasSuffix(u, ext) {
			return true
		}
	}
	return false
}

func harFilter(raw []byte) []byte {
	// Cheap gate: HARs are JSON objects that start with "{" and contain
	// a "log" key with "entries". Anything else passes through.
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, "{") || !strings.Contains(trimmed, "\"entries\"") {
		return raw
	}

	var doc struct {
		Log struct {
			Version string        `json:"version"`
			Entries []harEntryRaw `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return raw // not parseable as JSON — leave it alone
	}
	if len(doc.Log.Entries) == 0 {
		return raw // not a HAR shape we recognise
	}

	var out []string
	kept, dropped := 0, 0
	for _, e := range doc.Log.Entries {
		method := strings.ToUpper(e.Request.Method)
		urlStr := e.Request.URL
		mime := ""
		if e.Response.Content != nil {
			// Take the media type before any ";charset=…" parameter.
			mime = strings.TrimSpace(strings.SplitN(e.Response.Content.MimeType, ";", 2)[0])
		}

		// Drop pure static-asset entries entirely.
		if harStaticPath(urlStr) || (mime != "" && harStaticMime.MatchString(mime)) {
			dropped++
			continue
		}
		kept++

		size := 0
		if e.Response.Content != nil {
			size = e.Response.Content.Size
		}
		line := method + " " + strconv.Itoa(e.Response.Status) + " " + harRedactURL(urlStr)
		meta := strings.TrimSpace(mime)
		if size > 0 {
			if meta != "" {
				meta += " "
			}
			meta += harSize(size)
		}
		if e.Time > 0 {
			if meta != "" {
				meta += " "
			}
			meta += strconv.Itoa(int(e.Time+0.5)) + "ms"
		}
		if meta != "" {
			line += "  (" + meta + ")"
		}
		out = append(out, line)

		// Redacted auth headers worth flagging (request side).
		for _, h := range e.Request.Headers {
			if harSecretHeader.MatchString(h.Name) {
				out = append(out, "  "+h.Name+": "+harRedacted)
			}
		}

		// Request body credentials. Prefer the raw text; fall back to the
		// parsed params[] array (some HAR generators populate only one).
		if e.Request.PostData != nil {
			body := harRedactBody(e.Request.PostData.Text, e.Request.PostData.MimeType)
			if body == "" {
				body = harRedactFormParams(e.Request.PostData.Params)
			}
			if body != "" {
				out = append(out, "  → "+body)
			}
		}

		// Keep a small JSON / error response body (redacted). Skip empty
		// and non-JSON (already-dropped static bodies never reach here).
		if e.Response.Content != nil {
			m := e.Response.Content.MimeType
			body := e.Response.Content.Text
			keepBody := strings.Contains(m, "json") || e.Response.Status >= 400
			if keepBody && strings.TrimSpace(body) != "" {
				red := harRedactBody(body, m)
				if red != "" {
					out = append(out, "  ← "+red)
				}
			}
		}
	}

	if len(out) == 0 {
		return raw
	}
	header := "HAR " + doc.Log.Version + ": " + strconv.Itoa(kept) + " requests (" + strconv.Itoa(dropped) + " static dropped)"
	out = append([]string{header}, out...)
	return []byte(strings.Join(out, "\n") + "\n")
}

// --- entry JSON shapes (only the fields we read) ---

type harEntryRaw struct {
	Time    float64 `json:"time"`
	Request struct {
		Method   string     `json:"method"`
		URL      string     `json:"url"`
		Headers  []harNV    `json:"headers"`
		PostData *harPostDt `json:"postData"`
	} `json:"request"`
	Response struct {
		Status  int `json:"status"`
		Content *struct {
			Size     int    `json:"size"`
			MimeType string `json:"mimeType"`
			Text     string `json:"text"`
		} `json:"content"`
	} `json:"response"`
}

type harNV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type harPostDt struct {
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
	Params   []harNV `json:"params"`
}

// harRedactURL redacts secrets in a URL while preserving the endpoint so
// it stays legible: userinfo credentials (user:pass@host), secret-named
// query parameters, and secret-named fragment parameters (some SPAs stash
// tokens after the #). The rest of the URL is untouched.
func harRedactURL(raw string) string {
	raw = harRedactUserinfo(raw)

	// Split off a fragment (#…) first so query redaction doesn't run into
	// it; the fragment gets the same secret-param treatment.
	frag := ""
	if h := strings.IndexByte(raw, '#'); h >= 0 {
		frag = raw[h+1:]
		raw = raw[:h]
	}

	if q := strings.IndexByte(raw, '?'); q >= 0 {
		raw = raw[:q+1] + harRedactParams(raw[q+1:])
	}
	if frag != "" {
		// Only treat the fragment as params if it looks like key=value
		// pairs; otherwise leave a route like #/dashboard alone.
		if strings.Contains(frag, "=") {
			frag = harRedactParams(frag)
		}
		raw += "#" + frag
	}
	return raw
}

// harRedactUserinfo redacts a password (and lone token) embedded in a URL
// authority: scheme://user:pass@host → scheme://user:<redacted>@host.
func harRedactUserinfo(raw string) string {
	scheme := strings.Index(raw, "://")
	if scheme < 0 {
		return raw
	}
	rest := raw[scheme+3:]
	// The authority ends at the first '/', '?' or '#'.
	authEnd := strings.IndexAny(rest, "/?#")
	authority := rest
	if authEnd >= 0 {
		authority = rest[:authEnd]
	}
	at := strings.LastIndexByte(authority, '@')
	if at < 0 {
		return raw // no userinfo
	}
	userinfo := authority[:at]
	hostPart := authority[at:] // includes the '@'
	if colon := strings.IndexByte(userinfo, ':'); colon >= 0 {
		userinfo = userinfo[:colon+1] + harRedacted // keep user, redact pass
	} else {
		userinfo = harRedacted // a bare token as userinfo — redact whole
	}
	newAuthority := userinfo + hostPart
	return raw[:scheme+3] + newAuthority + rest[len(authority):]
}

// harRedactParams redacts values of secret-named keys in an "&"-joined
// key=value string (query string or fragment), preserving order and the
// non-secret pairs.
func harRedactParams(query string) string {
	parts := strings.Split(query, "&")
	for i, p := range parts {
		eq := strings.IndexByte(p, '=')
		if eq < 0 {
			continue
		}
		key := p[:eq]
		dec, err := url.QueryUnescape(key)
		if err != nil {
			dec = key
		}
		// Strip an array suffix (key[]=…) before matching.
		dec = strings.TrimSuffix(dec, "[]")
		if harSecretParam.MatchString(dec) {
			parts[i] = key + "=" + harRedacted
		}
	}
	return strings.Join(parts, "&")
}

// harRedactBody redacts secrets in a request/response body. If the body
// parses as JSON it redacts secret-named keys structurally; for anything
// (incl. non-JSON) it also masks JWTs and long bare tokens. Returns a
// size-bounded, single-line preview.
//
// The declared mime is only a hint — APIs routinely serve JSON as
// text/plain — so structural redaction is driven by a content sniff
// (leading "{"/"["), not by the mimeType. This is what stops a
// short (<32-char) secret in a JSON field mislabelled text/plain from
// slipping past the long-token masker.
func harRedactBody(text, mime string) string {
	_ = mime // retained for signature stability / future dispatch hints
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if s := strings.TrimSpace(text); strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err == nil {
			harRedactJSONValue(v)
			// Marshal without HTML escaping so the "<redacted>" marker
			// (and any legitimate <, >, & in the body) stays readable
			// rather than becoming <redacted>.
			var buf strings.Builder
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(false)
			if err := enc.Encode(v); err == nil {
				text = strings.TrimRight(buf.String(), "\n")
			}
		}
	}
	// Form-urlencoded bodies (password=…&token=…) aren't JSON, so the
	// structural pass above skips them and a short secret would sail past
	// the long-token masker. Redact secret-named form keys directly.
	if strings.Contains(mime, "form-urlencoded") ||
		(!strings.HasPrefix(text, "{") && !strings.HasPrefix(text, "[") &&
			strings.Contains(text, "=") && !strings.Contains(text, " ")) {
		text = harRedactParams(text)
	}

	// Token masking on the (possibly re-marshalled) text.
	text = harJWT.ReplaceAllString(text, harRedacted)
	text = harLongToken.ReplaceAllStringFunc(text, func(tok string) string {
		// Keep short-ish alnum words; only mask genuinely long tokens.
		if len(tok) >= 32 {
			return harRedacted
		}
		return tok
	})
	// Collapse whitespace + bound length for a compact preview.
	text = strings.Join(strings.Fields(text), " ")
	const max = 300
	if len(text) > max {
		// Truncate on a rune boundary so a multi-byte character isn't
		// split into invalid UTF-8.
		cut := max
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "…"
	}
	return text
}

// harRedactFormParams redacts secret-named fields in a HAR postData
// params[] array (present for form submissions). Returns the surviving
// pairs as a compact "key=value" preview, values of secret keys masked.
func harRedactFormParams(params []harNV) string {
	if len(params) == 0 {
		return ""
	}
	var b strings.Builder
	for i, p := range params {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(p.Name)
		b.WriteByte('=')
		key := strings.TrimSuffix(p.Name, "[]")
		if harSecretParam.MatchString(key) {
			b.WriteString(harRedacted)
		} else {
			b.WriteString(p.Value)
		}
	}
	return b.String()
}

// harRedactJSONValue walks a decoded JSON value and redacts any value
// whose object key looks secret — regardless of the value's type. A
// secret-named field holding a number ({"password":12345}) or a nested
// object ({"token":{"jwt":"…"}}) is just as sensitive as a string one, so
// the whole value is replaced with the marker rather than recursed into.
func harRedactJSONValue(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if harSecretParam.MatchString(k) {
				t[k] = harRedacted // redact the entire value, any type
				continue
			}
			harRedactJSONValue(val)
		}
	case []any:
		for _, val := range t {
			harRedactJSONValue(val)
		}
	}
}

func harSize(n int) string {
	switch {
	case n >= 1<<20:
		return strconv.Itoa(n>>20) + "MB"
	case n >= 1<<10:
		return strconv.Itoa(n>>10) + "KB"
	default:
		return strconv.Itoa(n) + "B"
	}
}
