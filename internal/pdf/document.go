package pdf

// Document represents an opened PDF file.
type Document struct {
	reader *Reader
	pages  []Dict
}

// thlibo: upstream's OpenFile(path) is dropped. Every caller here arrives
// with bytes already — a processor reads stdin, never a path — so the
// only thing a path-taking constructor added was a file-inclusion sink
// (gosec G304) for an argument no production code ever supplies.

// OpenBytes opens a PDF from raw bytes.
func OpenBytes(data []byte) (*Document, error) {
	r, err := Open(data)
	if err != nil {
		return nil, err
	}
	pages, err := r.Pages()
	if err != nil {
		return nil, err
	}
	return &Document{reader: r, pages: pages}, nil
}

// NumPages returns the number of pages in the document.
func (d *Document) NumPages() int {
	return len(d.pages)
}

// Page returns the page at index n (0-based).
func (d *Document) Page(n int) *Page {
	if n < 0 || n >= len(d.pages) {
		return nil
	}
	return &Page{dict: d.pages[n], reader: d.reader, num: n}
}

// Text extracts all text from all pages, joined by newlines.
func (d *Document) Text() (string, error) {
	var result string
	for i := range d.pages {
		p := d.Page(i)
		text, err := p.Text()
		if err != nil {
			return "", err
		}
		if i > 0 {
			result += "\n"
		}
		result += text
	}
	return result, nil
}

// thlibo: local additions. Document is the ergonomic entry point, but
// tagging and encryption are trailer/catalog properties that only Reader
// could see. Callers should not have to drop to Reader to ask whether a
// document is safe or better read from its tag tree.

// IsEncrypted reports whether the document declares /Encrypt. Nothing in
// this package decrypts, so an encrypted document extracts to
// ciphertext-shaped garbage with no error — callers must check.
func (d *Document) IsEncrypted() bool {
	return d.reader.IsEncrypted()
}

// IsTagged reports whether the document carries logical structure worth
// reading in preference to page geometry.
func (d *Document) IsTagged() bool {
	return d.reader.IsTagged()
}

// StructuredMarkdown renders the document's tag tree as Markdown,
// erroring when there is no usable tree so the caller falls through to
// geometry extraction.
func (d *Document) StructuredMarkdown() (string, error) {
	return d.reader.StructuredMarkdown()
}

// InfoString returns a decoded /Info entry (/Title, /Author, …), or ""
// when absent.
func (d *Document) InfoString(key Name) string {
	return d.reader.InfoString(key)
}

// Outline returns the document's bookmark tree, flattened depth-first.
func (d *Document) Outline() []OutlineItem {
	return d.reader.Outline()
}

// Page represents a single PDF page.
type Page struct {
	dict   Dict
	reader *Reader
	num    int
}

// TextSpans returns the raw positioned text spans on this page.
func (p *Page) TextSpans() ([]TextSpan, error) {
	spans := ExtractPageText(p.dict, p.reader)
	return spans, nil
}

// TextLines returns text grouped into spatial lines (sorted top-to-bottom).
func (p *Page) TextLines() ([]TextLine, error) {
	spans, err := p.TextSpans()
	if err != nil {
		return nil, err
	}
	return BuildLines(spans), nil
}

// Text returns the full text of the page as a single string.
func (p *Page) Text() (string, error) {
	lines, err := p.TextLines()
	if err != nil {
		return "", err
	}
	var result string
	for i, line := range lines {
		if i > 0 {
			result += "\n"
		}
		result += line.Text
	}
	return result, nil
}

// HasImages reports whether the page references an image XObject — the
// signal that separates a scanned page (no text, one big image) from a
// genuinely blank one.
//
// This is the cheap answer: it walks /Resources without parsing the content
// stream, so it says only that an image is available to paint. Use Images
// when the answer needs to depend on size or position.
func (p *Page) HasImages() bool {
	return p.reader.HasImages(p.dict)
}

// Images returns every image the page's content stream paints, in painting
// order, with placement resolved.
//
// thlibo: local addition. Unlike HasImages this parses the content stream,
// which is what makes the result actionable — /Resources says what a page
// *may* paint, not what it does or how big.
func (p *Page) Images() []ImageRef {
	return ExtractPageImages(p.dict, p.reader)
}

// ScanImage returns the single image that covers this page, or nil.
//
// This is the scanned-page test that matters for OCR: exactly one image
// painted over essentially the whole page. It returns nil for a page with
// several images (a photo collage, a page of figures) even when one of them
// is large, because compositing those is rasterization's job — the whole
// premise of extracting instead of rendering is that one image *is* the
// page. It also returns nil when the page has real text, since a text page
// with a full-bleed background image is not a scan and its text is already
// extractable losslessly.
func (p *Page) ScanImage() *ImageRef {
	spans, err := p.TextSpans()
	if err == nil && countTextRunes(spans) >= minScanPageTextRunes {
		return nil
	}

	mb := p.MediaBox()
	imgs := p.Images()
	var found *ImageRef
	for i := range imgs {
		if !imgs[i].CoversPage(mb) {
			continue
		}
		if found != nil {
			return nil // more than one full-page image: not a simple scan
		}
		found = &imgs[i]
	}
	return found
}

// minScanPageTextRunes is the text budget below which a page still counts
// as scanned. It is not zero because scanners and OCR-embedding producers
// routinely stamp a page number, a Bates number, or a "scanned by" footer
// onto an otherwise imaged page, and rejecting those would send real scans
// down the no-OCR path.
const minScanPageTextRunes = 16

func countTextRunes(spans []TextSpan) int {
	n := 0
	for _, s := range spans {
		for _, r := range s.Text {
			if r != ' ' && r != '\n' && r != '\t' {
				n++
			}
		}
	}
	return n
}

// Tables auto-detects all tables on this page.
func (p *Page) Tables() ([]Table, error) {
	spans, err := p.TextSpans()
	if err != nil {
		return nil, err
	}
	return FindTables(spans, nil), nil
}

// FindTable detects a table on this page using the given options.
func (p *Page) FindTable(opts *TableOpts) (*Table, error) {
	spans, err := p.TextSpans()
	if err != nil {
		return nil, err
	}
	return FindTable(spans, opts), nil
}

// Rotation returns the page rotation in degrees (0, 90, 180, 270).
func (p *Page) Rotation() int {
	r, _ := p.dict.Int("Rotate")
	return r
}

// MediaBox returns the page media box [llx, lly, urx, ury].
func (p *Page) MediaBox() [4]float64 {
	mb, ok := p.dict.Array("MediaBox")
	if !ok || len(mb) < 4 {
		return [4]float64{0, 0, 612, 792} // default US Letter
	}
	return [4]float64{asFloat(mb[0]), asFloat(mb[1]), asFloat(mb[2]), asFloat(mb[3])}
}
