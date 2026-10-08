package pdf

// textExtractor interprets one content stream (a page, or a Form XObject
// reached through Do) and collects the text it shows. The operators split
// into groups that each own a slice of this state — text state, text
// positioning, text showing, graphics state, XObjects, marked content —
// and each group is one method below.
type textExtractor struct {
	fonts     map[Name]Dict
	ft        *fontTables
	reader    *Reader
	resources Dict
	depth     int

	// Graphics state; q/Q save and restore it.
	ctm      [6]float64 // current transformation matrix
	fontSize float64
	fontName string
	tl       float64 // leading
	tc       float64 // character spacing
	tw       float64 // word spacing
	th       float64 // horizontal scaling (percentage)
	gsStack  []graphicsState

	// Text state, reset by BT. Zero (not identity) before the first BT.
	tm [6]float64 // text matrix
	lm [6]float64 // line matrix

	markedStack []markedEntry
	spans       []TextSpan
}

// graphicsState is what q saves and Q restores.
type graphicsState struct {
	ctm      [6]float64
	fontSize float64
	fontName string
	tc       float64
	tw       float64
	th       float64
	tl       float64
}

// markedEntry is one level of BMC/BDC marked content, kept for ActualText
// substitution and structure-tree binding.
type markedEntry struct {
	actualText string
	hasActual  bool
	startX     float64
	startY     float64
	suppress   bool // suppress glyph output when ActualText active
	mcid       int  // thlibo: /MCID from the BDC properties, or -1
}

var identityMatrix = [6]float64{1, 0, 0, 1, 0, 0}

// thlibo: currentMCID reports the innermost enclosing marked-content
// id. Nesting is legal (a /Span inside a /TD), and the structure tree
// references the innermost one, so we scan from the top of the stack
// down and take the first real id.
func (x *textExtractor) currentMCID() int {
	for i := len(x.markedStack) - 1; i >= 0; i-- {
		if x.markedStack[i].mcid >= 0 {
			return x.markedStack[i].mcid
		}
	}
	return -1
}

// advanceTextMatrix moves the text matrix past a shown string.
func (x *textExtractor) advanceTextMatrix(s string) {
	raw := []byte(s)
	hScale := x.th / 100.0
	var totalWidth float64
	if x.ft.composite[x.fontName] && len(raw)%2 == 0 {
		for i := 0; i+1 < len(raw); i += 2 {
			code := int(raw[i])<<8 | int(raw[i+1])
			w := x.ft.charWidth(x.fontName, code)
			totalWidth += (w*x.fontSize + x.tc) * hScale
		}
	} else {
		for _, b := range raw {
			w := x.ft.charWidth(x.fontName, int(b))
			totalWidth += (w*x.fontSize + x.tc) * hScale
			if b == ' ' {
				totalWidth += x.tw * hScale
			}
		}
	}
	x.tm[4] += totalWidth * x.tm[0]
	x.tm[5] += totalWidth * x.tm[1]
}

// transformPos applies the CTM to a text-space position.
func (x *textExtractor) transformPos(tx, ty float64) (float64, float64) {
	return x.ctm[0]*tx + x.ctm[2]*ty + x.ctm[4],
		x.ctm[1]*tx + x.ctm[3]*ty + x.ctm[5]
}

func (x *textExtractor) showString(s string) {
	decoded := x.ft.decode(x.fontName, s)
	if decoded == "" {
		return
	}
	px, py := x.transformPos(x.tm[4], x.tm[5])
	x.advanceTextMatrix(s)
	// Suppress glyph output when ActualText is active — the EMC handler
	// will emit the ActualText string instead.
	for _, m := range x.markedStack {
		if m.suppress {
			return
		}
	}
	endX, _ := x.transformPos(x.tm[4], x.tm[5])
	x.spans = append(x.spans, TextSpan{
		X:        px,
		Y:        py,
		EndX:     endX,
		FontSize: x.fontSize,
		Font:     x.fontName,
		Text:     decoded,
		MCID:     x.currentMCID(), // thlibo
	})
}

// nextLine moves to the start of the next line: 0 -TL Td (T*, ', ").
func (x *textExtractor) nextLine() {
	x.lm = matMul6(translateMatrix(0, -x.tl), x.lm)
	x.tm = x.lm
}

// matrixOperand reads the six operands of Tm or cm; ok is false when
// fewer than six are on the stack.
func matrixOperand(stack []any) ([6]float64, bool) {
	n := len(stack)
	if n < 6 {
		return [6]float64{}, false
	}
	return [6]float64{
		asFloat(stack[n-6]), asFloat(stack[n-5]),
		asFloat(stack[n-4]), asFloat(stack[n-3]),
		asFloat(stack[n-2]), asFloat(stack[n-1]),
	}, true
}
