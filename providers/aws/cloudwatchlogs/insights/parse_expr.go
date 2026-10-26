package insights

import (
	"regexp"
	"strconv"
	"strings"
)

// exprParser parses one command's expressions from its tokens.
type exprParser struct {
	toks []token
	pos  int
	// end is the query offset just past the command, for errors at its end.
	end int
	// src is the whole query, for naming expressions by their text.
	src string
	// aggs collects a stats command's aggregation calls; nil elsewhere,
	// where aggregation functions are not allowed.
	aggs *[]*aggCall
	// refs are the field names the command references, and defines the
	// fields it creates.
	refs    []token
	defines []string
}

func (p *exprParser) peek() *token {
	if p.pos >= len(p.toks) {
		return nil
	}

	return &p.toks[p.pos]
}

func (p *exprParser) atKeyword(kw string) bool {
	t := p.peek()

	return t != nil && t.kind == tIdent && strings.EqualFold(t.text, kw)
}

func (p *exprParser) errHere(msg string) error {
	if t := p.peek(); t != nil {
		return compileErr(t.start, t.end, msg)
	}

	return compileErr(p.end, p.end, msg)
}

func (p *exprParser) parseOr() (expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	for p.atKeyword(kwOr) {
		p.pos++

		right, rerr := p.parseAnd()
		if rerr != nil {
			return nil, rerr
		}

		left = logicExpr{l: left, r: right}
	}

	return left, nil
}

func (p *exprParser) parseAnd() (expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}

	for p.atKeyword(kwAnd) {
		p.pos++

		right, rerr := p.parseNot()
		if rerr != nil {
			return nil, rerr
		}

		left = logicExpr{and: true, l: left, r: right}
	}

	return left, nil
}

func (p *exprParser) parseNot() (expr, error) {
	if p.atKeyword(kwNot) {
		p.pos++

		x, err := p.parseNot()
		if err != nil {
			return nil, err
		}

		return notExpr{x: x}, nil
	}

	return p.parseCmp()
}

//nolint:gochecknoglobals // fixed operator set.
var cmpOps = map[string]bool{
	opEq: true, opNe: true, opLt: true, opLe: true, opGt: true, opGe: true, opMatch: true, opNotMatch: true,
}

// parseCmp parses a comparison, `like`, `in`, or their `not` forms.
func (p *exprParser) parseCmp() (expr, error) {
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}

	negate := p.negatedMatch()

	switch {
	case p.atKeyword(kwLike):
		p.pos++

		pat, perr := p.parsePrimary()
		if perr != nil {
			return nil, perr
		}

		return likeExpr{x: left, pattern: pat, negate: negate}, nil
	case p.atKeyword(kwIn):
		p.pos++

		return p.parseInList(left, negate)
	}

	t := p.peek()
	if t == nil || t.kind != tOp || !cmpOps[t.text] {
		return left, nil
	}

	p.pos++

	right, err := p.parseAdd()
	if err != nil {
		return nil, err
	}

	return cmpExpr{op: t.text, l: left, r: right}, nil
}

// negatedMatch consumes the `not` of `not like` / `not in`.
func (p *exprParser) negatedMatch() bool {
	if !p.atKeyword(kwNot) || p.pos+1 >= len(p.toks) {
		return false
	}

	next := strings.ToLower(p.toks[p.pos+1].text)
	if next != kwLike && next != kwIn {
		return false
	}

	p.pos++

	return true
}

func (p *exprParser) parseInList(left expr, negate bool) (expr, error) {
	if t := p.peek(); t == nil || t.kind != tLBracket {
		return nil, p.errHere("expected '[' after in")
	}

	p.pos++

	var list []expr

	for {
		item, err := p.parseAdd()
		if err != nil {
			return nil, err
		}

		list = append(list, item)

		t := p.peek()
		if t != nil && t.kind == tComma {
			p.pos++
			continue
		}

		if t == nil || t.kind != tRBracket {
			return nil, p.errHere("expected ',' or ']' in list")
		}

		p.pos++

		return inExpr{x: left, list: list, negate: negate}, nil
	}
}

func (p *exprParser) parseAdd() (expr, error) {
	return p.parseBinary(p.parseMul, "+", "-")
}

func (p *exprParser) parseMul() (expr, error) {
	return p.parseBinary(p.parseUnary, "*", "/", "%", "^")
}

func (p *exprParser) parseBinary(next func() (expr, error), ops ...string) (expr, error) {
	left, err := next()
	if err != nil {
		return nil, err
	}

	for {
		t := p.peek()
		if t == nil || t.kind != tOp || !contains(ops, t.text) {
			return left, nil
		}

		p.pos++

		right, rerr := next()
		if rerr != nil {
			return nil, rerr
		}

		left = arithExpr{op: t.text, l: left, r: right}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}

	return false
}

func (p *exprParser) parseUnary() (expr, error) {
	if t := p.peek(); t != nil && t.kind == tOp && t.text == "-" {
		p.pos++

		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}

		return negExpr{x: x}, nil
	}

	return p.parsePrimary()
}

//nolint:gochecknoglobals // fixed reserved-word set.
var reserved = map[string]bool{kwAnd: true, kwOr: true, kwNot: true, kwLike: true, kwIn: true, kwAs: true}

func (p *exprParser) parsePrimary() (expr, error) {
	t := p.peek()
	if t == nil {
		return nil, p.errHere("expression expected")
	}

	p.pos++

	switch t.kind {
	case tNumber:
		f, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return nil, compileErr(t.start, t.end, "invalid number '"+t.text+"'")
		}

		return literal{v: f}, nil
	case tString:
		return literal{v: t.text}, nil
	case tRegex:
		re, err := regexp.Compile(t.text)
		if err != nil {
			return nil, compileErr(t.start, t.end, "invalid regular expression /"+t.text+"/")
		}

		return regexLit{re: re}, nil
	case tLParen:
		return p.parseParen()
	case tIdent:
		return p.parseIdent(t)
	case tOp, tComma, tRParen, tLBracket, tRBracket, tPipe:
	}

	return nil, compileErr(t.start, t.end, "unexpected '"+t.text+"'")
}

func (p *exprParser) parseParen() (expr, error) {
	inner, err := p.parseOr()
	if err != nil {
		return nil, err
	}

	if t := p.peek(); t == nil || t.kind != tRParen {
		return nil, p.errHere("expected ')'")
	}

	p.pos++

	return inner, nil
}

func (p *exprParser) parseIdent(t *token) (expr, error) {
	if reserved[strings.ToLower(t.text)] {
		return nil, compileErr(t.start, t.end, "unexpected keyword '"+t.text+"'")
	}

	if next := p.peek(); next == nil || next.kind != tLParen {
		p.refs = append(p.refs, *t)

		return fieldRef{name: t.text}, nil
	}

	name := strings.ToLower(t.text)

	spec, ok := functions[name]
	if !ok {
		return p.parseSpecialCall(t, name)
	}

	p.pos++

	args, err := p.parseArgs()
	if err != nil {
		return nil, err
	}

	if len(args) < spec.minArgs || (spec.maxArgs >= 0 && len(args) > spec.maxArgs) {
		return nil, compileErr(t.start, t.end, "wrong number of arguments to "+t.text)
	}

	return callExpr{spec: spec, args: args}, nil
}

// parseSpecialCall parses the calls that are not plain functions: the
// date functions, which take a period literal, and inside stats the
// aggregations.
func (p *exprParser) parseSpecialCall(t *token, name string) (expr, error) {
	agg, isAgg := aggregations[name]

	switch {
	case isDateFunc(name):
		p.pos++

		return p.parseDateFunc(t)
	case isAgg && p.aggs != nil:
		p.pos++

		return p.parseAgg(t, agg)
	case isAgg:
		return nil, compileErr(t.start, t.end, "aggregation function '"+t.text+"' is only allowed in stats")
	}

	return nil, compileErr(t.start, t.end, "unknown function '"+t.text+"'")
}

func (p *exprParser) parseArgs() ([]expr, error) {
	var args []expr

	if t := p.peek(); t != nil && t.kind == tRParen {
		p.pos++
		return args, nil
	}

	for {
		a, err := p.parseOr()
		if err != nil {
			return nil, err
		}

		args = append(args, a)

		t := p.peek()
		if t != nil && t.kind == tComma {
			p.pos++
			continue
		}

		if t == nil || t.kind != tRParen {
			return nil, p.errHere("expected ',' or ')' in function call")
		}

		p.pos++

		return args, nil
	}
}
