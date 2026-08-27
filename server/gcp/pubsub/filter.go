package pubsub

import (
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// filterExpr is a parsed Pub/Sub subscription filter, evaluated against a
// message's attributes. See
// https://cloud.google.com/pubsub/docs/subscription-message-filter for the
// grammar: attributes.KEY = "V" | attributes.KEY != "V" | attributes:KEY |
// hasPrefix(attributes.KEY, "P"), combined with NOT / AND / OR and parentheses.
type filterExpr interface {
	eval(attrs map[string]string) bool
}

// filterAll matches every message (empty filter).
type filterAll struct{}

func (filterAll) eval(map[string]string) bool { return true }

// hasAttr is `attributes:KEY` — true when the attribute is present.
type hasAttr struct{ key string }

func (h hasAttr) eval(a map[string]string) bool {
	_, ok := a[h.key]
	return ok
}

// equals is `attributes.KEY = "V"` (or != when negate) — exact value match.
type equals struct {
	key, val string
	negate   bool
}

func (e equals) eval(a map[string]string) bool {
	v, ok := a[e.key]
	match := ok && v == e.val

	if e.negate {
		return !match
	}

	return match
}

// prefix is `hasPrefix(attributes.KEY, "P")`.
type prefix struct{ key, pfx string }

func (p prefix) eval(a map[string]string) bool {
	v, ok := a[p.key]
	return ok && strings.HasPrefix(v, p.pfx)
}

type notExpr struct{ x filterExpr }

func (n notExpr) eval(a map[string]string) bool { return !n.x.eval(a) }

type andExpr struct{ l, r filterExpr }

func (n andExpr) eval(a map[string]string) bool { return n.l.eval(a) && n.r.eval(a) }

type orExpr struct{ l, r filterExpr }

func (n orExpr) eval(a map[string]string) bool { return n.l.eval(a) || n.r.eval(a) }

func errFilter() error {
	return cerrors.New(cerrors.InvalidArgument, "invalid filter expression")
}

// parseFilter compiles a Pub/Sub filter string. An empty filter matches all.
func parseFilter(s string) (filterExpr, error) {
	if strings.TrimSpace(s) == "" {
		return filterAll{}, nil
	}

	toks, err := tokenize(s)
	if err != nil {
		return nil, err
	}

	p := &parser{toks: toks}

	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}

	if p.peek().kind != tEOF {
		return nil, errFilter()
	}

	return expr, nil
}

// ---------- tokenizer ----------

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tString
	tDot
	tColon
	tEq
	tNe
	tLParen
	tRParen
	tComma
	tMinus
)

type token struct {
	kind tokKind
	val  string
}

const (
	// escLen is the length of a two-character escape (backslash + one char).
	escLen = 2
	// unicodeEscLen is the length of a \uXXXX escape.
	unicodeEscLen = 6
	// maxBMPCodepoint is the largest value a \uXXXX escape can encode (four hex
	// digits). It bounds the parsed value before the rune conversion.
	maxBMPCodepoint = 0xFFFF
)

func tokenize(s string) ([]token, error) {
	var toks []token

	for i := 0; i < len(s); {
		if c := s[i]; c == ' ' || c == '\t' || c == '\n' {
			i++
			continue
		}

		tok, n, err := scanToken(s, i)
		if err != nil {
			return nil, err
		}

		toks = append(toks, tok)
		i = n
	}

	return append(toks, token{tEOF, ""}), nil
}

// scanToken reads the single token starting at s[i], returning it and the index
// just past it.
func scanToken(s string, i int) (tok token, next int, err error) {
	c := s[i]

	switch {
	case c == '"':
		str, n, rerr := readString(s, i)
		if rerr != nil {
			return token{}, 0, rerr
		}

		return token{tString, str}, n, nil
	case c == '!':
		if i+1 >= len(s) || s[i+1] != '=' {
			return token{}, 0, errFilter()
		}

		return token{tNe, "!="}, i + len("!="), nil
	case isIdentStart(c):
		id, n := readIdent(s, i)
		return token{tIdent, id}, n, nil
	}

	k, ok := punctKind(c)
	if !ok {
		return token{}, 0, errFilter()
	}

	return token{k, string(c)}, i + 1, nil
}

func punctKind(c byte) (tokKind, bool) {
	switch c {
	case '.':
		return tDot, true
	case ':':
		return tColon, true
	case '(':
		return tLParen, true
	case ')':
		return tRParen, true
	case ',':
		return tComma, true
	case '-':
		return tMinus, true
	case '=':
		return tEq, true
	}

	return tEOF, false
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '-'
}

func readIdent(s string, start int) (ident string, next int) {
	i := start
	for i < len(s) && isIdentPart(s[i]) {
		i++
	}

	return s[start:i], i
}

func readString(s string, start int) (result string, next int, err error) {
	var b strings.Builder

	i := start + 1
	for i < len(s) {
		c := s[i]
		if c == '"' {
			return b.String(), i + 1, nil
		}

		if c != '\\' {
			b.WriteByte(c)

			i++

			continue
		}

		n, werr := writeEscape(&b, s, i)
		if werr != nil {
			return "", 0, werr
		}

		i = n
	}

	return "", 0, errFilter()
}

// writeEscape decodes the escape sequence at s[i] into b and returns the index
// just past it.
func writeEscape(b *strings.Builder, s string, i int) (next int, err error) {
	if i+1 >= len(s) {
		return 0, errFilter()
	}

	c := s[i+1]
	if c == 'u' {
		return writeUnicodeEscape(b, s, i)
	}

	switch c {
	case 'n':
		b.WriteByte('\n')
	case 't':
		b.WriteByte('\t')
	default:
		b.WriteByte(c)
	}

	return i + escLen, nil
}

func writeUnicodeEscape(b *strings.Builder, s string, i int) (next int, err error) {
	if i+unicodeEscLen > len(s) {
		return 0, errFilter()
	}

	v, perr := strconv.ParseUint(s[i+escLen:i+unicodeEscLen], 16, 32)
	if perr != nil {
		return 0, errFilter()
	}

	// A \uXXXX escape is four hex digits, so v is always <= 0xFFFF; bound it
	// explicitly so the rune conversion below cannot silently overflow.
	if v > maxBMPCodepoint {
		return 0, errFilter()
	}

	b.WriteRune(rune(v))

	return i + unicodeEscLen, nil
}

// ---------- recursive-descent parser (NOT > AND > OR) ----------

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) next() token {
	t := p.toks[p.pos]
	p.pos++

	return t
}

func (p *parser) isKeyword(kw string) bool {
	t := p.peek()
	return t.kind == tIdent && t.val == kw
}

func (p *parser) parseOr() (filterExpr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	for p.isKeyword("OR") {
		p.next()

		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}

		left = orExpr{left, right}
	}

	return left, nil
}

func (p *parser) parseAnd() (filterExpr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}

	for p.isKeyword("AND") {
		p.next()

		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}

		left = andExpr{left, right}
	}

	return left, nil
}

func (p *parser) parseNot() (filterExpr, error) {
	if p.isKeyword("NOT") || p.peek().kind == tMinus {
		p.next()

		x, err := p.parseNot()
		if err != nil {
			return nil, err
		}

		return notExpr{x}, nil
	}

	return p.parsePrimary()
}

func (p *parser) parsePrimary() (filterExpr, error) {
	if p.peek().kind == tLParen {
		p.next()

		x, err := p.parseOr()
		if err != nil {
			return nil, err
		}

		if p.next().kind != tRParen {
			return nil, errFilter()
		}

		return x, nil
	}

	t := p.peek()
	if t.kind != tIdent {
		return nil, errFilter()
	}

	switch t.val {
	case "hasPrefix":
		return p.parseHasPrefix()
	case "attributes":
		return p.parseAttr()
	}

	return nil, errFilter()
}

func (p *parser) parseAttr() (filterExpr, error) {
	p.next() // attributes

	kind := p.peek().kind
	if kind != tColon && kind != tDot {
		return nil, errFilter()
	}

	p.next()

	key, err := p.parseKey()
	if err != nil {
		return nil, err
	}

	if kind == tColon {
		return hasAttr{key}, nil
	}

	return p.parseComparison(key)
}

func (p *parser) parseComparison(key string) (filterExpr, error) {
	op := p.next()
	if op.kind != tEq && op.kind != tNe {
		return nil, errFilter()
	}

	v := p.next()
	if v.kind != tString {
		return nil, errFilter()
	}

	return equals{key: key, val: v.val, negate: op.kind == tNe}, nil
}

func (p *parser) parseHasPrefix() (filterExpr, error) {
	p.next() // hasPrefix

	if p.next().kind != tLParen {
		return nil, errFilter()
	}

	if !p.isKeyword("attributes") {
		return nil, errFilter()
	}

	p.next() // attributes

	if p.next().kind != tDot {
		return nil, errFilter()
	}

	key, err := p.parseKey()
	if err != nil {
		return nil, err
	}

	if p.next().kind != tComma {
		return nil, errFilter()
	}

	v := p.next()
	if v.kind != tString {
		return nil, errFilter()
	}

	if p.next().kind != tRParen {
		return nil, errFilter()
	}

	return prefix{key: key, pfx: v.val}, nil
}

// parseKey accepts an identifier or a quoted string as an attribute key.
func (p *parser) parseKey() (string, error) {
	t := p.next()
	if t.kind == tIdent || t.kind == tString {
		return t.val, nil
	}

	return "", errFilter()
}
