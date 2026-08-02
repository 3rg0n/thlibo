package pdf

// thlibo: local addition. Tests for content-stream image extraction.
//
// The distinction under test throughout is placement, because that is the
// only thing this walker adds over the resource-dictionary scan HasImages
// already did. A test that merely counts images would pass with a broken
// CTM, and a broken CTM is precisely what makes a logo look like a scan.

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// buildImagePDF assembles a one-page PDF whose content stream is given
// verbatim, with the supplied XObject dicts wired into /Resources.
//
// Built by hand for the same reason helper_test.go's buildPDF is: a fixture
// builder that shares code with the parser under test can hide bugs in both.
func buildImagePDF(t testing.TB, content string, xobjects map[string]string) []byte {
	t.Helper()

	// 1=Catalog, 2=Pages, 3=Page, 4=Contents, 5+=XObjects.
	var names []string
	for n := range xobjects {
		names = append(names, n)
	}
	// Stable order so object numbers are deterministic across runs.
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}

	var resParts []string
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"", // page, filled in below once /Resources is known
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
	}
	for i, n := range names {
		num := 5 + i
		resParts = append(resParts, fmt.Sprintf("/%s %d 0 R", n, num))
		objs = append(objs, xobjects[n])
	}

	res := "<< /Font << >> >>"
	if len(resParts) > 0 {
		res = fmt.Sprintf("<< /XObject << %s >> >>", strings.Join(resParts, " "))
	}
	objs[2] = fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "+
		"/Resources %s /Contents 4 0 R >>", res)

	var b strings.Builder
	b.WriteString("%PDF-1.7\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objs)+1, xref)
	return []byte(b.String())
}

// imageXObject returns a minimal image XObject dict with `n` bytes of data.
func imageXObject(w, h int, extra string) string {
	data := strings.Repeat("\x00", w*h)
	return fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d "+
		"/BitsPerComponent 8 /ColorSpace /DeviceGray %s /Length %d >>\nstream\n%s\nendstream",
		w, h, extra, len(data), data)
}

func openPage(t testing.TB, data []byte) *Page {
	t.Helper()
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	p := doc.Page(0)
	if p == nil {
		t.Fatal("no page 0")
	}
	return p
}

// TestPageImagesReportsPlacement is the core assertion: the CTM in force at
// the Do operator, not the resource entry, decides where an image lands.
func TestPageImagesReportsPlacement(t *testing.T) {
	// Paint a 100x50 image into a 200x100 box at (50, 300).
	content := "q 200 0 0 100 50 300 cm /Im0 Do Q"
	data := buildImagePDF(t, content, map[string]string{"Im0": imageXObject(100, 50, "")})

	imgs := openPage(t, data).Images()
	if len(imgs) != 1 {
		t.Fatalf("got %d images, want 1", len(imgs))
	}
	im := imgs[0]

	if im.Name != "Im0" {
		t.Errorf("name %q, want Im0", im.Name)
	}
	if w, h := im.PixelSize(); w != 100 || h != 50 {
		t.Errorf("pixel size %dx%d, want 100x50", w, h)
	}
	x, y, w, h := im.Bounds()
	assertClose(t, "x", x, 50)
	assertClose(t, "y", y, 300)
	assertClose(t, "width", w, 200)
	assertClose(t, "height", h, 100)
}

// TestImageBoundsHandlesRotationAndMirroring pins the four-corner transform.
//
// The naive version transforms only (0,0) and (1,1), which reports a
// negative width for a mirrored CTM and a transposed box for a rotated one —
// and a negative width silently fails every CoversPage check.
//
// The mirrored and quarter-turn cases below do *not* on their own justify
// four corners: both map opposite corners to opposite corners, so min/max
// over two of them lands on the same answer. The 45-degree case is what
// separates the two implementations, which is why it is here.
func TestImageBoundsHandlesRotationAndMirroring(t *testing.T) {
	for _, tc := range []struct {
		name         string
		ctm          [6]float64
		wantX, wantY float64
		wantW, wantH float64
	}{
		{
			name:  "upright",
			ctm:   [6]float64{100, 0, 0, 50, 10, 20},
			wantX: 10, wantY: 20, wantW: 100, wantH: 50,
		},
		{
			// Negative scale: the image is flipped, and the box extends
			// left of the origin. A two-corner implementation reports
			// width -100.
			name:  "mirrored horizontally",
			ctm:   [6]float64{-100, 0, 0, 50, 200, 20},
			wantX: 100, wantY: 20, wantW: 100, wantH: 50,
		},
		{
			// 90-degree rotation: a 100-wide, 50-tall placement occupies a
			// 50-wide, 100-tall box.
			name:  "rotated 90 degrees",
			ctm:   [6]float64{0, 100, -50, 0, 100, 0},
			wantX: 50, wantY: 0, wantW: 50, wantH: 100,
		},
		{
			name:  "flipped vertically",
			ctm:   [6]float64{100, 0, 0, -50, 0, 50},
			wantX: 0, wantY: 0, wantW: 100, wantH: 50,
		},
		{
			// A 45-degree rotation of a 100pt square. Unlike a quarter turn,
			// this maps the unit square's corners off the axes, so (0,0) and
			// (1,1) both land on the vertical centre line: the two-corner
			// version reports width 0 here, and a zero width fails every
			// CoversPage check silently.
			name:  "rotated 45 degrees",
			ctm:   [6]float64{70.71068, 70.71068, -70.71068, 70.71068, 100, 100},
			wantX: 29.289, wantY: 100, wantW: 141.421, wantH: 141.421,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			im := ImageRef{CTM: tc.ctm}
			x, y, w, h := im.Bounds()
			assertClose(t, "x", x, tc.wantX)
			assertClose(t, "y", y, tc.wantY)
			assertClose(t, "width", w, tc.wantW)
			assertClose(t, "height", h, tc.wantH)
		})
	}
}

// TestCoversPageDistinguishesScanFromDecoration is the test that justifies
// the whole walker: a logo and a scan both appear in /Resources/XObject, and
// only placement tells them apart.
func TestCoversPageDistinguishesScanFromDecoration(t *testing.T) {
	letter := [4]float64{0, 0, 612, 792}

	scan := ImageRef{CTM: [6]float64{612, 0, 0, 792, 0, 0}}
	if !scan.CoversPage(letter) {
		t.Error("a full-page image should cover the page")
	}

	// Real scans are routinely inset a few points; 95% must still count.
	inset := ImageRef{CTM: [6]float64{581, 0, 0, 752, 15, 20}}
	if !inset.CoversPage(letter) {
		t.Error("a slightly inset scan should still cover the page")
	}

	logo := ImageRef{CTM: [6]float64{72, 0, 0, 36, 40, 730}}
	if logo.CoversPage(letter) {
		t.Error("a corner logo must not count as covering the page")
	}

	// A full-width banner is not a scan: it fails on height.
	banner := ImageRef{CTM: [6]float64{612, 0, 0, 80, 0, 700}}
	if banner.CoversPage(letter) {
		t.Error("a full-width banner must not count as covering the page")
	}

	// A degenerate MediaBox must not divide by zero into a true answer.
	if scan.CoversPage([4]float64{0, 0, 0, 0}) {
		t.Error("a zero-size MediaBox cannot be covered")
	}
}

// TestPageImagesFollowsFormXObjects checks the form /Matrix is composed into
// the CTM. Producers routinely wrap a scan in a form, so a walker that stops
// at the form boundary finds no images at all on those files — and one that
// enters but ignores /Matrix reports the wrong size.
func TestPageImagesFollowsFormXObjects(t *testing.T) {
	// The form scales its contents 2x; inside, the image is painted into a
	// 100x50 unit box. Net placement: 200x100 at (20, 40).
	inner := "q 100 0 0 50 10 20 cm /Im0 Do Q"
	form := fmt.Sprintf("<< /Type /XObject /Subtype /Form /Matrix [2 0 0 2 0 0] "+
		"/Resources << /XObject << /Im0 6 0 R >> >> /Length %d >>\nstream\n%s\nendstream",
		len(inner), inner)

	data := buildImagePDF(t, "/Fm0 Do", map[string]string{
		"Fm0": form,
		"Im0": imageXObject(100, 50, ""),
	})

	imgs := openPage(t, data).Images()
	if len(imgs) != 1 {
		t.Fatalf("got %d images, want 1 (form not followed?)", len(imgs))
	}
	x, y, w, h := imgs[0].Bounds()
	assertClose(t, "x", x, 20)
	assertClose(t, "y", y, 40)
	assertClose(t, "width", w, 200)
	assertClose(t, "height", h, 100)
}

// TestPageImagesRestoresCTMOnQ confirms q/Q bracketing. Without the restore,
// each cm compounds into the next image's placement and every image after
// the first lands somewhere else entirely.
func TestPageImagesRestoresCTMOnQ(t *testing.T) {
	content := "q 100 0 0 100 0 0 cm /Im0 Do Q q 50 0 0 50 200 200 cm /Im0 Do Q"
	data := buildImagePDF(t, content, map[string]string{"Im0": imageXObject(10, 10, "")})

	imgs := openPage(t, data).Images()
	if len(imgs) != 2 {
		t.Fatalf("got %d images, want 2", len(imgs))
	}
	x0, y0, w0, _ := imgs[0].Bounds()
	assertClose(t, "first x", x0, 0)
	assertClose(t, "first y", y0, 0)
	assertClose(t, "first width", w0, 100)

	x1, y1, w1, _ := imgs[1].Bounds()
	assertClose(t, "second x", x1, 200)
	assertClose(t, "second y", y1, 200)
	assertClose(t, "second width", w1, 50)
}

// TestPageImagesIgnoresUnpaintedResources is the difference between this
// walker and HasImages stated as a test: an XObject listed in /Resources but
// never painted is not on the page.
func TestPageImagesIgnoresUnpaintedResources(t *testing.T) {
	data := buildImagePDF(t, "q 10 0 0 10 0 0 cm Q", map[string]string{
		"Im0": imageXObject(10, 10, ""),
	})
	p := openPage(t, data)

	if !p.HasImages() {
		t.Error("HasImages should still see the resource entry")
	}
	if imgs := p.Images(); len(imgs) != 0 {
		t.Errorf("got %d painted images, want 0 — the content stream never calls Do", len(imgs))
	}
}

// TestPageImagesReadsInlineImages covers BI/ID/EI, which the text walker
// skipped past and discarded.
func TestPageImagesReadsInlineImages(t *testing.T) {
	// A 2x2 8-bpc grey inline image, 4 bytes of data.
	content := "q 100 0 0 100 10 10 cm BI /W 2 /H 2 /BPC 8 /CS /G ID \x01\x02\x03\x04 EI Q"
	data := buildImagePDF(t, content, nil)

	imgs := openPage(t, data).Images()
	if len(imgs) != 1 {
		t.Fatalf("got %d images, want 1", len(imgs))
	}
	im := imgs[0]
	if !im.Inline {
		t.Error("Inline should be true")
	}
	if w, h := im.PixelSize(); w != 2 || h != 2 {
		t.Errorf("pixel size %dx%d, want 2x2 — the /W and /H abbreviations must expand", w, h)
	}
	if cs, _ := im.Stream.Dict.Name("ColorSpace"); cs != "DeviceGray" {
		t.Errorf("colour space %q, want DeviceGray — /G must expand", cs)
	}
	if bpc, _ := im.Stream.Dict.Int("BitsPerComponent"); bpc != 8 {
		t.Errorf("bpc %d, want 8 — /BPC must expand", bpc)
	}
	x, y, w, _ := im.Bounds()
	assertClose(t, "x", x, 10)
	assertClose(t, "y", y, 10)
	assertClose(t, "width", w, 100)

	img, err := im.Decode()
	if err != nil {
		t.Fatalf("decoding the inline image: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 2 || b.Dy() != 2 {
		t.Errorf("decoded bounds %v, want 2x2", b)
	}
}

// TestInlineImageDoesNotDerailTheTokenizer is the load-bearing test for the
// shared BI scanner.
//
// Inline image data is arbitrary binary that can contain anything a
// tokenizer recognises — here a `Q`, a `/Name` and even the bytes `Do`. If
// the scan does not land past EI, those get read as operators: the CTM stack
// unwinds wrongly and the operator that follows is attributed to the wrong
// state. The text extractor has the same exposure, which is why the two
// share one implementation.
func TestInlineImageDoesNotDerailTheTokenizer(t *testing.T) {
	// 8 bytes of hostile-looking data, then a real image after it.
	content := "BI /W 4 /H 2 /BPC 8 /CS /G ID Q /Im0 Do EI q 300 0 0 300 5 5 cm /Im0 Do Q"
	data := buildImagePDF(t, content, map[string]string{"Im0": imageXObject(4, 4, "")})

	imgs := openPage(t, data).Images()
	if len(imgs) != 2 {
		t.Fatalf("got %d images, want 2 (inline + XObject) — the EI scan lost sync", len(imgs))
	}
	if !imgs[0].Inline {
		t.Error("first image should be the inline one")
	}
	if imgs[1].Inline {
		t.Error("second image should be the XObject, not another inline")
	}
	// The placement of the trailing image proves the operator stream stayed
	// in sync: a lost EI scan would have consumed the cm.
	x, y, w, _ := imgs[1].Bounds()
	assertClose(t, "trailing image x", x, 5)
	assertClose(t, "trailing image y", y, 5)
	assertClose(t, "trailing image width", w, 300)
}

// TestInlineImageHonoursDeclaredLength covers the /L fast path, including
// the case that makes it necessary: data containing the byte sequence " EI "
// where the scan would stop early.
func TestInlineImageHonoursDeclaredLength(t *testing.T) {
	// 8 bytes whose middle spells " EI " — a scan without /L truncates here.
	payload := "ab EI cd"
	content := fmt.Sprintf("BI /W 4 /H 2 /BPC 8 /CS /G /L %d ID %s EI", len(payload), payload)
	data := buildImagePDF(t, content, nil)

	imgs := openPage(t, data).Images()
	if len(imgs) != 1 {
		t.Fatalf("got %d images, want 1", len(imgs))
	}
	if got := len(imgs[0].Stream.Data); got != len(payload) {
		t.Errorf("data is %d bytes, want %d — the declared /L was not used", got, len(payload))
	}
}

// TestInlineImageIgnoresWrongDeclaredLength confirms /L is verified rather
// than trusted: a length that does not land on EI is file-controlled
// misinformation, and following it would leave the lexer inside pixel data.
func TestInlineImageIgnoresWrongDeclaredLength(t *testing.T) {
	t.Run("out of range", func(t *testing.T) {
		content := "BI /W 4 /H 1 /BPC 8 /CS /G /L 9999 ID \x01\x02\x03\x04 EI q 10 0 0 10 0 0 cm /Im0 Do Q"
		data := buildImagePDF(t, content, map[string]string{"Im0": imageXObject(4, 4, "")})

		imgs := openPage(t, data).Images()
		if len(imgs) != 2 {
			t.Fatalf("got %d images, want 2 — a bogus /L must fall back to the EI scan", len(imgs))
		}
	})

	t.Run("in range but not landing on EI", func(t *testing.T) {
		// This is the case the range check alone does not catch, and the one
		// that costs sync: /L 2 is inside the buffer, so a decoder that
		// trusts it leaves the lexer at byte 2 — inside pixel data that here
		// spells `/Im0 Do`, which then reads as a real paint operation. The
		// declared length must be verified against EI, not merely bounded.
		payload := "\x01\x02/Im0 Do\x03"
		content := fmt.Sprintf("BI /W 4 /H 2 /BPC 8 /CS /G /L 2 ID %s EI "+
			"q 10 0 0 10 0 0 cm /Im0 Do Q", payload)
		data := buildImagePDF(t, content, map[string]string{"Im0": imageXObject(4, 4, "")})

		imgs := openPage(t, data).Images()
		if len(imgs) != 2 {
			t.Fatalf("got %d images, want 2 (inline + XObject) — an unverified /L "+
				"left the lexer inside the pixel data", len(imgs))
		}
		if got := len(imgs[0].Stream.Data); got != len(payload) {
			t.Errorf("inline data is %d bytes, want %d — the wrong /L was trusted",
				got, len(payload))
		}
	})
}

// TestScanImageIdentifiesScannedPages exercises the OCR decision itself.
func TestScanImageIdentifiesScannedPages(t *testing.T) {
	t.Run("full page image with no text is a scan", func(t *testing.T) {
		data := buildImagePDF(t, "q 612 0 0 792 0 0 cm /Im0 Do Q",
			map[string]string{"Im0": imageXObject(60, 80, "")})
		if openPage(t, data).ScanImage() == nil {
			t.Error("want a scan")
		}
	})

	t.Run("corner logo is not a scan", func(t *testing.T) {
		data := buildImagePDF(t, "q 72 0 0 36 40 730 cm /Im0 Do Q",
			map[string]string{"Im0": imageXObject(10, 10, "")})
		if im := openPage(t, data).ScanImage(); im != nil {
			t.Error("a logo is not a scan")
		}
	})

	t.Run("two full-page images are not a simple scan", func(t *testing.T) {
		content := "q 612 0 0 792 0 0 cm /Im0 Do Q q 612 0 0 792 0 0 cm /Im1 Do Q"
		data := buildImagePDF(t, content, map[string]string{
			"Im0": imageXObject(10, 10, ""),
			"Im1": imageXObject(10, 10, ""),
		})
		if openPage(t, data).ScanImage() != nil {
			t.Error("compositing two full-page images is rasterization's job")
		}
	})

	t.Run("full page image under real text is not a scan", func(t *testing.T) {
		// A background image with extractable body text over it: the text is
		// already lossless, so OCR would be a downgrade.
		content := "q 612 0 0 792 0 0 cm /Im0 Do Q\n" +
			"BT /F1 12 Tf 72 700 Td (This page has plenty of real extractable text) Tj ET"
		data := buildImagePDF(t, content, map[string]string{"Im0": imageXObject(10, 10, "")})
		if openPage(t, data).ScanImage() != nil {
			t.Error("a text page with a background image is not a scan")
		}
	})

	t.Run("a stamped page number does not disqualify a scan", func(t *testing.T) {
		// Scanners routinely stamp a page or Bates number onto an imaged
		// page; rejecting those would send real scans down the no-OCR path.
		content := "q 612 0 0 792 0 0 cm /Im0 Do Q\nBT /F1 8 Tf 300 20 Td (12) Tj ET"
		data := buildImagePDF(t, content, map[string]string{"Im0": imageXObject(10, 10, "")})
		if openPage(t, data).ScanImage() == nil {
			t.Error("a page-number stamp should not disqualify a scan")
		}
	})
}

// TestPageImagesTerminatesOnSelfReferentialForm proves the cycle guard.
//
// Termination alone does not prove it: maxXObjectDepth caps recursion at 8
// regardless, so a self-referential form finishes either way. What the
// visited set actually buys is bounding the *fan-out* — a form that paints
// itself twice per level expands 2^depth, so the depth cap alone leaves 2^8
// = 256 traversals of a subtree the file declared once. Asserting the image
// count is what makes the guard observable.
func TestPageImagesTerminatesOnSelfReferentialForm(t *testing.T) {
	// The form paints one image and re-enters itself twice: exponential
	// without the visited set, one image with it.
	inner := "q 10 0 0 10 0 0 cm /Im0 Do Q /Fm0 Do /Fm0 Do"
	form := fmt.Sprintf("<< /Type /XObject /Subtype /Form "+
		"/Resources << /XObject << /Fm0 5 0 R /Im0 6 0 R >> >> /Length %d >>\nstream\n%s\nendstream",
		len(inner), inner)
	data := buildImagePDF(t, "/Fm0 Do", map[string]string{
		"Fm0": form,
		"Im0": imageXObject(10, 10, ""),
	})

	got := make(chan int, 1)
	go func() { got <- len(openPage(t, data).Images()) }()
	select {
	case n := <-got:
		if n != 1 {
			t.Errorf("got %d images, want 1 — the form is entered once, "+
				"and a depth cap alone lets it expand exponentially", n)
		}
	case <-timeoutAfter():
		t.Fatal("self-referential form did not terminate")
	}
}

// TestPageImagesRepeatedFormIsNotACycle guards against over-correcting: the
// same form legitimately appears twice on a page (a repeated header), and a
// visited set held for the whole walk would drop the second one.
func TestPageImagesRepeatedFormIsNotACycle(t *testing.T) {
	inner := "q 100 0 0 50 0 0 cm /Im0 Do Q"
	form := fmt.Sprintf("<< /Type /XObject /Subtype /Form "+
		"/Resources << /XObject << /Im0 6 0 R >> >> /Length %d >>\nstream\n%s\nendstream",
		len(inner), inner)
	content := "q 1 0 0 1 0 700 cm /Fm0 Do Q q 1 0 0 1 0 100 cm /Fm0 Do Q"
	data := buildImagePDF(t, content, map[string]string{
		"Fm0": form,
		"Im0": imageXObject(10, 10, ""),
	})

	imgs := openPage(t, data).Images()
	if len(imgs) != 2 {
		t.Fatalf("got %d images, want 2 — a form used twice is not a cycle", len(imgs))
	}
	_, y0, _, _ := imgs[0].Bounds()
	_, y1, _, _ := imgs[1].Bounds()
	assertClose(t, "first placement y", y0, 700)
	assertClose(t, "second placement y", y1, 100)
}

// TestResolveColorSpaceFollowsResourceNames covers the form that is easiest
// to miss: /CS0 is not a colour space, it is a key into the page's
// /Resources/ColorSpace dict. Missing it makes an ordinary ICCBased RGB
// image read as unsupported.
func TestResolveColorSpaceFollowsResourceNames(t *testing.T) {
	r := &Reader{cache: map[int]any{}}
	profile := &Stream{Dict: Dict{"N": 3}}
	resources := Dict{
		"ColorSpace": Dict{"CS0": Array{Name("ICCBased"), profile}},
	}

	got := r.resolveColorSpace(Name("CS0"), resources)
	arr, ok := got.(Array)
	if !ok || len(arr) != 2 {
		t.Fatalf("got %T (%v), want a 2-element ICCBased array", got, got)
	}
	if fam, _ := arr[0].(Name); fam != "ICCBased" {
		t.Errorf("family %v, want ICCBased", arr[0])
	}

	// A device name must NOT be looked up as a resource key, even when a
	// same-named entry exists — that would shadow the real device space.
	shadow := Dict{"ColorSpace": Dict{"DeviceRGB": Array{Name("ICCBased"), profile}}}
	if got := r.resolveColorSpace(Name("DeviceRGB"), shadow); got != Name("DeviceRGB") {
		t.Errorf("got %v, want the device space DeviceRGB unshadowed", got)
	}
}

func assertClose(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.01 {
		t.Errorf("%s = %g, want %g", what, got, want)
	}
}
