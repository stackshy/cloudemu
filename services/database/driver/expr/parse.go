package expr

import (
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Function name literals shared by the lexer and parser.
const (
	fnSize       = "size"
	fnAttrExists = "attribute_exists"
)

// ParseCondition parses a DynamoDB condition/filter expression into an AST.
// names resolves #alias steps (ExpressionAttributeNames) and values resolves
// :placeholder operands (ExpressionAttributeValues, already native). It
// returns a cerrors.InvalidArgument error on any malformed input.
func ParseCondition(raw string, names map[string]string, values map[string]any) (Node, error) {
	toks, err := lex(raw)
	if err != nil {
		return nil, err
	}

	p := &parser{toks: toks, names: names, values: values}
	if p.peek().kind == tEOF {
		return nil, cerrors.New(cerrors.InvalidArgument, "empty expression")
	}

	node, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	if p.peek().kind != tEOF {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unexpected trailing token %q", p.peek().text)
	}

	return node, nil
}

type parser struct {
	toks   []token
	pos    int
	names  map[string]string
	values map[string]any
}

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) next() token {
	t := p.toks[p.pos]

	if p.pos < len(p.toks)-1 {
		p.pos++
	}

	return t
}

func (p *parser) expect(kind tokenKind, what string) error {
	if p.peek().kind != kind {
		return cerrors.Newf(cerrors.InvalidArgument, "expected %s but found %q", what, p.peek().text)
	}

	p.next()

	return nil
}

// parseExpr → OR (lowest precedence).
func (p *parser) parseExpr() (Node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	for p.peek().kind == tOr {
		p.next()

		right, rerr := p.parseAnd()
		if rerr != nil {
			return nil, rerr
		}

		left = &Or{Left: left, Right: right}
	}

	return left, nil
}

func (p *parser) parseAnd() (Node, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}

	for p.peek().kind == tAnd {
		p.next()

		right, rerr := p.parseNot()
		if rerr != nil {
			return nil, rerr
		}

		left = &And{Left: left, Right: right}
	}

	return left, nil
}

func (p *parser) parseNot() (Node, error) {
	if p.peek().kind == tNot {
		p.next()

		child, err := p.parseNot()
		if err != nil {
			return nil, err
		}

		return &Not{Child: child}, nil
	}

	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Node, error) {
	if p.peek().kind == tLParen {
		p.next()

		node, err := p.parseExpr()
		if err != nil {
			return nil, err
		}

		if perr := p.expect(tRParen, "')'"); perr != nil {
			return nil, perr
		}

		return node, nil
	}

	return p.parseCondition()
}

func (p *parser) parseCondition() (Node, error) {
	if p.peek().kind == tFunc && p.peek().text != fnSize {
		return p.parseBooleanFunc()
	}

	left, err := p.parseOperand()
	if err != nil {
		return nil, err
	}

	//nolint:exhaustive // remaining token kinds fall through to the error default.
	switch p.peek().kind {
	case tBetween:
		return p.parseBetween(left)
	case tIn:
		return p.parseIn(left)
	case tOp:
		op := p.next().text

		right, rerr := p.parseOperand()
		if rerr != nil {
			return nil, rerr
		}

		return &Comparison{Op: op, Left: left, Right: right}, nil
	default:
		return nil, cerrors.Newf(cerrors.InvalidArgument, "expected an operator but found %q", p.peek().text)
	}
}

func (p *parser) parseBetween(left Operand) (Node, error) {
	p.next()

	lo, err := p.parseOperand()
	if err != nil {
		return nil, err
	}

	if aerr := p.expect(tAnd, "'AND'"); aerr != nil {
		return nil, aerr
	}

	hi, herr := p.parseOperand()
	if herr != nil {
		return nil, herr
	}

	return &Between{Operand: left, Lo: lo, Hi: hi}, nil
}

func (p *parser) parseIn(left Operand) (Node, error) {
	p.next()

	if err := p.expect(tLParen, "'('"); err != nil {
		return nil, err
	}

	list := []Operand{}

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

	if err := p.expect(tRParen, "')'"); err != nil {
		return nil, err
	}

	return &In{Operand: left, List: list}, nil
}

func (p *parser) parseBooleanFunc() (Node, error) {
	fn := p.next().text

	if err := p.expect(tLParen, "'('"); err != nil {
		return nil, err
	}

	if fn == fnAttrExists || fn == "attribute_not_exists" {
		return p.finishUnaryFunc(fn)
	}

	return p.finishBinaryFunc(fn)
}

func (p *parser) finishUnaryFunc(fn string) (Node, error) {
	path, err := p.parsePath()
	if err != nil {
		return nil, err
	}

	if rerr := p.expect(tRParen, "')'"); rerr != nil {
		return nil, rerr
	}

	if fn == fnAttrExists {
		return &AttrExists{Path: path}, nil
	}

	return &AttrNotExists{Path: path}, nil
}

func (p *parser) finishBinaryFunc(fn string) (Node, error) {
	path, err := p.parsePath()
	if err != nil {
		return nil, err
	}

	if cerr := p.expect(tComma, "','"); cerr != nil {
		return nil, cerr
	}

	arg, aerr := p.parseOperand()
	if aerr != nil {
		return nil, aerr
	}

	if rerr := p.expect(tRParen, "')'"); rerr != nil {
		return nil, rerr
	}

	switch fn {
	case "attribute_type":
		return &AttrType{Path: path, Type: arg}, nil
	case "begins_with":
		return &BeginsWith{Path: path, Prefix: arg}, nil
	default:
		return &Contains{Path: path, Operand: arg}, nil
	}
}

func (p *parser) parseOperand() (Operand, error) {
	//nolint:exhaustive // remaining token kinds fall through to the error default.
	switch p.peek().kind {
	case tValue:
		return p.parseValue()
	case tFunc:
		if p.peek().text != fnSize {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "function %q cannot be used as an operand", p.peek().text)
		}

		return p.parseSize()
	case tIdent, tAlias:
		return p.parsePath()
	default:
		return nil, cerrors.Newf(cerrors.InvalidArgument, "expected an operand but found %q", p.peek().text)
	}
}

func (p *parser) parseValue() (Operand, error) {
	name := p.next().text

	v, ok := p.values[name]
	if !ok {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "no value supplied for placeholder %q", name)
	}

	return &ValueOperand{Value: v}, nil
}

func (p *parser) parseSize() (Operand, error) {
	p.next()

	if err := p.expect(tLParen, "'('"); err != nil {
		return nil, err
	}

	path, err := p.parsePath()
	if err != nil {
		return nil, err
	}

	if rerr := p.expect(tRParen, "')'"); rerr != nil {
		return nil, rerr
	}

	return &SizeOperand{Path: path}, nil
}

func (p *parser) parsePath() (*PathOperand, error) {
	name, err := p.parseName()
	if err != nil {
		return nil, err
	}

	parts := []PathPart{{Name: name}}

	for {
		//nolint:exhaustive // only '.' and '[' continue a path; anything else ends it.
		switch p.peek().kind {
		case tDot:
			p.next()

			next, nerr := p.parseName()
			if nerr != nil {
				return nil, nerr
			}

			parts = append(parts, PathPart{Name: next})
		case tLBracket:
			idx, ierr := p.parseIndex()
			if ierr != nil {
				return nil, ierr
			}

			parts = append(parts, PathPart{Index: idx, IsIndex: true})
		default:
			return &PathOperand{Parts: parts}, nil
		}
	}
}

func (p *parser) parseName() (string, error) {
	tok := p.peek()

	//nolint:exhaustive // only identifiers and aliases name attributes.
	switch tok.kind {
	case tIdent:
		p.next()

		return tok.text, nil
	case tAlias:
		p.next()

		resolved, ok := p.names[tok.text]
		if !ok {
			return "", cerrors.Newf(cerrors.InvalidArgument, "no name supplied for alias %q", tok.text)
		}

		return resolved, nil
	default:
		return "", cerrors.Newf(cerrors.InvalidArgument, "expected an attribute name but found %q", tok.text)
	}
}

func (p *parser) parseIndex() (int, error) {
	p.next()

	tok := p.peek()
	if tok.kind != tNumber {
		return 0, cerrors.Newf(cerrors.InvalidArgument, "expected a list index but found %q", tok.text)
	}

	p.next()

	idx, err := strconv.Atoi(tok.text)
	if err != nil {
		return 0, cerrors.Newf(cerrors.InvalidArgument, "invalid list index %q", tok.text)
	}

	if rerr := p.expect(tRBracket, "']'"); rerr != nil {
		return 0, rerr
	}

	return idx, nil
}
