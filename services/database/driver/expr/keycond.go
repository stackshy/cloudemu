package expr

import (
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// KeyCond is a parsed DynamoDB KeyConditionExpression: an equality on the
// partition key and an optional sort-key condition. SortOp is "" when no sort
// condition is present, otherwise one of = < <= > >= BETWEEN BEGINS_WITH.
type KeyCond struct {
	PartitionKey string
	PartitionVal any
	SortKey      string
	SortOp       string
	SortVal      any
	SortValEnd   any // for BETWEEN
}

// ParseKeyCondition parses a KeyConditionExpression using the shared lexer, so
// it is tolerant of spacing (e.g. "sk>:v") and enforces the restricted key
// grammar: equality on the partition key and one optional sort-key condition.
func ParseKeyCondition(raw string, names map[string]string, values map[string]any) (KeyCond, error) {
	toks, err := lex(strings.TrimSpace(raw))
	if err != nil {
		return KeyCond{}, err
	}

	p := &parser{toks: toks, names: names, values: values}

	kc, perr := p.parseKeyCond()
	if perr != nil {
		return KeyCond{}, perr
	}

	if p.peek().kind != tEOF {
		return KeyCond{}, cerrors.Newf(cerrors.InvalidArgument,
			"unexpected token %q in key condition", p.peek().text)
	}

	return kc, nil
}

func (p *parser) parseKeyCond() (KeyCond, error) {
	name, err := p.keyName()
	if err != nil {
		return KeyCond{}, err
	}

	if eq := p.next(); eq.kind != tOp || eq.text != "=" {
		return KeyCond{}, cerrors.Newf(cerrors.InvalidArgument,
			"partition key must use '=' but found %q", eq.text)
	}

	val, verr := p.keyValue()
	if verr != nil {
		return KeyCond{}, verr
	}

	kc := KeyCond{PartitionKey: name, PartitionVal: val}

	if p.peek().kind == tAnd {
		p.next()

		if serr := p.parseSortCond(&kc); serr != nil {
			return KeyCond{}, serr
		}
	}

	return kc, nil
}

func (p *parser) parseSortCond(kc *KeyCond) error {
	if p.peek().kind == tFunc {
		return p.parseBeginsWithCond(kc)
	}

	name, err := p.keyName()
	if err != nil {
		return err
	}

	kc.SortKey = name

	if p.peek().kind == tBetween {
		return p.parseBetweenCond(kc)
	}

	op := p.next()
	if op.kind != tOp || !isKeyOp(op.text) {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid sort key operator %q", op.text)
	}

	val, verr := p.keyValue()
	if verr != nil {
		return verr
	}

	kc.SortOp = op.text
	kc.SortVal = val

	return nil
}

func (p *parser) parseBeginsWithCond(kc *KeyCond) error {
	fn := p.next().text
	if !strings.EqualFold(fn, "begins_with") {
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported function %q in key condition", fn)
	}

	if err := p.expect(tLParen, "'('"); err != nil {
		return err
	}

	name, nerr := p.keyName()
	if nerr != nil {
		return nerr
	}

	if cerr := p.expect(tComma, "','"); cerr != nil {
		return cerr
	}

	val, verr := p.keyValue()
	if verr != nil {
		return verr
	}

	if rerr := p.expect(tRParen, "')'"); rerr != nil {
		return rerr
	}

	kc.SortKey = name
	kc.SortOp = "BEGINS_WITH"
	kc.SortVal = val

	return nil
}

func (p *parser) parseBetweenCond(kc *KeyCond) error {
	p.next() // consume BETWEEN

	lo, lerr := p.keyValue()
	if lerr != nil {
		return lerr
	}

	if aerr := p.expect(tAnd, "'AND'"); aerr != nil {
		return aerr
	}

	hi, herr := p.keyValue()
	if herr != nil {
		return herr
	}

	kc.SortOp = "BETWEEN"
	kc.SortVal = lo
	kc.SortValEnd = hi

	return nil
}

// keyName parses a single attribute name (resolving a #alias). Key attributes
// are simple top-level names — nested paths and indexes are rejected.
func (p *parser) keyName() (string, error) {
	path, err := p.parsePath()
	if err != nil {
		return "", err
	}

	if len(path.Parts) != 1 || path.Parts[0].IsIndex {
		return "", cerrors.New(cerrors.InvalidArgument, "key condition attributes must be simple names")
	}

	return path.Parts[0].Name, nil
}

// keyValue parses a :placeholder and returns its resolved native value.
func (p *parser) keyValue() (any, error) {
	op, err := p.parseValue()
	if err != nil {
		return nil, err
	}

	v, ok := op.(*ValueOperand)
	if !ok {
		return nil, cerrors.New(cerrors.InvalidArgument, "key condition expects a value placeholder")
	}

	return v.Value, nil
}

func isKeyOp(op string) bool {
	switch op {
	case "=", "<", opLTE, ">", opGTE:
		return true
	default:
		return false
	}
}
