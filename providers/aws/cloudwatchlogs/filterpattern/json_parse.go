package filterpattern

import (
	"regexp"
	"strconv"
	"strings"
)

// Operators and keywords of the JSON pattern language.
const (
	opEq     = "="
	opNe     = "!="
	kwIs     = "IS"
	kwNot    = "NOT"
	kwExists = "EXISTS"
	kwNull   = "NULL"
	kwTrue   = "TRUE"
	kwFalse  = "FALSE"
)

// jsonNode is one node of a parsed JSON pattern expression.
type jsonNode interface {
	eval(doc any) bool
	selectors() []token
}

type orNode struct{ l, r jsonNode }

func (n orNode) eval(doc any) bool { return n.l.eval(doc) || n.r.eval(doc) }

func (n orNode) selectors() []token { return append(n.l.selectors(), n.r.selectors()...) }

type andNode struct{ l, r jsonNode }

func (n andNode) eval(doc any) bool { return n.l.eval(doc) && n.r.eval(doc) }

func (n andNode) selectors() []token { return append(n.l.selectors(), n.r.selectors()...) }

// jsonPattern is a parsed { ... } pattern.
type jsonPattern struct {
	root       jsonNode
	regexCount int
}

type jsonParser struct {
	toks      []token
	pos       int
	regexes   int
	wildcards int
}

func parseJSONPattern(s string) (*jsonPattern, error) {
	if !strings.HasSuffix(s, "}") {
		return nil, invalid("Invalid filter pattern: JSON pattern must end with '}'")
	}

	toks, err := lexJSON(s[1 : len(s)-1])
	if err != nil {
		return nil, err
	}

	if len(toks) == 0 {
		return nil, invalid("Invalid filter pattern: empty JSON pattern")
	}

	p := &jsonParser{toks: toks}

	root, err := p.parseOr()
	if err != nil {
		return nil, err
	}

	if p.pos != len(p.toks) {
		return nil, invalid("Invalid filter pattern: unexpected '" + p.toks[p.pos].text + "'")
	}

	if p.regexes > maxRegexPerStructuredPattern {
		return nil, invalid("Invalid filter pattern: at most 2 regular expressions are allowed in a JSON filter pattern")
	}

	if p.wildcards > maxWildcardsPerPattern {
		return nil, invalid("Invalid filter pattern: at most 3 wildcard selectors are allowed in a filter pattern")
	}

	return &jsonPattern{root: root, regexCount: p.regexes}, nil
}

func (p *jsonParser) peek() *token {
	if p.pos >= len(p.toks) {
		return nil
	}

	return &p.toks[p.pos]
}

func (p *jsonParser) next() *token {
	t := p.peek()
	if t != nil {
		p.pos++
	}

	return t
}

// parseOr and parseAnd give && higher precedence than ||, and parentheses
// the highest, as documented.
func (p *jsonParser) parseOr() (jsonNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	for t := p.peek(); t != nil && t.kind == tokOr; t = p.peek() {
		p.pos++

		right, rerr := p.parseAnd()
		if rerr != nil {
			return nil, rerr
		}

		left = orNode{l: left, r: right}
	}

	return left, nil
}

func (p *jsonParser) parseAnd() (jsonNode, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}

	for t := p.peek(); t != nil && t.kind == tokAnd; t = p.peek() {
		p.pos++

		right, rerr := p.parseUnary()
		if rerr != nil {
			return nil, rerr
		}

		left = andNode{l: left, r: right}
	}

	return left, nil
}

func (p *jsonParser) parseUnary() (jsonNode, error) {
	t := p.peek()
	if t == nil {
		return nil, invalid("Invalid filter pattern: expression expected")
	}

	if t.kind != tokLParen {
		return p.parseCondition()
	}

	p.pos++

	inner, err := p.parseOr()
	if err != nil {
		return nil, err
	}

	if c := p.next(); c == nil || c.kind != tokRParen {
		return nil, invalid("Invalid filter pattern: missing ')'")
	}

	return inner, nil
}

// parseCondition parses `selector op value`, `selector IS NULL|TRUE|FALSE` or
// `selector NOT EXISTS`. `IS NOT` and a bare `EXISTS` are not part of the
// language and are rejected.
func (p *jsonParser) parseCondition() (jsonNode, error) {
	sel := p.next()
	if sel.kind != tokSelector {
		return nil, invalid("Invalid filter pattern: property selector expected, got '" + sel.text + "'")
	}

	if err := p.countWildcards(sel); err != nil {
		return nil, err
	}

	t := p.next()
	if t == nil {
		return nil, invalid("Invalid filter pattern: operator expected after " + sel.text)
	}

	if t.kind == tokOp {
		return p.parseComparison(sel, t.text)
	}

	if t.kind == tokWord {
		return p.parseKeyword(sel, strings.ToUpper(t.text))
	}

	return nil, invalid("Invalid filter pattern: operator expected after " + sel.text)
}

func (p *jsonParser) countWildcards(sel *token) error {
	n := 0

	for _, st := range sel.steps {
		if st.kind == stepWildcard {
			n++
		}
	}

	if n > maxWildcardsPerSelector {
		return invalid("Invalid filter pattern: only one wildcard is allowed in property selector " + sel.text)
	}

	p.wildcards += n

	return nil
}

func (p *jsonParser) parseKeyword(sel *token, kw string) (jsonNode, error) {
	switch kw {
	case kwIs:
		v := p.next()
		if v == nil {
			return nil, invalid("Invalid filter pattern: NULL, TRUE or FALSE expected after IS")
		}

		switch kind := strings.ToUpper(v.text); kind {
		case kwNull, kwTrue, kwFalse:
			return isNode{sel: *sel, kind: kind}, nil
		case kwNot:
			return nil, invalid("Invalid filter pattern: IS NOT is not supported")
		default:
			return nil, invalid("Invalid filter pattern: NULL, TRUE or FALSE expected after IS")
		}
	case kwNot:
		if v := p.next(); v == nil || !strings.EqualFold(v.text, kwExists) {
			return nil, invalid("Invalid filter pattern: EXISTS expected after NOT")
		}

		return notExistsNode{sel: *sel}, nil
	case kwExists:
		return nil, invalid("Invalid filter pattern: EXISTS is not supported; use NOT EXISTS")
	}

	return nil, invalid("Invalid filter pattern: operator expected after " + sel.text)
}

func (p *jsonParser) parseComparison(sel *token, op string) (jsonNode, error) {
	t := p.next()
	if t == nil || (t.kind != tokString && t.kind != tokRegex && t.kind != tokWord) {
		return nil, invalid("Invalid filter pattern: value expected after " + sel.text + " " + op)
	}

	v, err := p.newValue(t)
	if err != nil {
		return nil, err
	}

	if op != opEq && op != opNe && !v.isNum {
		return nil, invalid("Invalid filter pattern: " + op + " requires a number, got '" + t.text + "'")
	}

	return cmpNode{sel: *sel, op: op, val: v}, nil
}

func (p *jsonParser) newValue(t *token) (*valueSpec, error) {
	v := &valueSpec{text: t.text}

	if t.kind == tokRegex {
		re, err := compileRegex(t.text)
		if err != nil {
			return nil, err
		}

		p.regexes++
		v.re = re

		return v, nil
	}

	if t.kind == tokWord {
		if n, err := strconv.ParseFloat(t.text, 64); err == nil && !strings.Contains(t.text, "*") {
			v.isNum, v.num = true, n
		}
	}

	v.glob = globRegexp(t.text)

	return v, nil
}

// globRegexp turns a value with * wildcards into an anchored regexp.
func globRegexp(s string) *regexp.Regexp {
	parts := strings.Split(s, "*")
	for i := range parts {
		parts[i] = regexp.QuoteMeta(parts[i])
	}

	return regexp.MustCompile("^" + strings.Join(parts, ".*") + "$")
}
