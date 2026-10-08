package pdf

// run interprets the content stream: operands accumulate on a stack, and
// each operator consumes it and clears it.
func (x *textExtractor) run(lex *Lexer) {
	var stack []any
	for {
		tok, err := lex.NextToken()
		if err != nil || tok.Type == TEOF {
			break
		}

		// If it's an operand, push to stack.
		switch tok.Type {
		case TNumber:
			if tok.IsInt {
				stack = append(stack, tok.Int)
			} else {
				stack = append(stack, tok.Num)
			}
			continue
		case TString, THexString:
			stack = append(stack, tok.Str)
			continue
		case TName:
			stack = append(stack, Name(tok.Str))
			continue
		case TArrayStart:
			stack = append(stack, parseInlineArray(lex))
			continue
		case TDictStart:
			// thlibo: inline dicts used to be skipped outright. BDC's
			// properties operand is an inline dict, so discarding it made
			// /MCID unreachable and the structure tree unbindable.
			stack = append(stack, parseInlineDict(lex, 0))
			continue
		}

		if tok.Type != TKeyword {
			continue
		}

		switch op := tok.Str; op {
		case "Tf", "Tc", "Tw", "TL", "Th", "Tz":
			x.textState(op, stack)
		case "BT", "Td", "TD", "Tm", "T*":
			x.textPosition(op, stack)
		case "Tj", "'", "\"", "TJ":
			x.textShow(op, stack)
		case "q", "Q", "cm":
			x.graphicsStateOp(op, stack)
		case "Do":
			x.doXObject(stack)
		case "BMC", "BDC", "EMC":
			x.markedContent(op, stack)
		case "BI":
			skipInlineImage(lex)
		}
		// ET and every non-text operator are no-ops here.

		stack = stack[:0] // clear stack after each operator
	}
}

// textState handles the text-state operators (PDF 32000 §9.3).
func (x *textExtractor) textState(op string, stack []any) {
	n := len(stack)
	if op == "Tf" {
		// /FontName size Tf
		if n >= 2 {
			x.fontSize = asFloat(stack[n-1])
			if name, ok := stack[n-2].(Name); ok {
				x.fontName = string(name)
			}
		}
		return
	}
	if n < 1 {
		return
	}
	v := asFloat(stack[n-1])
	switch op {
	case "Tc":
		x.tc = v
	case "Tw":
		x.tw = v
	case "TL":
		x.tl = v
	case "Th", "Tz":
		x.th = v
	}
}

// textPosition handles BT and the text-positioning operators (§9.4.2).
func (x *textExtractor) textPosition(op string, stack []any) {
	n := len(stack)
	switch op {
	case "BT":
		x.tm = identityMatrix
		x.lm = identityMatrix

	case "Td", "TD":
		// tx ty Td — move to next line. TD is the same, preceded by -ty TL.
		if n >= 2 {
			tx := asFloat(stack[n-2])
			ty := asFloat(stack[n-1])
			if op == "TD" {
				x.tl = -ty
			}
			x.lm = matMul6(translateMatrix(tx, ty), x.lm)
			x.tm = x.lm
		}

	case "Tm":
		// a b c d e f Tm — set text matrix directly.
		if m, ok := matrixOperand(stack); ok {
			x.tm = m
			x.lm = m
		}

	case "T*":
		x.nextLine()
	}
}

// textShow handles the text-showing operators (§9.4.3).
func (x *textExtractor) textShow(op string, stack []any) {
	n := len(stack)
	switch op {
	case "Tj":
		if n >= 1 {
			if s, ok := stack[n-1].(string); ok {
				x.showString(s)
			}
		}

	case "'":
		// T* then Tj.
		x.nextLine()
		if n >= 1 {
			if s, ok := stack[n-1].(string); ok {
				x.showString(s)
			}
		}

	case "\"":
		// aw ac string " — set word/char spacing, T*, Tj.
		if n >= 3 {
			x.tw = asFloat(stack[n-3])
			x.tc = asFloat(stack[n-2])
			x.nextLine()
			if s, ok := stack[n-1].(string); ok {
				x.showString(s)
			}
		}

	case "TJ":
		// Array of strings and positioning adjustments.
		if n >= 1 {
			if arr, ok := stack[n-1].(Array); ok {
				for _, item := range arr {
					switch v := item.(type) {
					case string:
						x.showString(v)
					case int:
						// Displacement in thousandths of a unit of text space.
						x.tm[4] -= float64(v) / 1000.0 * x.fontSize * (x.th / 100.0)
					case float64:
						x.tm[4] -= v / 1000.0 * x.fontSize * (x.th / 100.0)
					}
				}
			}
		}
	}
}

// graphicsStateOp handles q, Q and cm (§8.4.4).
func (x *textExtractor) graphicsStateOp(op string, stack []any) {
	switch op {
	case "q":
		x.gsStack = append(x.gsStack, graphicsState{
			ctm: x.ctm, fontSize: x.fontSize, fontName: x.fontName,
			tc: x.tc, tw: x.tw, th: x.th, tl: x.tl,
		})

	case "Q":
		if len(x.gsStack) > 0 {
			gs := x.gsStack[len(x.gsStack)-1]
			x.gsStack = x.gsStack[:len(x.gsStack)-1]
			x.ctm = gs.ctm
			x.fontSize = gs.fontSize
			x.fontName = gs.fontName
			x.tc = gs.tc
			x.tw = gs.tw
			x.th = gs.th
			x.tl = gs.tl
		}

	case "cm":
		if m, ok := matrixOperand(stack); ok {
			x.ctm = matMul6(m, x.ctm)
		}
	}
}

// doXObject recurses into a Form XObject, bounded by the depth limit in
// extractTextWithResources, and maps its spans through the form's CTM.
func (x *textExtractor) doXObject(stack []any) {
	if len(stack) < 1 || x.resources == nil || x.reader == nil {
		return
	}
	xobjName, ok := stack[len(stack)-1].(Name)
	if !ok {
		return
	}
	xobjDict, _ := x.reader.ResolveDict(x.resources["XObject"])
	if xobjDict == nil {
		return
	}
	stream, ok := x.reader.Resolve(xobjDict[xobjName]).(*Stream)
	if !ok {
		return
	}
	if subtype, _ := stream.Dict.Name("Subtype"); subtype != "Form" {
		return
	}
	// Get Form's resources (fall back to page resources).
	formFonts := x.reader.fontsFromDict(stream.Dict)
	if len(formFonts) == 0 {
		formFonts = x.fonts
	}
	// Apply Form's Matrix if present.
	formCTM := x.ctm
	if mArr, ok := stream.Dict.Array("Matrix"); ok && len(mArr) == 6 {
		fm := [6]float64{
			asFloat(mArr[0]), asFloat(mArr[1]),
			asFloat(mArr[2]), asFloat(mArr[3]),
			asFloat(mArr[4]), asFloat(mArr[5]),
		}
		formCTM = matMul6(fm, x.ctm)
	}
	formResources, _ := x.reader.ResolveDict(stream.Dict["Resources"])
	formSpans := extractTextWithResources(stream.Data, formFonts, x.reader, formResources, x.depth+1)
	// Transform form spans through the form's CTM.
	for i := range formSpans {
		fx := formCTM[0]*formSpans[i].X + formCTM[2]*formSpans[i].Y + formCTM[4]
		fy := formCTM[1]*formSpans[i].X + formCTM[3]*formSpans[i].Y + formCTM[5]
		formSpans[i].X = fx
		formSpans[i].Y = fy
	}
	x.spans = append(x.spans, formSpans...)
}

// markedContent handles BMC, BDC and EMC (§14.6): ActualText replaces the
// glyphs it spans, and /MCID binds content to the structure tree.
func (x *textExtractor) markedContent(op string, stack []any) {
	switch op {
	case "BMC":
		// Begin marked content (no properties).
		x.markedStack = append(x.markedStack, markedEntry{mcid: -1})

	case "BDC":
		// Begin marked content with properties dict.
		entry := markedEntry{mcid: -1}
		if len(stack) >= 2 {
			if props, ok := stack[len(stack)-1].(Dict); ok {
				if at, ok := props.String("ActualText"); ok {
					entry.actualText = decodeActualText(at)
					entry.hasActual = true
					entry.suppress = true
					entry.startX = x.tm[4]
					entry.startY = x.tm[5]
				}
				// thlibo: record /MCID so the structure-tree walker
				// can bind this content to its tag.
				if id, ok := props.Int("MCID"); ok {
					entry.mcid = id
				}
			}
		}
		x.markedStack = append(x.markedStack, entry)

	case "EMC":
		if len(x.markedStack) == 0 {
			return
		}
		top := x.markedStack[len(x.markedStack)-1]
		x.markedStack = x.markedStack[:len(x.markedStack)-1]
		if top.hasActual && top.actualText != "" {
			px, py := x.transformPos(top.startX, top.startY)
			x.spans = append(x.spans, TextSpan{
				X:        px,
				Y:        py,
				EndX:     px, // approximate
				FontSize: x.fontSize,
				Font:     x.fontName,
				Text:     top.actualText,
				// thlibo: the entry is already popped, so take
				// its own id — not currentMCID(), which would
				// now report the enclosing scope.
				MCID: top.mcid,
			})
		}
	}
}
