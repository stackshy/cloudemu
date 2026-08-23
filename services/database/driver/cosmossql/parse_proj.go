package cosmossql

import (
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

func (p *parser) parseProjection() (Projection, error) {
	if p.peek().kind == tStar {
		p.next()
		return Projection{Kind: ProjStar}, nil
	}

	value := p.accept("VALUE")

	agg, isAgg, err := p.tryAggregate()
	if err != nil {
		return Projection{}, err
	}

	if isAgg {
		return Projection{Kind: ProjAggregate, Aggregate: agg, Bare: !value}, nil
	}

	if value {
		segs, verr := p.parseSegments()
		if verr != nil {
			return Projection{}, verr
		}

		return Projection{Kind: ProjValue, ValuePath: segs}, nil
	}

	fields, ferr := p.parseFieldList()
	if ferr != nil {
		return Projection{}, ferr
	}

	return Projection{Kind: ProjFields, Fields: fields}, nil
}

func (p *parser) parseFieldList() ([]ProjField, error) {
	var fields []ProjField

	for {
		segs, err := p.parseSegments()
		if err != nil {
			return nil, err
		}

		field := ProjField{Path: segs}

		if p.accept("AS") {
			if p.peek().kind != tIdent {
				return nil, cerrors.Newf(cerrors.InvalidArgument, "expected an alias but found %q", p.peek().text)
			}

			field.Alias = p.next().text
		}

		fields = append(fields, field)

		if p.peek().kind != tComma {
			return fields, nil
		}

		p.next()
	}
}

// tryAggregate parses an aggregate call (COUNT/SUM/AVG/MIN/MAX) when the cursor
// is on one; ok is false (without consuming) otherwise.
func (p *parser) tryAggregate() (*Aggregate, bool, error) {
	t := p.peek()
	if t.kind != tIdent || !isAggregate(t.text) || p.toks[p.pos+1].kind != tLParen {
		return nil, false, nil
	}

	fn := strings.ToUpper(p.next().text)
	p.next() // '('

	agg := &Aggregate{Func: fn}

	// COUNT(1) / COUNT(*) take no path; the others aggregate a property.
	if fn == "COUNT" && (p.peek().kind == tStar || p.peek().kind == tNumber) {
		p.next()
	} else {
		segs, err := p.parseSegments()
		if err != nil {
			return nil, false, err
		}

		agg.Path = segs
	}

	if err := p.expectKind(tRParen, "')'"); err != nil {
		return nil, false, err
	}

	return agg, true, nil
}

func (p *parser) stripProjection(proj Projection) Projection {
	switch proj.Kind {
	case ProjFields:
		for i := range proj.Fields {
			proj.Fields[i].Path = p.strip(proj.Fields[i].Path)
		}
	case ProjValue:
		proj.ValuePath = p.strip(proj.ValuePath)
	case ProjAggregate:
		if proj.Aggregate.Path != nil {
			proj.Aggregate.Path = p.strip(proj.Aggregate.Path)
		}
	case ProjStar:
	}

	return proj
}

// tryBoolFunc parses a boolean SQL function (STARTSWITH/CONTAINS/IS_DEFINED/
// IS_NULL/ARRAY_CONTAINS) into a node; ok is false (without consuming) when the
// cursor is not on such a call.
func (p *parser) tryBoolFunc() (expr.Node, bool, error) {
	t := p.peek()
	if t.kind != tIdent || !isBoolFunc(t.text) || p.toks[p.pos+1].kind != tLParen {
		return nil, false, nil
	}

	fn := strings.ToUpper(p.next().text)
	p.next() // '('

	node, err := p.finishBoolFunc(fn)

	return node, true, err
}

const fnIsDefined = "IS_DEFINED"

func (p *parser) finishBoolFunc(fn string) (expr.Node, error) {
	if fn == fnIsDefined || fn == "IS_NULL" {
		return p.finishUnaryFunc(fn)
	}

	path, arg, err := p.parsePathAndArg()
	if err != nil {
		return nil, err
	}

	switch fn {
	case "STARTSWITH":
		return &expr.BeginsWith{Path: path, Prefix: arg}, nil
	case "CONTAINS":
		return &expr.Contains{Path: path, Operand: arg}, nil
	default: // ARRAY_CONTAINS — gate on the field being an array (see isArray).
		return &expr.And{Left: isArray(path), Right: &expr.Contains{Path: path, Operand: arg}}, nil
	}
}

func (p *parser) finishUnaryFunc(fn string) (expr.Node, error) {
	path, err := p.parsePathOperand()
	if err != nil {
		return nil, err
	}

	if rerr := p.expectKind(tRParen, "')'"); rerr != nil {
		return nil, rerr
	}

	if fn == fnIsDefined {
		return &expr.AttrExists{Path: path}, nil
	}

	return &expr.Comparison{Op: "=", Left: path, Right: &expr.ValueOperand{Value: nil}}, nil
}

func (p *parser) parsePathAndArg() (*expr.PathOperand, expr.Operand, error) {
	path, err := p.parsePathOperand()
	if err != nil {
		return nil, nil, err
	}

	if cerr := p.expectKind(tComma, "','"); cerr != nil {
		return nil, nil, cerr
	}

	arg, aerr := p.parseOperand()
	if aerr != nil {
		return nil, nil, aerr
	}

	if rerr := p.expectKind(tRParen, "')'"); rerr != nil {
		return nil, nil, rerr
	}

	return path, arg, nil
}

// isArray is true when path resolves to a list ("L" is expr.dynamoType's code
// for a native []any), gating ARRAY_CONTAINS off the substring match that the
// shared Contains node does on strings.
func isArray(path *expr.PathOperand) expr.Node {
	return &expr.AttrType{Path: path, Type: &expr.ValueOperand{Value: "L"}}
}

//nolint:gochecknoglobals // static keyword sets
var (
	aggregateFuncs = map[string]bool{"COUNT": true, "SUM": true, "AVG": true, "MIN": true, "MAX": true}
	boolFuncs      = map[string]bool{
		"STARTSWITH": true, "CONTAINS": true, "IS_DEFINED": true, "IS_NULL": true, "ARRAY_CONTAINS": true,
	}
)

func isAggregate(name string) bool { return aggregateFuncs[strings.ToUpper(name)] }
func isBoolFunc(name string) bool  { return boolFuncs[strings.ToUpper(name)] }
