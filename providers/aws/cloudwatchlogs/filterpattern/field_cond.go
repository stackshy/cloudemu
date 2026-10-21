package filterpattern

import "strconv"

// fieldCond is a condition on one space-delimited field's value.
type fieldCond interface {
	eval(value string) bool
}

type orCond struct{ l, r fieldCond }

func (c orCond) eval(v string) bool { return c.l.eval(v) || c.r.eval(v) }

type andCond struct{ l, r fieldCond }

func (c andCond) eval(v string) bool { return c.l.eval(v) && c.r.eval(v) }

// cmpCond compares a field value with =, !=, <, <=, > or >=. Equality uses
// a numeric compare when both sides are numbers, else a * wildcard or %regex%
// match; ordering compares need a numeric field value.
type cmpCond struct {
	op  string
	val *valueSpec
}

func (c cmpCond) eval(v string) bool {
	switch c.op {
	case opEq:
		return c.equals(v)
	case opNe:
		return !c.equals(v)
	}

	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return false
	}

	switch c.op {
	case opLt:
		return f < c.val.num
	case opLe:
		return f <= c.val.num
	case opGt:
		return f > c.val.num
	default:
		return f >= c.val.num
	}
}

func (c cmpCond) equals(v string) bool {
	if c.val.re != nil {
		return c.val.re.MatchString(v)
	}

	if c.val.isNum {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f == c.val.num
		}
	}

	return c.val.glob.MatchString(v)
}

// condParser parses `name op value` terms joined by && and || (&& binds
// tighter). Every term must name the field the condition belongs to.
type condParser struct {
	toks    []token
	pos     int
	name    string
	regexes int
}

func (p *condParser) parseOr() (fieldCond, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	for p.pos < len(p.toks) && p.toks[p.pos].kind == tokOr {
		p.pos++

		right, rerr := p.parseAnd()
		if rerr != nil {
			return nil, rerr
		}

		left = orCond{l: left, r: right}
	}

	return left, nil
}

func (p *condParser) parseAnd() (fieldCond, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}

	for p.pos < len(p.toks) && p.toks[p.pos].kind == tokAnd {
		p.pos++

		right, rerr := p.parseTerm()
		if rerr != nil {
			return nil, rerr
		}

		left = andCond{l: left, r: right}
	}

	return left, nil
}

func (p *condParser) parseTerm() (fieldCond, error) {
	const termLen = 3 // name, operator, value

	if p.pos+termLen > len(p.toks) {
		return nil, invalid("Invalid condition on field '" + p.name + "' in space-delimited filter pattern")
	}

	name, op, val := p.toks[p.pos], p.toks[p.pos+1], p.toks[p.pos+2]

	if err := p.checkTerm(&name, &op, &val); err != nil {
		return nil, err
	}

	p.pos += termLen

	jp := &jsonParser{}

	v, err := jp.newValue(&val)
	if err != nil {
		return nil, err
	}

	p.regexes += jp.regexes

	if op.text != opEq && op.text != opNe && !v.isNum {
		return nil, invalid("Invalid filter pattern: " + op.text + " requires a number, got '" + val.text + "'")
	}

	return cmpCond{op: op.text, val: v}, nil
}

// checkTerm validates the shape of one `name op value` term.
func (p *condParser) checkTerm(name, op, val *token) error {
	if name.kind != tokWord || name.text != p.name {
		return invalid("Invalid condition on field '" + p.name + "': it must compare " + p.name)
	}

	if op.kind != tokOp || (val.kind != tokWord && val.kind != tokString && val.kind != tokRegex) {
		return invalid("Invalid condition on field '" + p.name + "' in space-delimited filter pattern")
	}

	return nil
}
