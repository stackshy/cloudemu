package metricmath

// A small recursive-descent parser for the supported metric-math syntax:
// numbers, references to other entry IDs, the binary operators + - * /,
// unary minus and parentheses. ANOMALY_DETECTION_BAND(id[, k]) is also
// accepted when it is the whole expression.

import (
	"errors"
	"strconv"
)

// errMathParse marks a malformed expression. Callers treat it as no data.
var errMathParse = errors.New("malformed metric math expression")

// parseExpression parses expr. ok is false when it is not in the supported
// syntax.
func parseExpression(expr string) (n node, ok bool) {
	tokens := tokenizeMath(expr)
	if band, isBand := parseBand(tokens); isBand {
		return band, true
	}

	p := &mathParser{tokens: tokens}

	n, err := p.parse()
	if err != nil || !p.atEnd() {
		return nil, false
	}

	return n, true
}

// mathToken is one lexical unit of an expression.
type mathToken struct {
	kind  byte // 'n' number, 'i' ident, or an operator/paren rune
	num   float64
	ident string
}

func tokenizeMath(expr string) []mathToken {
	var tokens []mathToken

	for i := 0; i < len(expr); {
		c := expr[i]

		switch {
		case isSpace(c):
			i++
		case isOperatorOrParen(c):
			tokens = append(tokens, mathToken{kind: c})
			i++
		case isNumberStart(c):
			tok, next := lexNumber(expr, i)
			tokens = append(tokens, tok)
			i = next
		case isIdentStart(c):
			tok, next := lexIdent(expr, i)
			tokens = append(tokens, tok)
			i = next
		default:
			// Unknown character: emit a sentinel the parser rejects.
			tokens = append(tokens, mathToken{kind: '?'})
			i++
		}
	}

	return tokens
}

func lexNumber(expr string, start int) (tok mathToken, next int) {
	i := start
	for i < len(expr) && isNumberStart(expr[i]) {
		i++
	}

	val, err := strconv.ParseFloat(expr[start:i], 64)
	if err != nil {
		return mathToken{kind: '?'}, i
	}

	return mathToken{kind: 'n', num: val}, i
}

func lexIdent(expr string, start int) (tok mathToken, next int) {
	i := start
	for i < len(expr) && isIdentPart(expr[i]) {
		i++
	}

	return mathToken{kind: 'i', ident: expr[start:i]}, i
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' }
func isOperatorOrParen(c byte) bool {
	return c == '+' || c == '-' || c == '*' || c == '/' || c == '(' || c == ')' || c == ','
}
func isDigit(c byte) bool       { return c >= '0' && c <= '9' }
func isNumberStart(c byte) bool { return isDigit(c) || c == '.' }
func isIdentStart(c byte) bool  { return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isIdentPart(c byte) bool   { return isIdentStart(c) || isDigit(c) }

// mathParser is a recursive-descent parser over a token slice.
type mathParser struct {
	tokens []mathToken
	pos    int
}

func (p *mathParser) atEnd() bool { return p.pos >= len(p.tokens) }

func (p *mathParser) peek() (mathToken, bool) {
	if p.atEnd() {
		return mathToken{}, false
	}

	return p.tokens[p.pos], true
}

func (p *mathParser) parse() (node, error) {
	if len(p.tokens) == 0 {
		return nil, errMathParse
	}

	return p.parseExpr()
}

func (p *mathParser) parseExpr() (node, error) {
	n, err := p.parseTerm()
	if err != nil {
		return nil, err
	}

	for {
		tok, ok := p.peek()
		if !ok || (tok.kind != '+' && tok.kind != '-') {
			return n, nil
		}

		p.pos++

		right, rerr := p.parseTerm()
		if rerr != nil {
			return nil, rerr
		}

		n = binaryNode{op: tok.kind, left: n, right: right}
	}
}

func (p *mathParser) parseTerm() (node, error) {
	n, err := p.parseFactor()
	if err != nil {
		return nil, err
	}

	for {
		tok, ok := p.peek()
		if !ok || (tok.kind != '*' && tok.kind != '/') {
			return n, nil
		}

		p.pos++

		right, rerr := p.parseFactor()
		if rerr != nil {
			return nil, rerr
		}

		n = binaryNode{op: tok.kind, left: n, right: right}
	}
}

func (p *mathParser) parseFactor() (node, error) {
	tok, ok := p.peek()
	if !ok {
		return nil, errMathParse
	}

	if tok.kind == '-' {
		p.pos++

		operand, err := p.parseFactor()
		if err != nil {
			return nil, err
		}

		return negNode{operand: operand}, nil
	}

	return p.parsePrimary()
}

func (p *mathParser) parsePrimary() (node, error) {
	tok, ok := p.peek()
	if !ok {
		return nil, errMathParse
	}

	switch tok.kind {
	case 'n':
		p.pos++
		return numberNode{val: tok.num}, nil
	case 'i':
		p.pos++
		return refNode{id: tok.ident}, nil
	case '(':
		p.pos++

		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}

		closing, has := p.peek()
		if !has || closing.kind != ')' {
			return nil, errMathParse
		}

		p.pos++

		return inner, nil
	default:
		return nil, errMathParse
	}
}

// bandFunction is the metric-math function that returns an anomaly band.
const bandFunction = "ANOMALY_DETECTION_BAND"

// defaultBandWidth is the number of standard deviations when the call
// leaves it out.
const defaultBandWidth = 2

// Token counts of the two band call forms: F ( id ) and F ( id , k ).
const (
	bandCallTokens      = 4
	bandCallTokensWithK = 6
)

// isBandCall reports whether t starts ANOMALY_DETECTION_BAND(id and ends
// with a closing parenthesis.
func isBandCall(t []mathToken) bool {
	return t[0].kind == 'i' && t[0].ident == bandFunction && t[1].kind == '(' && t[2].kind == 'i' && t[len(t)-1].kind == ')'
}

// parseBand matches ANOMALY_DETECTION_BAND(id) and ANOMALY_DETECTION_BAND(id, k).
func parseBand(t []mathToken) (bandNode, bool) {
	if len(t) != bandCallTokens && len(t) != bandCallTokensWithK {
		return bandNode{}, false
	}

	if !isBandCall(t) {
		return bandNode{}, false
	}

	n := bandNode{input: t[2].ident, k: defaultBandWidth}

	if len(t) == bandCallTokensWithK {
		if t[3].kind != ',' || t[4].kind != 'n' {
			return bandNode{}, false
		}

		n.k = t[4].num
	}

	return n, true
}
