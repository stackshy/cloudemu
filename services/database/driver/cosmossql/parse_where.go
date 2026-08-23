package cosmossql

import (
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

func (p *parser) parsePredicate() (expr.Node, error) { return p.parseOr() }

func (p *parser) parseOr() (expr.Node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	for p.accept("OR") {
		right, rerr := p.parseAnd()
		if rerr != nil {
			return nil, rerr
		}

		left = &expr.Or{Left: left, Right: right}
	}

	return left, nil
}

func (p *parser) parseAnd() (expr.Node, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}

	for p.accept("AND") {
		right, rerr := p.parseNot()
		if rerr != nil {
			return nil, rerr
		}

		left = &expr.And{Left: left, Right: right}
	}

	return left, nil
}

func (p *parser) parseNot() (expr.Node, error) {
	if p.accept("NOT") {
		child, err := p.parseNot()
		if err != nil {
			return nil, err
		}

		return &expr.Not{Child: child}, nil
	}

	return p.parsePrimaryPred()
}

func (p *parser) parsePrimaryPred() (expr.Node, error) {
	if p.peek().kind == tLParen {
		p.next()

		node, err := p.parsePredicate()
		if err != nil {
			return nil, err
		}

		if perr := p.expectKind(tRParen, "')'"); perr != nil {
			return nil, perr
		}

		return node, nil
	}

	if node, ok, err := p.tryBoolFunc(); err != nil {
		return nil, err
	} else if ok {
		return node, nil
	}

	return p.parseComparison()
}

func (p *parser) parseComparison() (expr.Node, error) {
	left, err := p.parseOperand()
	if err != nil {
		return nil, err
	}

	switch {
	case p.accept("IN"):
		return p.parseIn(left)
	case p.accept("BETWEEN"):
		return p.parseBetween(left)
	case p.peek().kind == tOp:
		op := normalizeOp(p.next().text)

		right, rerr := p.parseOperand()
		if rerr != nil {
			return nil, rerr
		}

		return &expr.Comparison{Op: op, Left: left, Right: right}, nil
	default:
		return nil, cerrors.Newf(cerrors.InvalidArgument, "expected an operator but found %q", p.peek().text)
	}
}

const (
	opBangEqual = "!="
	opNotEqual  = "<>"
)

func normalizeOp(op string) string {
	if op == opBangEqual {
		return opNotEqual
	}

	return op
}

func (p *parser) parseIn(left expr.Operand) (expr.Node, error) {
	if err := p.expectKind(tLParen, "'('"); err != nil {
		return nil, err
	}

	var list []expr.Operand

	for {
		op, err := p.parseOperand()
		if err != nil {
			return nil, err
		}

		list = append(list, op)

		if p.peek().kind != tComma {
			break
		}

		p.next()
	}

	if err := p.expectKind(tRParen, "')'"); err != nil {
		return nil, err
	}

	return &expr.In{Operand: left, List: list}, nil
}

func (p *parser) parseBetween(left expr.Operand) (expr.Node, error) {
	lo, err := p.parseOperand()
	if err != nil {
		return nil, err
	}

	if !p.accept("AND") {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "expected AND in BETWEEN but found %q", p.peek().text)
	}

	hi, herr := p.parseOperand()
	if herr != nil {
		return nil, herr
	}

	return &expr.Between{Operand: left, Lo: lo, Hi: hi}, nil
}

func (p *parser) parseOperand() (expr.Operand, error) {
	t := p.peek()

	//nolint:exhaustive // the default case handles the remaining token kinds.
	switch t.kind {
	case tParam:
		p.next()

		v, ok := p.params[t.text]
		if !ok {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "no value supplied for parameter %q", t.text)
		}

		return &expr.ValueOperand{Value: v}, nil
	case tString:
		p.next()
		return &expr.ValueOperand{Value: t.text}, nil
	case tNumber:
		p.next()

		f, _ := strconv.ParseFloat(t.text, 64)

		return &expr.ValueOperand{Value: f}, nil
	case tIdent:
		return p.parseIdentOperand()
	default:
		return nil, cerrors.Newf(cerrors.InvalidArgument, "expected an operand but found %q", t.text)
	}
}

func (p *parser) parseIdentOperand() (expr.Operand, error) {
	switch strings.ToUpper(p.peek().text) {
	case "TRUE":
		p.next()
		return &expr.ValueOperand{Value: true}, nil
	case "FALSE":
		p.next()
		return &expr.ValueOperand{Value: false}, nil
	case "NULL":
		p.next()
		return &expr.ValueOperand{Value: nil}, nil
	default:
		return p.parsePathOperand()
	}
}

func (p *parser) parsePathOperand() (*expr.PathOperand, error) {
	parts, err := p.parsePathParts()
	if err != nil {
		return nil, err
	}

	return &expr.PathOperand{Parts: p.stripParts(parts)}, nil
}

func (p *parser) parsePathParts() ([]expr.PathPart, error) {
	if p.peek().kind != tIdent {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "expected a property path but found %q", p.peek().text)
	}

	parts := []expr.PathPart{{Name: p.next().text}}

	for {
		//nolint:exhaustive // only '.' and '[' continue a path.
		switch p.peek().kind {
		case tDot:
			p.next()

			if p.peek().kind != tIdent {
				return nil, cerrors.Newf(cerrors.InvalidArgument, "expected a property name but found %q", p.peek().text)
			}

			parts = append(parts, expr.PathPart{Name: p.next().text})
		case tLBracket:
			part, err := p.parseBracket()
			if err != nil {
				return nil, err
			}

			parts = append(parts, part)
		default:
			return parts, nil
		}
	}
}

func (p *parser) parseBracket() (expr.PathPart, error) {
	p.next() // '['

	t := p.peek()

	var part expr.PathPart

	//nolint:exhaustive // the default case handles the remaining token kinds.
	switch t.kind {
	case tString:
		p.next()
		part = expr.PathPart{Name: t.text}
	case tNumber:
		p.next()

		idx, err := strconv.Atoi(t.text)
		if err != nil {
			return part, cerrors.Newf(cerrors.InvalidArgument, "invalid array index %q", t.text)
		}

		part = expr.PathPart{Index: idx, IsIndex: true}
	default:
		return part, cerrors.Newf(cerrors.InvalidArgument, "expected a property or index but found %q", t.text)
	}

	if err := p.expectKind(tRBracket, "']'"); err != nil {
		return part, err
	}

	return part, nil
}

func (p *parser) stripParts(parts []expr.PathPart) []expr.PathPart {
	if len(parts) > 0 && !parts[0].IsIndex && parts[0].Name == p.alias {
		return parts[1:]
	}

	return parts
}
