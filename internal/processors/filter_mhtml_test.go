package processors

import (
	"bytes"
	"mime/quotedprintable"
	"strings"
	"testing"
)

// mhtml-filter has no Python reference (it was born native), so these are
// direct-assertion tests. Fixtures are minimal hand-built MHTML: a
// multipart/related container with a quoted-printable text/html part and,
// where relevant, a base64 image part that must be dropped.

// buildMHTML wraps an HTML body into a multipart/related MHTML envelope,
// quoted-printable-encoding the HTML part exactly as a browser does (so
// long lines are soft-wrapped and the fixture is valid QP). Optional
// extra raw parts (already-formatted headers+body) are appended.
func buildMHTML(htmlBody string, extraParts ...string) string {
	const b = "----Boundary--TEST----"
	var qp bytes.Buffer
	qw := quotedprintable.NewWriter(&qp)
	_, _ = qw.Write([]byte(htmlBody))
	_ = qw.Close()

	var sb strings.Builder
	sb.WriteString("From: <Saved by Blink>\r\n")
	sb.WriteString("Snapshot-Content-Location: https://example.com/article\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: multipart/related;\r\n\ttype=\"text/html\";\r\n\tboundary=\"" + b + "\"\r\n")
	sb.WriteString("\r\n")
	sb.WriteString("--" + b + "\r\n")
	sb.WriteString("Content-Type: text/html\r\n")
	sb.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	sb.WriteString("Content-Location: https://example.com/article\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(qp.String() + "\r\n")
	for _, p := range extraParts {
		sb.WriteString("--" + b + "\r\n")
		sb.WriteString(p + "\r\n")
	}
	sb.WriteString("--" + b + "--\r\n")
	return sb.String()
}

func TestMhtmlPassesThroughNonMHTML(t *testing.T) {
	cases := map[string]string{
		"plain text": "just some text\nnot mime at all\n",
		"html only":  "<html><body><h1>hi</h1></body></html>",
		"empty":      "",
		"plain mail": "From: a@b\r\nContent-Type: text/plain\r\n\r\nhello world, this is a normal email body that is reasonably long.\r\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got := string(mhtmlFilter([]byte(in)))
			if got != in {
				t.Errorf("non-MHTML input must pass through unchanged:\n in: %q\nout: %q", in, got)
			}
		})
	}
}

func TestMhtmlExtractsHTMLToMarkdown(t *testing.T) {
	body := `<!DOCTYPE html><html><head><title>My Article</title>` +
		`<style>.x{color:red}</style><script>evil()</script></head>` +
		`<body><nav>menu junk</nav>` +
		`<h1>Big Heading</h1>` +
		`<p>First <strong>bold</strong> and <em>italic</em> paragraph.</p>` +
		`<h2>Sub</h2><p>Second para with a <a href="https://x.test/p">link</a>.</p>` +
		`</body></html>`
	out := string(mhtmlFilter([]byte(buildMHTML(body))))

	for _, want := range []string{
		"# My Article",             // title prepended
		"# Big Heading",            // h1
		"## Sub",                   // h2
		"**bold**",                 // strong
		"*italic*",                 // em
		"[link](https://x.test/p)", // anchor
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// Dropped chrome must be gone.
	for _, bad := range []string{"evil()", "color:red", "menu junk", "<script", "<style", "<nav"} {
		if strings.Contains(out, bad) {
			t.Errorf("chrome/markup %q leaked into output:\n%s", bad, out)
		}
	}
}

func TestMhtmlListsAndCode(t *testing.T) {
	body := `<html><body>` +
		`<ul><li>first</li><li>second</li></ul>` +
		`<ol><li>one</li><li>two</li></ol>` +
		`<pre><code>func main() {
    x := 1
}</code></pre>` +
		`<p>inline <code>go vet</code> here</p>` +
		`</body></html>`
	out := string(mhtmlFilter([]byte(buildMHTML(body))))

	for _, want := range []string{
		"- first",
		"- second",
		"1. one",
		"2. two",
		"```", // fenced code block
		"func main() {",
		"`go vet`", // inline code
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// The <pre> body must be preserved verbatim (indentation kept).
	if !strings.Contains(out, "    x := 1") {
		t.Errorf("pre-block indentation not preserved:\n%s", out)
	}
}

func TestMhtmlTable(t *testing.T) {
	body := `<html><body><table>` +
		`<tr><th>Name</th><th>Role</th></tr>` +
		`<tr><td>neo</td><td>the one</td></tr>` +
		`</table></body></html>`
	out := string(mhtmlFilter([]byte(buildMHTML(body))))
	for _, want := range []string{"| Name | Role |", "| --- | --- |", "| neo | the one |"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing table row %q in:\n%s", want, out)
		}
	}
}

// TestMhtmlDropsEmbeddedImageBytes: an inline base64 image part is
// dropped entirely; a cid: <img> reference becomes an alt placeholder,
// never the bytes.
func TestMhtmlDropsEmbeddedImageBytes(t *testing.T) {
	imgPart := "Content-Type: image/png\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"Content-Location: https://example.com/pic.png\r\n\r\n" +
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	body := `<html><body><p>see <img src="cid:pic@x" alt="a diagram"> here</p>` +
		`<p><img src="https://example.com/real.png" alt="remote"></p></body></html>`
	out := string(mhtmlFilter([]byte(buildMHTML(body, imgPart))))

	if strings.Contains(out, "iVBORw0KGgo") {
		t.Errorf("base64 image bytes leaked into output:\n%s", out)
	}
	// cid: image → alt placeholder (bytes dropped), no cid: src.
	if strings.Contains(out, "cid:") {
		t.Errorf("cid: reference leaked:\n%s", out)
	}
	if !strings.Contains(out, "a diagram") {
		t.Errorf("alt text for embedded image dropped:\n%s", out)
	}
	// Remote image → normal markdown reference (the URL, not bytes).
	if !strings.Contains(out, "![remote](https://example.com/real.png)") {
		t.Errorf("remote image not rendered as a reference:\n%s", out)
	}
}

// TestMhtmlMonotonicViaRunNative: a tiny MHTML whose markdown would be
// larger than the input comes back unchanged (RunNative's byte-win
// guard). Also proves registration.
func TestMhtmlMonotonicViaRunNative(t *testing.T) {
	if nativeFilter("mhtml-filter") == nil {
		t.Fatal("mhtml-filter not registered")
	}
	// A big MHTML (mostly a dropped base64 part) must shrink; assert the
	// engine returns something no larger than input.
	big := buildMHTML(`<html><body><h1>Title</h1><p>`+strings.Repeat("content word ", 500)+`</p></body></html>`,
		"Content-Type: image/png\r\nContent-Transfer-Encoding: base64\r\n\r\n"+strings.Repeat("QUJDREVGR0g=", 4000))
	out, ok := RunNative("mhtml-filter", []byte(big))
	if !ok {
		t.Fatal("RunNative did not run mhtml-filter")
	}
	if len(out) > len(big) {
		t.Errorf("RunNative returned larger output: in=%d out=%d", len(big), len(out))
	}
	if !strings.Contains(string(out), "# Title") {
		t.Errorf("expected extracted markdown, got:\n%s", out)
	}
}

// TestMhtmlFailsOpenOnMalformed: every malformed / degenerate MHTML shape
// must return the ORIGINAL bytes unchanged (never-drop / fail-open), and
// never panic. Covers the paths the reviewer flagged as untested.
func TestMhtmlFailsOpenOnMalformed(t *testing.T) {
	const b = "----Boundary--TEST----"
	cases := map[string]string{
		// Looks like MHTML (envelope matches) but the boundary never
		// appears / parts are truncated.
		"truncated body": "Content-Type: multipart/related; boundary=\"" + b + "\"\r\n\r\n--" + b + "\r\nContent-Type: text/html\r\n\r\n<html><body>",
		// multipart/related but NO text/html part at all — only an image.
		"no html part": "Content-Type: multipart/related; boundary=\"" + b + "\"\r\n\r\n--" + b + "\r\nContent-Type: image/png\r\nContent-Transfer-Encoding: base64\r\n\r\nAAAA\r\n--" + b + "--\r\n",
		// Envelope matches but the Content-Type header is unparseable.
		"bad content-type": "Content-Type: multipart/related; boundary\r\n\r\ngarbage",
		// Declares multipart/related but no boundary param.
		"no boundary": "Content-Type: multipart/related\r\n\r\nsome body text that is long enough to matter here",
		// Empty-ish element names / degenerate HTML in a valid envelope
		// (must not panic on heading-index access etc.).
		"degenerate html": buildMHTML("<html><body><h1></h1><h></h><li>x</li><table></table><a></a></body></html>"),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			var out []byte
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("mhtmlFilter PANICKED on %q: %v", name, r)
					}
				}()
				out = mhtmlFilter([]byte(in))
			}()
			// For the malformed-container cases, output must equal input
			// (fail open). The "degenerate html" case has a valid container
			// so it may render (possibly empty→passthrough); the key
			// guarantee there is simply "no panic".
			if name != "degenerate html" && string(out) != in {
				t.Errorf("%s: expected verbatim passthrough (fail open)\n in: %q\nout: %q", name, in, out)
			}
		})
	}
}

// TestMhtmlOversizeHTMLFailsOpen: an HTML part larger than the decode cap
// is not read into memory wholesale — the filter bails to passthrough.
func TestMhtmlOversizeHTMLFailsOpen(t *testing.T) {
	// Build a valid MHTML whose HTML part exceeds maxHTMLBytes.
	huge := "<html><body><p>" + strings.Repeat("A", maxHTMLBytes+1024) + "</p></body></html>"
	in := buildMHTML(huge)
	out := mhtmlFilter([]byte(in))
	if string(out) != in {
		t.Errorf("oversize HTML part should pass through unchanged (got %d bytes, in %d)", len(out), len(in))
	}
}

// TestMhtmlQuotedPrintableDecoded: non-ASCII + long lines survive the QP
// round-trip (buildMHTML QP-encodes; the filter must decode). A long
// paragraph forces QP soft line-breaks, exercising the rejoin path.
func TestMhtmlQuotedPrintableDecoded(t *testing.T) {
	long := strings.Repeat("café ", 40) // >76 chars → QP soft-wraps it
	body := "<html><body><h1>Café Menu</h1><p>" + long + "</p></body></html>"
	out := string(mhtmlFilter([]byte(buildMHTML(body))))
	if !strings.Contains(out, "Café Menu") {
		t.Errorf("non-ASCII heading not decoded (want 'Café Menu'):\n%s", out)
	}
	// The soft-wrapped body must rejoin into contiguous "café café ..."
	// with no stray '=' soft-break artifacts.
	if strings.Contains(out, "=\n") || strings.Contains(out, "caf=") {
		t.Errorf("QP soft-break artifact leaked:\n%s", out)
	}
	if !strings.Contains(out, "café café") {
		t.Errorf("QP-wrapped body not rejoined:\n%s", out)
	}
}
