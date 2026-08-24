package tablestorage

import (
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	driver "github.com/stackshy/cloudemu/v2/services/tablestorage/driver"
)

// predicate evaluates an OData $filter against a rendered entity.
type predicate func(driver.Entity) bool

// parseFilter compiles an OData $filter expression into a predicate. It supports
// the comparison operators eq/ne/gt/ge/lt/le, the logical operators and/or/not,
// parentheses, and string, numeric, boolean, datetime and guid literals. An
// empty filter compiles to a nil predicate (match everything). A malformed
// filter returns an InvalidArgument error so the handler answers 400.
func parseFilter(filter string) (predicate, error) {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return nil, nil
	}

	toks, err := tokenize(filter)
	if err != nil {
		return nil, err
	}

	p := &parser{toks: toks}

	pred, err := p.parseOr()
	if err != nil {
		return nil, err
	}

	if p.pos != len(p.toks) {
		return nil, errFilter("unexpected trailing tokens in $filter")
	}

	return pred, nil
}

func errFilter(msg string) error {
	return errors.New(errors.InvalidArgument, msg)
}

// token kinds.
type tokKind int

const (
	tokIdent tokKind = iota // property name or operator keyword
	tokString
	tokNumber
	tokBool
	tokDateTime
	tokGUID
	tokLParen
	tokRParen
)

type token struct {
	kind tokKind
	text string
	num  float64
	b    bool
	t    time.Time
}

//nolint:gocyclo,cyclop // a flat lexer switch is clearer than fragmenting it.
func tokenize(s string) ([]token, error) {
	var toks []token

	i := 0
	for i < len(s) {
		c := s[i]

		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '(':
			toks = append(toks, token{kind: tokLParen})
			i++
		case c == ')':
			toks = append(toks, token{kind: tokRParen})
			i++
		case c == '\'':
			str, next, err := readString(s, i)
			if err != nil {
				return nil, err
			}

			toks = append(toks, token{kind: tokString, text: str})
			i = next
		case c == '-' || c == '+' || (c >= '0' && c <= '9'):
			tok, next, err := readNumber(s, i)
			if err != nil {
				return nil, err
			}

			toks = append(toks, tok)
			i = next
		case isIdentStart(c):
			tok, next, err := readIdentOrTyped(s, i)
			if err != nil {
				return nil, err
			}

			toks = append(toks, tok)
			i = next
		default:
			return nil, errFilter("unexpected character in $filter")
		}
	}

	return toks, nil
}

func readString(s string, i int) (str string, next int, err error) {
	// i points at the opening quote.
	var b strings.Builder

	j := i + 1
	for j < len(s) {
		if s[j] == '\'' {
			if j+1 < len(s) && s[j+1] == '\'' { // escaped quote
				b.WriteByte('\'')

				j += 2

				continue
			}

			return b.String(), j + 1, nil
		}

		b.WriteByte(s[j])

		j++
	}

	return "", 0, errFilter("unterminated string literal in $filter")
}

func readNumber(s string, i int) (token, int, error) {
	j := i
	if s[j] == '-' || s[j] == '+' {
		j++
	}

	for j < len(s) && isNumberByte(s[j]) {
		j++
	}

	// A trailing type suffix (L for Int64, etc.) is tolerated and ignored.
	for j < len(s) && isNumberSuffix(s[j]) {
		j++
	}

	raw := strings.TrimRight(s[i:j], "LlfFdD")

	n, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return token{}, 0, errFilter("invalid numeric literal in $filter")
	}

	return token{kind: tokNumber, num: n}, j, nil
}

func isNumberByte(c byte) bool {
	return c == '.' || (c >= '0' && c <= '9') || c == 'e' || c == 'E'
}

func isNumberSuffix(c byte) bool {
	return c == 'L' || c == 'l' || c == 'f' || c == 'F' || c == 'd' || c == 'D'
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// readIdentOrTyped reads a bare identifier/keyword or a typed literal such as
// datetime'…', guid'…', X'…' (binary), or a bare true/false boolean.
func readIdentOrTyped(s string, i int) (token, int, error) {
	j := i
	for j < len(s) && isIdentPart(s[j]) {
		j++
	}

	word := s[i:j]

	// Typed literal: identifier immediately followed by a quoted value.
	if j < len(s) && s[j] == '\'' {
		val, next, err := readString(s, j)
		if err != nil {
			return token{}, 0, err
		}

		return typedLiteral(word, val, next)
	}

	switch strings.ToLower(word) {
	case "true":
		return token{kind: tokBool, b: true}, j, nil
	case "false":
		return token{kind: tokBool, b: false}, j, nil
	}

	return token{kind: tokIdent, text: word}, j, nil
}

func typedLiteral(prefix, val string, next int) (token, int, error) {
	switch strings.ToLower(prefix) {
	case "datetime":
		t, err := time.Parse(time.RFC3339Nano, val)
		if err != nil {
			return token{}, 0, errFilter("invalid datetime literal in $filter")
		}

		return token{kind: tokDateTime, t: t}, next, nil
	case "guid":
		return token{kind: tokGUID, text: val}, next, nil
	case "x", "binary":
		return token{kind: tokString, text: val}, next, nil
	default:
		return token{}, 0, errFilter("unknown typed literal in $filter")
	}
}

// parser is a recursive-descent parser over the token stream.
type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() (token, bool) {
	if p.pos >= len(p.toks) {
		return token{}, false
	}

	return p.toks[p.pos], true
}

// isKeyword reports whether the current token is the ident keyword kw.
func (p *parser) isKeyword(kw string) bool {
	t, ok := p.peek()
	return ok && t.kind == tokIdent && strings.EqualFold(t.text, kw)
}

func (p *parser) parseOr() (predicate, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	for p.isKeyword("or") {
		p.pos++

		right, rerr := p.parseAnd()
		if rerr != nil {
			return nil, rerr
		}

		l, r := left, right
		left = func(e driver.Entity) bool { return l(e) || r(e) }
	}

	return left, nil
}

func (p *parser) parseAnd() (predicate, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}

	for p.isKeyword("and") {
		p.pos++

		right, rerr := p.parseUnary()
		if rerr != nil {
			return nil, rerr
		}

		l, r := left, right
		left = func(e driver.Entity) bool { return l(e) && r(e) }
	}

	return left, nil
}

func (p *parser) parseUnary() (predicate, error) {
	if p.isKeyword("not") {
		p.pos++

		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}

		return func(e driver.Entity) bool { return !inner(e) }, nil
	}

	return p.parsePrimary()
}

func (p *parser) parsePrimary() (predicate, error) {
	t, ok := p.peek()
	if !ok {
		return nil, errFilter("unexpected end of $filter")
	}

	if t.kind == tokLParen {
		p.pos++

		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}

		if cur, ok := p.peek(); !ok || cur.kind != tokRParen {
			return nil, errFilter("missing closing parenthesis in $filter")
		}

		p.pos++

		return inner, nil
	}

	return p.parseComparison()
}

// comparison operators.
var comparisonOps = map[string]bool{ //nolint:gochecknoglobals // immutable operator set.
	"eq": true, "ne": true, "gt": true, "ge": true, "lt": true, "le": true,
}

func (p *parser) parseComparison() (predicate, error) {
	left, err := p.parseOperand()
	if err != nil {
		return nil, err
	}

	opTok, ok := p.peek()
	if !ok || opTok.kind != tokIdent || !comparisonOps[strings.ToLower(opTok.text)] {
		return nil, errFilter("expected a comparison operator in $filter")
	}

	op := strings.ToLower(opTok.text)
	p.pos++

	right, err := p.parseOperand()
	if err != nil {
		return nil, err
	}

	return func(e driver.Entity) bool {
		return compareOp(op, left(e), right(e))
	}, nil
}

// operand resolves to a value against an entity: a literal ignores the entity,
// an identifier looks up the property.
type operand func(driver.Entity) any

func (p *parser) parseOperand() (operand, error) {
	t, ok := p.peek()
	if !ok {
		return nil, errFilter("expected an operand in $filter")
	}

	p.pos++

	switch t.kind {
	case tokString, tokGUID:
		v := t.text
		return func(driver.Entity) any { return v }, nil
	case tokNumber:
		v := t.num
		return func(driver.Entity) any { return v }, nil
	case tokBool:
		v := t.b
		return func(driver.Entity) any { return v }, nil
	case tokDateTime:
		v := t.t
		return func(driver.Entity) any { return v }, nil
	case tokIdent:
		name := t.text
		return func(e driver.Entity) any { return e[name] }, nil
	case tokLParen, tokRParen:
		return nil, errFilter("unexpected parenthesis where an operand was expected in $filter")
	}

	return nil, errFilter("unexpected token where an operand was expected in $filter")
}

// compareOp evaluates one comparison.
func compareOp(op string, left, right any) bool {
	cmp, ok := compareValues(left, right)

	// "ne" is the only operator that is true for incomparable operands.
	if op == "ne" {
		return !ok || cmp != 0
	}

	if !ok {
		return false
	}

	switch op {
	case "eq":
		return cmp == 0
	case "gt":
		return cmp > 0
	case "ge":
		return cmp >= 0
	case "lt":
		return cmp < 0
	case "le":
		return cmp <= 0
	default:
		return false
	}
}

// compareValues returns -1/0/1 comparing a and b, and ok=false when they are
// not meaningfully comparable.
func compareValues(a, b any) (int, bool) {
	if a == nil || b == nil {
		return 0, a == nil && b == nil
	}

	if c, ok := cmpAsNumbers(a, b); ok {
		return c, true
	}

	if c, ok := cmpAsTimes(a, b); ok {
		return c, true
	}

	if c, ok := cmpAsBools(a, b); ok {
		return c, true
	}

	return cmpAsStrings(a, b)
}

func cmpAsNumbers(a, b any) (int, bool) {
	an, aok := toFloat(a)
	bn, bok := toFloat(b)

	if aok && bok {
		return cmpFloat(an, bn), true
	}

	return 0, false
}

func cmpAsTimes(a, b any) (int, bool) {
	at, aok := toTime(a)
	bt, bok := toTime(b)

	if aok && bok {
		return cmpTime(at, bt), true
	}

	return 0, false
}

func cmpAsBools(a, b any) (int, bool) {
	ab, aok := a.(bool)
	bb, bok := b.(bool)

	if aok && bok {
		return cmpBool(ab, bb), true
	}

	return 0, false
}

func cmpAsStrings(a, b any) (int, bool) {
	as, aok := a.(string)
	bs, bok := b.(string)

	if aok && bok {
		return strings.Compare(as, bs), true
	}

	return 0, false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func toTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, t)
		if err != nil {
			return time.Time{}, false
		}

		return parsed, true
	default:
		return time.Time{}, false
	}
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpTime(a, b time.Time) int {
	switch {
	case a.Before(b):
		return -1
	case a.After(b):
		return 1
	default:
		return 0
	}
}

func cmpBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case !a:
		return -1
	default:
		return 1
	}
}
