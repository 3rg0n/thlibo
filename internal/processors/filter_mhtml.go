package processors

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// mhtml-filter: distill an MHTML (.mhtml / .mht) web-page archive into
// Markdown for an AI client.
//
// MHTML is a MIME multipart/related archive of a saved web page: one
// text/html part plus every stylesheet, script, font, and image the page
// referenced, each a separate MIME part (images base64-encoded). On a
// real capture the embedded assets are ~90%+ of the bytes and an AI
// reading the article never needs any of them. This filter:
//
//  1. Parses the MIME container (stdlib mime/multipart).
//  2. Picks the primary text/html part, decoding quoted-printable/base64.
//  3. Walks the DOM (golang.org/x/net/html), dropping script/style/nav/
//     head chrome, and renders the content as Markdown — headings,
//     paragraphs, lists, links, code/pre, blockquotes, tables, images as
//     `![alt](src)` references (not the bytes).
//
// Everything else (CSS parts, base64 image/font parts) is discarded.
// Non-MHTML input passes through unchanged; the engine's monotonic guard
// (RunNative) returns the original if this somehow isn't smaller, and a
// panic in the HTML walk is recovered to the original input.

func init() { RegisterNative("mhtml-filter", mhtmlFilter) }

// mhtmlEnvelope recognises the MHTML container cheaply before doing any
// MIME work: a multipart/related body typed text/html, as written by
// Chromium ("Saved by Blink"), Edge, and "Save as .mht" in general.
var mhtmlEnvelope = regexp.MustCompile(`(?i)Content-Type:\s*multipart/related`)

func mhtmlFilter(raw []byte) []byte {
	// Cheap gate: must look like a MIME message whose top type is
	// multipart/related. Anything else passes through untouched.
	if !mhtmlEnvelope.Match(raw) {
		return raw
	}

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return raw
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return raw
	}
	boundary := params["boundary"]
	if boundary == "" {
		return raw
	}

	htmlBytes := extractPrimaryHTML(msg.Body, boundary)
	if len(htmlBytes) == 0 {
		return raw
	}

	doc, err := html.Parse(bytes.NewReader(htmlBytes))
	if err != nil {
		return raw
	}

	var md mdWriter
	md.walk(doc)
	out := md.result()
	if strings.TrimSpace(out) == "" {
		return raw
	}

	// Prepend a title line if the document had one.
	if title := findTitle(doc); title != "" {
		out = "# " + title + "\n\n" + out
	}
	return []byte(out)
}

// maxHTMLBytes caps how much of the text/html part we decode. A saved
// page's HTML is realistically a few MB at most; the multi-MB bulk of an
// MHTML file is the base64 asset parts, which we never read. The cap
// bounds memory against an adversarial / pathological part (a 100 MB+
// HTML body would otherwise allocate that much) — over the cap we bail to
// fail-open passthrough rather than read it all. Also bounds the total
// parts scanned so a "part bomb" can't spin us forever.
const (
	maxHTMLBytes = 32 << 20 // 32 MiB decoded HTML ceiling
	maxParts     = 5000     // stop scanning after this many MIME parts
)

// extractPrimaryHTML reads the multipart body and returns the decoded
// bytes of the first text/html part (the page itself). Later text/html
// parts (iframes) are ignored. Returns nil on any error or if no html
// part is found within maxParts — the caller then fails open.
func extractPrimaryHTML(body io.Reader, boundary string) []byte {
	mr := multipart.NewReader(body, boundary)
	for scanned := 0; scanned < maxParts; scanned++ {
		p, err := mr.NextPart()
		if err != nil {
			return nil
		}
		ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		if ct != "text/html" {
			continue
		}
		var r io.Reader = p
		switch strings.ToLower(strings.TrimSpace(p.Header.Get("Content-Transfer-Encoding"))) {
		case "quoted-printable":
			r = quotedprintable.NewReader(p)
			// base64 text/html is unusual; mail/multipart does not auto-
			// decode it, but Chromium/Edge always write quoted-printable
			// for the HTML part, so we only special-case that.
		}
		// Read at most maxHTMLBytes+1 so we can detect an over-cap part
		// and bail (fail open) instead of allocating an unbounded body.
		b, err := io.ReadAll(io.LimitReader(r, maxHTMLBytes+1))
		if err != nil {
			return nil
		}
		if len(b) > maxHTMLBytes {
			return nil // pathologically large HTML — pass through untouched
		}
		return b
	}
	return nil
}

// findTitle returns the trimmed <title> text, or "".
func findTitle(n *html.Node) string {
	var title string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if title != "" {
			return
		}
		if node.Type == html.ElementNode && node.DataAtom == atom.Title {
			if node.FirstChild != nil && node.FirstChild.Type == html.TextNode {
				title = strings.TrimSpace(node.FirstChild.Data)
			}
			return
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return title
}

// --- Markdown rendering ---

// mdWriter accumulates Markdown as it walks the DOM. It tracks list depth
// and whether we're inside a <pre> (verbatim) block.
type mdWriter struct {
	buf     strings.Builder
	listDep int
	inPre   bool
}

// skipContainers are elements whose entire subtree is dropped — they
// carry no article content an AI needs.
var skipContainers = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Head: true,
	atom.Nav: true, atom.Footer: true, atom.Noscript: true,
	atom.Svg: true, atom.Iframe: true, atom.Form: true,
	atom.Button: true,
}

func (w *mdWriter) walk(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		w.text(n.Data)
		return
	case html.ElementNode:
		if skipContainers[n.DataAtom] {
			return
		}
		w.element(n)
		return
	default:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			w.walk(c)
		}
	}
}

func (w *mdWriter) children(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.walk(c)
	}
}

func (w *mdWriter) element(n *html.Node) {
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		// n.Data is "h1".."h6" whenever the atom matched (html.Parse
		// guarantees it), but guard the index defensively rather than
		// depend on that invariant.
		if len(n.Data) < 2 || n.Data[1] < '1' || n.Data[1] > '6' {
			w.children(n)
			return
		}
		level := int(n.Data[1] - '0') // "h1".."h6"
		var sub mdWriter
		sub.children(n)
		heading := strings.TrimSpace(collapseWS(sub.buf.String()))
		if heading == "" {
			return
		}
		w.block()
		w.buf.WriteString(strings.Repeat("#", level) + " " + heading)
		w.block()
	case atom.P, atom.Div, atom.Section, atom.Article, atom.Main, atom.Header:
		w.block()
		w.children(n)
		w.block()
	case atom.Br:
		w.buf.WriteString("\n")
	case atom.Hr:
		w.block()
		w.buf.WriteString("---")
		w.block()
	case atom.Strong, atom.B:
		w.wrapInline(n, "**")
	case atom.Em, atom.I:
		w.wrapInline(n, "*")
	case atom.Code:
		if w.inPre {
			w.children(n) // inside <pre>, code is verbatim already
		} else {
			w.wrapInline(n, "`")
		}
	case atom.Pre:
		w.block()
		w.buf.WriteString("```\n")
		prev := w.inPre
		w.inPre = true
		w.children(n)
		w.inPre = prev
		if !strings.HasSuffix(w.buf.String(), "\n") {
			w.buf.WriteString("\n")
		}
		w.buf.WriteString("```")
		w.block()
	case atom.Blockquote:
		w.block()
		var sub mdWriter
		sub.children(n)
		for _, line := range strings.Split(strings.TrimRight(sub.buf.String(), "\n"), "\n") {
			w.buf.WriteString("> " + line + "\n")
		}
		w.block()
	case atom.Ul, atom.Ol:
		w.list(n)
	case atom.Li:
		w.children(n) // handled by list(); bare <li> falls through as text
	case atom.A:
		w.link(n)
	case atom.Img:
		w.image(n)
	case atom.Table:
		w.table(n)
	default:
		// Unknown/inline element: render its children.
		w.children(n)
	}
}

func (w *mdWriter) wrapInline(n *html.Node, marker string) {
	// Only emit markers if there's actual inner text.
	var sub mdWriter
	sub.inPre = w.inPre
	sub.children(n)
	inner := sub.buf.String()
	if strings.TrimSpace(inner) == "" {
		w.buf.WriteString(inner)
		return
	}
	w.buf.WriteString(marker + strings.TrimSpace(inner) + marker)
}

func (w *mdWriter) list(n *html.Node) {
	ordered := n.DataAtom == atom.Ol
	w.block()
	w.listDep++
	idx := 0
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.DataAtom != atom.Li {
			continue
		}
		idx++
		indent := strings.Repeat("  ", w.listDep-1)
		var marker string
		if ordered {
			marker = itoaMD(idx) + ". "
		} else {
			marker = "- "
		}
		var sub mdWriter
		sub.listDep = w.listDep
		sub.children(c)
		item := strings.TrimSpace(collapseWS(sub.buf.String()))
		if item == "" {
			continue
		}
		w.buf.WriteString(indent + marker + item + "\n")
	}
	w.listDep--
	w.block()
}

func (w *mdWriter) link(n *html.Node) {
	href := attr(n, "href")
	var sub mdWriter
	sub.inPre = w.inPre
	sub.children(n)
	text := strings.TrimSpace(collapseWS(sub.buf.String()))
	switch {
	case text == "" && href == "":
		return
	case href == "" || strings.HasPrefix(href, "cid:") || strings.HasPrefix(href, "javascript:"):
		w.buf.WriteString(text) // dead/internal link — keep the text only
	case text == "":
		w.buf.WriteString(href)
	default:
		w.buf.WriteString("[" + text + "](" + href + ")")
	}
}

func (w *mdWriter) image(n *html.Node) {
	// Reference the image, never the bytes. Skip cid: (embedded) srcs —
	// they're the base64 parts we're dropping; keep alt text if present.
	alt := strings.TrimSpace(attr(n, "alt"))
	src := attr(n, "src")
	if strings.HasPrefix(src, "cid:") || strings.HasPrefix(src, "data:") {
		src = ""
	}
	switch {
	case src == "" && alt == "":
		return
	case src == "":
		w.buf.WriteString("[image: " + alt + "]")
	default:
		w.buf.WriteString("![" + alt + "](" + src + ")")
	}
}

func (w *mdWriter) table(n *html.Node) {
	rows := collectRows(n)
	if len(rows) == 0 {
		return
	}
	w.block()
	for i, row := range rows {
		w.buf.WriteString("| " + strings.Join(row, " | ") + " |\n")
		if i == 0 {
			seps := make([]string, len(row))
			for j := range seps {
				seps[j] = "---"
			}
			w.buf.WriteString("| " + strings.Join(seps, " | ") + " |\n")
		}
	}
	w.block()
}

// collectRows extracts table cell text as [][]string (header row first if
// present). Cell content is flattened to single-line text.
func collectRows(table *html.Node) [][]string {
	var rows [][]string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Tr {
			var cells []string
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && (c.DataAtom == atom.Td || c.DataAtom == atom.Th) {
					var sub mdWriter
					sub.children(c)
					cells = append(cells, strings.TrimSpace(collapseWS(sub.buf.String())))
				}
			}
			if len(cells) > 0 {
				rows = append(rows, cells)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(table)
	return rows
}

// text emits a text node. Inside <pre> it's verbatim; otherwise
// whitespace is collapsed and pure-whitespace runs are dropped.
func (w *mdWriter) text(s string) {
	if w.inPre {
		w.buf.WriteString(s)
		return
	}
	if strings.TrimSpace(s) == "" {
		// Preserve a single separating space if we're mid-line.
		if cur := w.buf.String(); cur != "" && !strings.HasSuffix(cur, " ") && !strings.HasSuffix(cur, "\n") {
			w.buf.WriteString(" ")
		}
		return
	}
	w.buf.WriteString(collapseWS(s))
}

// block ensures the buffer ends with a blank-line separation, so the next
// block element starts cleanly. Collapses runs of newlines to at most 2.
func (w *mdWriter) block() {
	cur := w.buf.String()
	if cur == "" {
		return
	}
	cur = strings.TrimRight(cur, " \t")
	// Ensure exactly a paragraph break.
	if strings.HasSuffix(cur, "\n\n") {
		return
	}
	if strings.HasSuffix(cur, "\n") {
		w.buf.Reset()
		w.buf.WriteString(cur + "\n")
		return
	}
	w.buf.Reset()
	w.buf.WriteString(cur + "\n\n")
}

func (w *mdWriter) result() string {
	// Collapse 3+ newlines to 2; trim.
	out := multiNL.ReplaceAllString(w.buf.String(), "\n\n")
	return strings.TrimSpace(out) + "\n"
}

var multiNL = regexp.MustCompile(`\n{3,}`)
var wsRun = regexp.MustCompile(`[ \t\r\n]+`)

func collapseWS(s string) string { return wsRun.ReplaceAllString(s, " ") }

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func itoaMD(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
