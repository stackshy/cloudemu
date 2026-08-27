package cloudwatch

// A small recursive-descent parser and evaluator for the subset of CloudWatch
// metric math GetMetricData supports here: numeric literals, references to other
// query Ids, the binary operators + - * /, unary minus, and parentheses.
// Expressions are evaluated element-wise across the referenced series.

import (
	"errors"
	"strconv"
	"time"
)

// errMathParse signals a malformed expression; the caller maps it to an empty
// series (never surfaced to the client as an error).
var errMathParse = errors.New("malformed metric math expression")

// mathResult is either a scalar constant or a resolved time series.
type mathResult struct {
	isScalar bool
	scalar   float64
	series   mathSeries
}

// asSeries renders a result as a series. A scalar collapses to a single-point
// series so a top-level constant expression still yields a value row.
func (r mathResult) asSeries() mathSeries {
	if !r.isScalar {
		return r.series
	}

	return mathSeries{timestamps: []time.Time{{}}, values: []float64{r.scalar}}
}

// combine applies a binary operator element-wise, broadcasting a scalar operand
// across the other operand's series.
func combine(op byte, left, right mathResult) mathResult {
	if left.isScalar && right.isScalar {
		return mathResult{isScalar: true, scalar: applyOp(op, left.scalar, right.scalar)}
	}

	if left.isScalar {
		return scalarSeries(op, left.scalar, right.series, true)
	}

	if right.isScalar {
		return scalarSeries(op, right.scalar, left.series, false)
	}

	return seriesSeries(op, left.series, right.series)
}

func scalarSeries(op byte, scalar float64, s mathSeries, scalarLeft bool) mathResult {
	values := make([]float64, len(s.values))

	for i, v := range s.values {
		if scalarLeft {
			values[i] = applyOp(op, scalar, v)
		} else {
			values[i] = applyOp(op, v, scalar)
		}
	}

	return mathResult{series: mathSeries{timestamps: s.timestamps, values: values}}
}

func seriesSeries(op byte, left, right mathSeries) mathResult {
	n := len(left.values)
	if len(right.values) < n {
		n = len(right.values)
	}

	values := make([]float64, n)
	for i := 0; i < n; i++ {
		values[i] = applyOp(op, left.values[i], right.values[i])
	}

	return mathResult{series: mathSeries{timestamps: left.timestamps[:n], values: values}}
}

func applyOp(op byte, a, b float64) float64 {
	switch op {
	case '+':
		return a + b
	case '-':
		return a - b
	case '*':
		return a * b
	case '/':
		if b == 0 {
			return 0
		}

		return a / b
	default:
		return 0
	}
}

// mathNode is a parsed expression node evaluated against the evaluator so that
// references resolve to the series of other queries.
type mathNode interface {
	evaluate(e *mathEvaluator) (mathResult, error)
}

type numberNode struct{ val float64 }

func (n numberNode) evaluate(*mathEvaluator) (mathResult, error) {
	return mathResult{isScalar: true, scalar: n.val}, nil
}

type refNode struct{ id string }

func (n refNode) evaluate(e *mathEvaluator) (mathResult, error) {
	series, err := e.resolve(n.id)
	if err != nil {
		return mathResult{}, err
	}

	return mathResult{series: series}, nil
}

type negNode struct{ operand mathNode }

func (n negNode) evaluate(e *mathEvaluator) (mathResult, error) {
	v, err := n.operand.evaluate(e)
	if err != nil {
		return mathResult{}, err
	}

	return combine('-', mathResult{isScalar: true, scalar: 0}, v), nil
}

type binaryNode struct {
	op          byte
	left, right mathNode
}

func (n binaryNode) evaluate(e *mathEvaluator) (mathResult, error) {
	left, err := n.left.evaluate(e)
	if err != nil {
		return mathResult{}, err
	}

	right, err := n.right.evaluate(e)
	if err != nil {
		return mathResult{}, err
	}

	return combine(n.op, left, right), nil
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
	return c == '+' || c == '-' || c == '*' || c == '/' || c == '(' || c == ')'
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

func (p *mathParser) parse() (mathNode, error) {
	if len(p.tokens) == 0 {
		return nil, errMathParse
	}

	return p.parseExpr()
}

func (p *mathParser) parseExpr() (mathNode, error) {
	node, err := p.parseTerm()
	if err != nil {
		return nil, err
	}

	for {
		tok, ok := p.peek()
		if !ok || (tok.kind != '+' && tok.kind != '-') {
			return node, nil
		}

		p.pos++

		right, rerr := p.parseTerm()
		if rerr != nil {
			return nil, rerr
		}

		node = binaryNode{op: tok.kind, left: node, right: right}
	}
}

func (p *mathParser) parseTerm() (mathNode, error) {
	node, err := p.parseFactor()
	if err != nil {
		return nil, err
	}

	for {
		tok, ok := p.peek()
		if !ok || (tok.kind != '*' && tok.kind != '/') {
			return node, nil
		}

		p.pos++

		right, rerr := p.parseFactor()
		if rerr != nil {
			return nil, rerr
		}

		node = binaryNode{op: tok.kind, left: node, right: right}
	}
}

func (p *mathParser) parseFactor() (mathNode, error) {
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

func (p *mathParser) parsePrimary() (mathNode, error) {
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
