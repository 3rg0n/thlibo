package processors

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// har-filter has no Python reference implementation (it was born native,
// ADR 0010), so these are direct-assertion tests rather than golden-vs-
// Python parity checks. Fixtures are synthetic — no real captured HAR
// (with live secrets) is committed to the repo. The one job that must
// never regress is redaction: a secret that leaks here leaks into an AI
// client's context.

// harTestDoc wraps entries in the minimal HAR envelope the filter needs.
func harTestDoc(entries string) string {
	return `{"log":{"version":"1.2","entries":[` + entries + `]}}`
}

// --- gate: only real HARs are touched ---

func TestHarFilterPassesThroughNonHAR(t *testing.T) {
	cases := map[string]string{
		"plain text":      "just some build output\nnothing to see here\n",
		"json no entries": `{"foo":"bar","baz":[1,2,3]}`,
		"array":           `[1,2,3]`,
		"empty":           "",
		"not json":        "entries: this mentions entries but isn't json",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got := string(harFilter([]byte(in)))
			if got != in {
				t.Errorf("non-HAR input must pass through unchanged:\n in: %q\nout: %q", in, got)
			}
		})
	}
}

func TestHarFilterEmptyEntriesPassesThrough(t *testing.T) {
	in := harTestDoc("")
	if got := string(harFilter([]byte(in))); got != in {
		t.Errorf("HAR with zero entries should pass through:\n%q", got)
	}
}

// --- static-asset dropping ---

func TestHarFilterDropsStaticAssets(t *testing.T) {
	entries := strings.Join([]string{
		// a real API call (kept)
		`{"time":10,"request":{"method":"GET","url":"https://x.test/api/data"},"response":{"status":200,"content":{"size":50,"mimeType":"application/json","text":"{}"}}}`,
		// a .js asset by extension (dropped)
		`{"time":5,"request":{"method":"GET","url":"https://x.test/app.js"},"response":{"status":200,"content":{"size":99999,"mimeType":"application/javascript"}}}`,
		// an image by mime (dropped)
		`{"time":5,"request":{"method":"GET","url":"https://x.test/logo"},"response":{"status":200,"content":{"size":88888,"mimeType":"image/png"}}}`,
		// css with query string, still dropped by extension sniff
		`{"time":5,"request":{"method":"GET","url":"https://x.test/main.css?v=3"},"response":{"status":200,"content":{"size":1234,"mimeType":"text/css"}}}`,
	}, ",")
	out := string(harFilter([]byte(harTestDoc(entries))))
	if !strings.Contains(out, "3 static dropped") {
		t.Errorf("expected 3 static assets dropped, header was:\n%s", firstLine(out))
	}
	if strings.Contains(out, "app.js") || strings.Contains(out, "logo") || strings.Contains(out, "main.css") {
		t.Errorf("static asset URL leaked into output:\n%s", out)
	}
	if !strings.Contains(out, "/api/data") {
		t.Errorf("the real API call was dropped:\n%s", out)
	}
}

// --- query-string secret redaction ---

func TestHarFilterRedactsQuerySecrets(t *testing.T) {
	entry := `{"time":10,"request":{"method":"GET","url":"https://x.test/authorize?authkey=ASMCBLYY05HL15ZCFVAI&scid=06f8dfa9442b4849b3b16b4cc7f76fe8&client_id=PUBLIC123&return=/dashboard"},"response":{"status":302,"content":{"size":10,"mimeType":"text/html"}}}`
	out := string(harFilter([]byte(harTestDoc(entry))))
	for _, secret := range []string{"ASMCBLYY05HL15ZCFVAI", "06f8dfa9442b4849b3b16b4cc7f76fe8"} {
		if strings.Contains(out, secret) {
			t.Errorf("query secret %q leaked:\n%s", secret, out)
		}
	}
	// Non-secret params + the endpoint stay legible.
	if !strings.Contains(out, "client_id=PUBLIC123") {
		t.Errorf("non-secret param client_id was clobbered:\n%s", out)
	}
	if !strings.Contains(out, "return=/dashboard") {
		t.Errorf("non-secret param return was clobbered:\n%s", out)
	}
	if !strings.Contains(out, "authkey="+harRedacted) || !strings.Contains(out, "scid="+harRedacted) {
		t.Errorf("secret params not redacted in place:\n%s", out)
	}
}

// --- auth header redaction ---

func TestHarFilterRedactsAuthHeaders(t *testing.T) {
	entry := `{"time":10,"request":{"method":"GET","url":"https://x.test/api","headers":[` +
		`{"name":"Authorization","value":"Bearer sk-abcdef123456"},` +
		`{"name":"Cookie","value":"session=deadbeefdeadbeef"},` +
		`{"name":"X-Api-Key","value":"key-9999"},` +
		`{"name":"Accept","value":"application/json"}` +
		`]},"response":{"status":200,"content":{"size":10,"mimeType":"application/json","text":"{}"}}}`
	out := string(harFilter([]byte(harTestDoc(entry))))
	for _, secret := range []string{"sk-abcdef123456", "deadbeefdeadbeef", "key-9999"} {
		if strings.Contains(out, secret) {
			t.Errorf("auth header value %q leaked:\n%s", secret, out)
		}
	}
	// Redacted markers present for each secret header.
	if strings.Count(out, harRedacted) < 3 {
		t.Errorf("expected >=3 redacted auth headers:\n%s", out)
	}
	// Non-secret header must NOT be echoed as an "auth" line.
	if strings.Contains(out, "Accept:") {
		t.Errorf("benign header should not be emitted:\n%s", out)
	}
}

// --- JSON response body: structural key redaction ---

func TestHarFilterRedactsJSONBodyKeys(t *testing.T) {
	entry := `{"time":10,"request":{"method":"POST","url":"https://x.test/login"},` +
		`"response":{"status":200,"content":{"size":80,"mimeType":"application/json",` +
		`"text":"{\"password\":\"hunter2\",\"session_token\":\"abc\",\"username\":\"neo\"}"}}}`
	out := string(harFilter([]byte(harTestDoc(entry))))
	if strings.Contains(out, "hunter2") {
		t.Errorf("password value leaked:\n%s", out)
	}
	if strings.Contains(out, "\"abc\"") {
		t.Errorf("session_token value leaked:\n%s", out)
	}
	if !strings.Contains(out, "neo") {
		t.Errorf("benign field username was dropped:\n%s", out)
	}
}

// --- the real-world leak that motivated the content-sniff fix: a JSON
// body served as text/plain must still get structural redaction, so a
// SHORT (<32-char) secret in a secret-named field doesn't slip past the
// long-token masker. Regression guard.
func TestHarFilterRedactsJSONMislabelledTextPlain(t *testing.T) {
	entry := `{"time":10,"request":{"method":"POST","url":"https://x.test/poll",` +
		`"postData":{"mimeType":"text/plain;charset=UTF-8","text":"{\"authkey\":\"AXPOXOFUS1M5SZ5SIAHP\",\"scheme\":\"https\"}"}},` +
		`"response":{"status":200,"content":{"size":10,"mimeType":"application/json","text":"{}"}}}`
	out := string(harFilter([]byte(harTestDoc(entry))))
	if strings.Contains(out, "AXPOXOFUS1M5SZ5SIAHP") {
		t.Errorf("short secret in text/plain-labelled JSON leaked:\n%s", out)
	}
	if !strings.Contains(out, "authkey") || !strings.Contains(out, harRedacted) {
		t.Errorf("authkey field not structurally redacted:\n%s", out)
	}
	// scheme is benign and should survive.
	if !strings.Contains(out, "https") {
		t.Errorf("benign field scheme dropped:\n%s", out)
	}
}

// --- JWT + long-token masking anywhere in a kept body ---

func TestHarFilterMasksJWTAndLongTokens(t *testing.T) {
	// The public jwt.io HS256 example vector ({"sub":"1234567890"}) — a
	// synthetic, non-credential test token, not a live secret.
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U" //gitleaks:allow
	longtok := "abcdefghijklmnopqrstuvwxyz0123456789ABCD"                                                 // 40 chars, no secret key name
	entry := `{"time":10,"request":{"method":"GET","url":"https://x.test/api"},` +
		`"response":{"status":200,"content":{"size":200,"mimeType":"application/json",` +
		`"text":"{\"note\":\"token is ` + jwt + ` here\",\"opaque\":\"` + longtok + `\"}"}}}`
	out := string(harFilter([]byte(harTestDoc(entry))))
	if strings.Contains(out, jwt) {
		t.Errorf("JWT leaked:\n%s", out)
	}
	if strings.Contains(out, longtok) {
		t.Errorf("long high-entropy token leaked:\n%s", out)
	}
}

// --- error bodies (>=400) are kept even when not JSON ---

func TestHarFilterKeepsErrorBody(t *testing.T) {
	entry := `{"time":10,"request":{"method":"GET","url":"https://x.test/api"},` +
		`"response":{"status":500,"content":{"size":40,"mimeType":"text/plain","text":"Internal Server Error: db down"}}}`
	out := string(harFilter([]byte(harTestDoc(entry))))
	if !strings.Contains(out, "Internal Server Error") {
		t.Errorf("error body should be kept for a 500:\n%s", out)
	}
}

func TestHarFilterDropsBenignNonJSONBody(t *testing.T) {
	// A 200 text/html body is not JSON and not an error → no body line.
	entry := `{"time":10,"request":{"method":"GET","url":"https://x.test/page"},` +
		`"response":{"status":200,"content":{"size":40,"mimeType":"text/html","text":"<html>lots of markup</html>"}}}`
	out := string(harFilter([]byte(harTestDoc(entry))))
	if strings.Contains(out, "lots of markup") {
		t.Errorf("benign 200 html body should not be emitted:\n%s", out)
	}
	// The request line itself is still present.
	if !strings.Contains(out, "/page") {
		t.Errorf("request line missing:\n%s", out)
	}
}

// --- line shape ---

func TestHarFilterLineShape(t *testing.T) {
	entry := `{"time":154.7,"request":{"method":"get","url":"https://x.test/api/data"},` +
		`"response":{"status":200,"content":{"size":2048,"mimeType":"application/json; charset=utf-8","text":"{\"ok\":true}"}}}`
	out := string(harFilter([]byte(harTestDoc(entry))))
	// METHOD uppercased, mime stripped of ;charset, size humanised, time rounded.
	want := "GET 200 https://x.test/api/data  (application/json 2KB 155ms)"
	if !strings.Contains(out, want) {
		t.Errorf("line shape wrong.\nwant substring: %q\ngot:\n%s", want, out)
	}
}

// --- monotonic guarantee via the engine wrapper ---

func TestHarFilterMonotonicViaRunNative(t *testing.T) {
	// A HAR whose distilled form would be LARGER than the tiny input must
	// come back unchanged (RunNative's strict byte-win guard).
	tiny := harTestDoc(`{"time":1,"request":{"method":"GET","url":"https://x.test/a"},"response":{"status":200,"content":{"size":1,"mimeType":"application/json","text":"{}"}}}`)
	out, ok := RunNative("har-filter", []byte(tiny))
	if !ok {
		t.Fatal("har-filter not registered with RunNative")
	}
	if len(out) > len(tiny) {
		t.Errorf("RunNative must not return output larger than input: in=%d out=%d", len(tiny), len(out))
	}
}

func TestHarFilterRegistered(t *testing.T) {
	if nativeFilter("har-filter") == nil {
		t.Fatal("har-filter must be registered as a native filter")
	}
}

// --- review-found redaction-bypass regressions ---

// A form-urlencoded POST body (password=…&token=…) is not JSON, so the
// structural pass skips it; a short secret would sail past the long-token
// masker. It must be redacted as form params.
func TestHarFilterRedactsFormUrlencodedBody(t *testing.T) {
	entry := `{"time":10,"request":{"method":"POST","url":"https://x.test/login",` +
		`"postData":{"mimeType":"application/x-www-form-urlencoded","text":"user=neo&password=hunter2&csrf=xyz"}},` +
		`"response":{"status":200,"content":{"size":10,"mimeType":"application/json","text":"{}"}}}`
	out := string(harFilter([]byte(harTestDoc(entry))))
	if strings.Contains(out, "hunter2") {
		t.Errorf("form-urlencoded password leaked:\n%s", out)
	}
	if !strings.Contains(out, "user=neo") {
		t.Errorf("benign form field dropped:\n%s", out)
	}
}

// postData.params[] (parsed form fields) must be redacted when the body
// text is empty and only the params array is populated.
func TestHarFilterRedactsPostDataParams(t *testing.T) {
	entry := `{"time":10,"request":{"method":"POST","url":"https://x.test/login",` +
		`"postData":{"mimeType":"application/x-www-form-urlencoded","params":[` +
		`{"name":"username","value":"neo"},{"name":"password","value":"hunter2"},{"name":"token","value":"abc"}]}},` +
		`"response":{"status":200,"content":{"size":10,"mimeType":"application/json","text":"{}"}}}`
	out := string(harFilter([]byte(harTestDoc(entry))))
	if strings.Contains(out, "hunter2") {
		t.Errorf("postData.params password leaked:\n%s", out)
	}
	if !strings.Contains(out, "username=neo") {
		t.Errorf("benign param dropped:\n%s", out)
	}
	if !strings.Contains(out, "password="+harRedacted) {
		t.Errorf("secret param not redacted:\n%s", out)
	}
}

// A secret-named JSON field holding a non-string value (number or nested
// object) must still be redacted — not just string values.
func TestHarFilterRedactsNonStringSecretValues(t *testing.T) {
	entry := `{"time":10,"request":{"method":"POST","url":"https://x.test/x"},` +
		`"response":{"status":200,"content":{"size":80,"mimeType":"application/json",` +
		`"text":"{\"password\":12345,\"token\":{\"inner\":\"s3cr3tvalue\"},\"ok\":true}"}}}`
	out := string(harFilter([]byte(harTestDoc(entry))))
	if strings.Contains(out, "12345") {
		t.Errorf("numeric password value leaked:\n%s", out)
	}
	if strings.Contains(out, "s3cr3tvalue") || strings.Contains(out, "inner") {
		t.Errorf("nested-object token value leaked:\n%s", out)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("benign field dropped:\n%s", out)
	}
}

// URL userinfo credentials (user:pass@host) must be redacted.
func TestHarFilterRedactsURLUserinfo(t *testing.T) {
	entry := `{"time":10,"request":{"method":"GET","url":"https://admin:s3cr3tpass@api.test/data?x=1"},` +
		`"response":{"status":200,"content":{"size":10,"mimeType":"application/json","text":"{}"}}}`
	out := string(harFilter([]byte(harTestDoc(entry))))
	if strings.Contains(out, "s3cr3tpass") {
		t.Errorf("URL userinfo password leaked:\n%s", out)
	}
	// The username and host stay legible.
	if !strings.Contains(out, "admin:") || !strings.Contains(out, "api.test/data") {
		t.Errorf("userinfo redaction mangled the endpoint:\n%s", out)
	}
}

// Secret params stashed in a URL fragment must be redacted too.
func TestHarFilterRedactsFragmentSecrets(t *testing.T) {
	entry := `{"time":10,"request":{"method":"GET","url":"https://x.test/app#access_token=SECRETTOKENVALUE&state=abc"},` +
		`"response":{"status":200,"content":{"size":10,"mimeType":"application/json","text":"{}"}}}`
	out := string(harFilter([]byte(harTestDoc(entry))))
	if strings.Contains(out, "SECRETTOKENVALUE") {
		t.Errorf("fragment access_token leaked:\n%s", out)
	}
	if !strings.Contains(out, "state=abc") {
		t.Errorf("benign fragment param dropped:\n%s", out)
	}
}

// A route-style fragment (#/dashboard) must be left alone, not mangled.
func TestHarFilterLeavesRouteFragmentAlone(t *testing.T) {
	entry := `{"time":10,"request":{"method":"GET","url":"https://x.test/app#/dashboard/users"},` +
		`"response":{"status":200,"content":{"size":10,"mimeType":"application/json","text":"{}"}}}`
	out := string(harFilter([]byte(harTestDoc(entry))))
	if !strings.Contains(out, "#/dashboard/users") {
		t.Errorf("route fragment should be preserved verbatim:\n%s", out)
	}
}

// A multi-byte UTF-8 body straddling the 300-char preview boundary must
// truncate on a rune boundary — never emit invalid UTF-8.
func TestHarFilterTruncatesOnRuneBoundary(t *testing.T) {
	// Build a JSON string value long enough to exceed the 300-char cap,
	// packed with 4-byte runes so a naive byte cut would split one.
	long := strings.Repeat("😀", 200) // 800 bytes, 200 runes
	entry := `{"time":10,"request":{"method":"GET","url":"https://x.test/x"},` +
		`"response":{"status":200,"content":{"size":900,"mimeType":"application/json",` +
		`"text":"{\"msg\":\"` + long + `\"}"}}}`
	out := string(harFilter([]byte(harTestDoc(entry))))
	if !utf8.ValidString(out) {
		t.Errorf("output contains invalid UTF-8 after truncation")
	}
	if !strings.Contains(out, "…") {
		t.Errorf("expected the truncation ellipsis in the output")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
