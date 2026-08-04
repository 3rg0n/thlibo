package pdf

import (
	"fmt"
)

// Parser reads PDF objects from a Lexer.
type Parser struct {
	lex   *Lexer
	depth int // current array/dict nesting, guarded by maxObjectDepth
}

// maxObjectDepth bounds array and dictionary nesting.
//
// thlibo: local addition. parseArray and parseDict recurse into each other
// through parseFromToken with nothing to stop them, so `[[[[...` descends
// once per byte. That is a **fatal** condition rather than a panic: Go grows
// a goroutine stack on demand up to 1 GB and then calls runtime.throw, which
// `recover()` cannot catch — so `RunNative`'s panic net (native.go) does not
// fire and the process dies with the tool output still on the input side of
// the pipe. Measured before the fix: exit 2 and **empty stdout** from
// `thlibo compress` at ~1.3M levels, i.e. a 2.6 MB file, against the 64 MiB
// the middleware accepts.
//
// 1024 is far past any document a real producer emits (the deepest nesting
// in a legitimate PDF is a handful of levels — an array of arrays of
// coordinates) while leaving the recursion four orders of magnitude short of
// the stack limit. Rejecting the object is the right outcome: an over-nested
// object is malformed, callers already treat a parse error as "not our
// document", and pdfFilter passes the original bytes through.
const maxObjectDepth = 1024

func NewParser(data []byte) *Parser {
	return &Parser{lex: NewLexer(data)}
}

func (p *Parser) Lexer() *Lexer { return p.lex }

// ParseObject reads the next complete PDF object (value) from the stream.
// It handles indirect references (N G R) by lookahead.
func (p *Parser) ParseObject() (any, error) {
	tok, err := p.lex.NextToken()
	if err != nil {
		return nil, err
	}
	return p.parseFromToken(tok)
}

func (p *Parser) parseFromToken(tok Token) (any, error) {
	switch tok.Type {
	case TEOF:
		return nil, fmt.Errorf("unexpected EOF")

	case TNumber:
		// Could be start of indirect reference: num gen R
		if tok.IsInt {
			savedPos := p.lex.Pos()
			tok2, err := p.lex.NextToken()
			if err == nil && tok2.Type == TNumber && tok2.IsInt {
				tok3, err := p.lex.NextToken()
				if err == nil && tok3.Type == TKeyword && tok3.Str == "R" {
					return Ref{Num: tok.Int, Gen: tok2.Int}, nil
				}
			}
			p.lex.SetPos(savedPos)
		}
		if tok.IsInt {
			return tok.Int, nil
		}
		return tok.Num, nil

	case TString:
		return tok.Str, nil

	case THexString:
		return tok.Str, nil

	case TName:
		return Name(tok.Str), nil

	case TBool:
		return tok.Str == "true", nil

	case TNull:
		return nil, nil

	case TArrayStart:
		return p.parseArray()

	case TDictStart:
		return p.parseDict()

	case TKeyword:
		// Return keywords as-is for content stream parsing.
		return tok.Str, nil

	default:
		return nil, fmt.Errorf("unexpected token type %d: %q", tok.Type, tok.Str)
	}
}

func (p *Parser) parseArray() (Array, error) {
	// thlibo: depth guard. See maxObjectDepth — unbounded here is a fatal
	// stack overflow, not a recoverable panic.
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxObjectDepth {
		return nil, fmt.Errorf("array nesting exceeds %d levels", maxObjectDepth)
	}

	var arr Array
	for {
		tok, err := p.lex.NextToken()
		if err != nil {
			return nil, err
		}
		if tok.Type == TArrayEnd {
			return arr, nil
		}
		if tok.Type == TEOF {
			return nil, fmt.Errorf("unterminated array")
		}
		obj, err := p.parseFromToken(tok)
		if err != nil {
			return nil, err
		}
		arr = append(arr, obj)
	}
}

func (p *Parser) parseDict() (Dict, error) {
	// thlibo: depth guard, same reasoning as parseArray. The two recurse
	// into each other, so both need it — bounding one leaves `<< /K << /K
	// << ...` unbounded.
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxObjectDepth {
		return nil, fmt.Errorf("dict nesting exceeds %d levels", maxObjectDepth)
	}

	d := make(Dict)
	for {
		tok, err := p.lex.NextToken()
		if err != nil {
			return nil, err
		}
		if tok.Type == TDictEnd {
			return d, nil
		}
		if tok.Type == TEOF {
			return nil, fmt.Errorf("unterminated dict")
		}
		if tok.Type != TName {
			return nil, fmt.Errorf("expected name key in dict, got %d: %q", tok.Type, tok.Str)
		}
		key := Name(tok.Str)
		val, err := p.ParseObject()
		if err != nil {
			return nil, fmt.Errorf("parsing dict value for /%s: %w", key, err)
		}
		d[key] = val
	}
}
