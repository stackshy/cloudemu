package expr

import (
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// UpdateProgram is a parsed DynamoDB UpdateExpression: the actions of its
// SET / REMOVE / ADD / DELETE clauses.
type UpdateProgram struct {
	sets    []setItem
	removes []*PathOperand
	adds    []pathValue
	deletes []pathValue
}

type setItem struct {
	path *PathOperand
	rhs  updOperand
}

type pathValue struct {
	path  *PathOperand
	value *ValueOperand
}

// updOperand is a SET right-hand-side operand: a leaf (value or path),
// arithmetic (+/-), if_not_exists, or list_append.
type updOperand interface{ isUpdOperand() }

type updLeaf struct{ operand Operand }

type updArith struct {
	op    string
	left  updOperand
	right updOperand
}

type updIfNotExists struct {
	path *PathOperand
	def  updOperand
}

type updListAppend struct {
	left  updOperand
	right updOperand
}

func (*updLeaf) isUpdOperand()        {}
func (*updArith) isUpdOperand()       {}
func (*updIfNotExists) isUpdOperand() {}
func (*updListAppend) isUpdOperand()  {}

// ParseUpdate parses a DynamoDB UpdateExpression. names resolves #alias steps
// and values resolves :placeholder operands (already native). It returns a
// cerrors.InvalidArgument error on malformed input.
func ParseUpdate(raw string, names map[string]string, values map[string]any) (*UpdateProgram, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "empty update expression")
	}

	toks, err := lex(raw)
	if err != nil {
		return nil, err
	}

	p := &parser{toks: toks, names: names, values: values}
	prog := &UpdateProgram{}

	for p.peek().kind != tEOF {
		if cerr := p.parseClause(prog); cerr != nil {
			return nil, cerr
		}
	}

	return prog, nil
}

func (p *parser) parseClause(prog *UpdateProgram) error {
	kw := p.peek()
	if kw.kind != tIdent {
		return cerrors.Newf(cerrors.InvalidArgument, "expected an update clause keyword but found %q", kw.text)
	}

	p.next()

	switch strings.ToUpper(kw.text) {
	case "SET":
		return p.parseSetClause(prog)
	case "REMOVE":
		return p.parseRemoveClause(prog)
	case "ADD":
		return p.parseAddClause(prog)
	case "DELETE":
		return p.parseDeleteClause(prog)
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "unknown update clause keyword %q", kw.text)
	}
}

func (p *parser) parseSetClause(prog *UpdateProgram) error {
	for {
		path, err := p.parsePath()
		if err != nil {
			return err
		}

		if eq := p.next(); eq.kind != tOp || eq.text != "=" {
			return cerrors.Newf(cerrors.InvalidArgument, "expected '=' in SET but found %q", eq.text)
		}

		rhs, rerr := p.parseSetOperand()
		if rerr != nil {
			return rerr
		}

		prog.sets = append(prog.sets, setItem{path: path, rhs: rhs})

		if p.peek().kind != tComma {
			return nil
		}

		p.next()
	}
}

func (p *parser) parseRemoveClause(prog *UpdateProgram) error {
	for {
		path, err := p.parsePath()
		if err != nil {
			return err
		}

		prog.removes = append(prog.removes, path)

		if p.peek().kind != tComma {
			return nil
		}

		p.next()
	}
}

func (p *parser) parseAddClause(prog *UpdateProgram) error {
	items, err := p.parsePathValueList()
	if err != nil {
		return err
	}

	prog.adds = append(prog.adds, items...)

	return nil
}

func (p *parser) parseDeleteClause(prog *UpdateProgram) error {
	items, err := p.parsePathValueList()
	if err != nil {
		return err
	}

	prog.deletes = append(prog.deletes, items...)

	return nil
}

// parsePathValueList parses the "path value[, path value]" form shared by ADD
// and DELETE.
func (p *parser) parsePathValueList() ([]pathValue, error) {
	var out []pathValue

	for {
		path, err := p.parsePath()
		if err != nil {
			return nil, err
		}

		if p.peek().kind != tValue {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "expected a value but found %q", p.peek().text)
		}

		v, verr := p.parseValue()
		if verr != nil {
			return nil, verr
		}

		out = append(out, pathValue{path: path, value: v.(*ValueOperand)})

		if p.peek().kind != tComma {
			return out, nil
		}

		p.next()
	}
}

// parseSetOperand parses a SET right-hand side: term (('+' | '-') term)*.
func (p *parser) parseSetOperand() (updOperand, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}

	for p.peek().kind == tOp && (p.peek().text == "+" || p.peek().text == "-") {
		op := p.next().text

		right, rerr := p.parseTerm()
		if rerr != nil {
			return nil, rerr
		}

		left = &updArith{op: op, left: left, right: right}
	}

	return left, nil
}

func (p *parser) parseTerm() (updOperand, error) {
	tok := p.peek()

	//nolint:exhaustive // only values, paths and the two update functions are terms.
	switch tok.kind {
	case tValue:
		v, err := p.parseValue()
		if err != nil {
			return nil, err
		}

		return &updLeaf{operand: v}, nil
	case tIdent, tAlias:
		if p.isUpdateFunc(tok) {
			return p.parseUpdateFunc(tok.text)
		}

		path, err := p.parsePath()
		if err != nil {
			return nil, err
		}

		return &updLeaf{operand: path}, nil
	default:
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unexpected token %q in SET operand", tok.text)
	}
}

func (p *parser) isUpdateFunc(tok token) bool {
	if tok.kind != tIdent {
		return false
	}

	name := strings.ToLower(tok.text)
	if name != "if_not_exists" && name != "list_append" {
		return false
	}

	return p.toks[p.pos+1].kind == tLParen
}

func (p *parser) parseUpdateFunc(name string) (updOperand, error) {
	p.next() // consume the function name

	if err := p.expect(tLParen, "'('"); err != nil {
		return nil, err
	}

	if strings.EqualFold(name, "if_not_exists") {
		return p.parseIfNotExists()
	}

	return p.parseListAppend()
}

func (p *parser) parseIfNotExists() (updOperand, error) {
	path, err := p.parsePath()
	if err != nil {
		return nil, err
	}

	if cerr := p.expect(tComma, "','"); cerr != nil {
		return nil, cerr
	}

	def, derr := p.parseSetOperand()
	if derr != nil {
		return nil, derr
	}

	if rerr := p.expect(tRParen, "')'"); rerr != nil {
		return nil, rerr
	}

	return &updIfNotExists{path: path, def: def}, nil
}

func (p *parser) parseListAppend() (updOperand, error) {
	left, err := p.parseSetOperand()
	if err != nil {
		return nil, err
	}

	if cerr := p.expect(tComma, "','"); cerr != nil {
		return nil, cerr
	}

	right, rerr := p.parseSetOperand()
	if rerr != nil {
		return nil, rerr
	}

	if rerr := p.expect(tRParen, "')'"); rerr != nil {
		return nil, rerr
	}

	return &updListAppend{left: left, right: right}, nil
}
