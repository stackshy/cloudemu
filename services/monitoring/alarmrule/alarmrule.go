// Package alarmrule parses and evaluates composite alarm rules such as
// `ALARM(cpu) AND NOT OK("disk")`. The grammar follows the AlarmRule of the
// CloudWatch PutCompositeAlarm API: ALARM, OK and INSUFFICIENT_DATA calls over
// an alarm name or ARN, TRUE, FALSE, AND, OR, NOT and parentheses. NOT binds
// tighter than AND, and AND binds tighter than OR.
package alarmrule

import (
	"errors"
	"fmt"
	"strings"
)

// Limits from the PutCompositeAlarm API reference.
const (
	MaxChildren = 100
	MaxElements = 500
	MaxLength   = 10240
)

// Rule literals.
const (
	literalTrue  = "TRUE"
	literalFalse = "FALSE"
)

// ErrSyntax is wrapped by every parse error that is not a limit.
var ErrSyntax = errors.New("invalid AlarmRule")

// ErrLimit is wrapped by the errors for rules over the API limits.
var ErrLimit = errors.New("AlarmRule over limit")

// Alarm states a rule can test.
const (
	StateAlarm            = "ALARM"
	StateOK               = "OK"
	StateInsufficientData = "INSUFFICIENT_DATA"
)

type kind int

const (
	kindLiteral kind = iota
	kindState
	kindNot
	kindAnd
	kindOr
)

// node is one parsed expression.
type node struct {
	kind  kind
	value bool   // kindLiteral
	state string // kindState
	ref   string // kindState
	left  *node
	right *node
}

// Rule is a parsed alarm rule.
type Rule struct {
	root     *node
	refs     []string
	elements int
}

// Parse parses an alarm rule. It rejects syntax errors and rules over the API
// limits.
func Parse(rule string) (*Rule, error) {
	if len(rule) > MaxLength {
		return nil, fmt.Errorf("%w: AlarmRule must be at most %d characters", ErrLimit, MaxLength)
	}

	p := &parser{src: rule}

	root, err := p.parseOr()
	if err != nil {
		return nil, err
	}

	p.skipSpace()

	if p.pos < len(p.src) {
		return nil, fmt.Errorf("%w: unexpected %q at position %d", ErrSyntax, p.src[p.pos:], p.pos)
	}

	r := &Rule{root: root, elements: p.elements, refs: dedupe(p.refs)}

	if len(r.refs) > MaxChildren {
		return nil, fmt.Errorf("%w: AlarmRule can reference at most %d alarms", ErrLimit, MaxChildren)
	}

	if r.elements > MaxElements {
		return nil, fmt.Errorf("%w: AlarmRule can have at most %d elements", ErrLimit, MaxElements)
	}

	return r, nil
}

// Refs returns the alarm names or ARNs the rule references, each once, in
// the order they first appear.
func (r *Rule) Refs() []string {
	return append([]string{}, r.refs...)
}

// Eval evaluates the rule. stateOf returns the current state of a referenced
// alarm.
func (r *Rule) Eval(stateOf func(ref string) string) bool {
	return eval(r.root, stateOf)
}

func eval(n *node, stateOf func(string) string) bool {
	switch n.kind {
	case kindLiteral:
		return n.value
	case kindState:
		return stateOf(n.ref) == n.state
	case kindNot:
		return !eval(n.left, stateOf)
	case kindAnd:
		return eval(n.left, stateOf) && eval(n.right, stateOf)
	case kindOr:
		return eval(n.left, stateOf) || eval(n.right, stateOf)
	default:
		return false
	}
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))

	for _, s := range in {
		if seen[s] {
			continue
		}

		seen[s] = true

		out = append(out, s)
	}

	return out
}

// errUnexpectedEnd reports a rule that stops before it is complete.
func errUnexpectedEnd() error { return fmt.Errorf("%w: unexpected end", ErrSyntax) }

// parser is a recursive-descent parser over the rule text.
type parser struct {
	src      string
	pos      int
	refs     []string
	elements int
}

func (p *parser) skipSpace() {
	for p.pos < len(p.src) && isSpace(p.src[p.pos]) {
		p.pos++
	}
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func isWordChar(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}

// peekWord returns the keyword at the cursor without consuming it.
func (p *parser) peekWord() string {
	p.skipSpace()

	end := p.pos
	for end < len(p.src) && isWordChar(p.src[end]) {
		end++
	}

	return p.src[p.pos:end]
}

func (p *parser) parseOr() (*node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	for p.peekWord() == "OR" {
		p.pos += len("OR")

		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}

		left = &node{kind: kindOr, left: left, right: right}
	}

	return left, nil
}

func (p *parser) parseAnd() (*node, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}

	for p.peekWord() == "AND" {
		p.pos += len("AND")

		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}

		left = &node{kind: kindAnd, left: left, right: right}
	}

	return left, nil
}

func (p *parser) parseNot() (*node, error) {
	if p.peekWord() == "NOT" {
		p.pos += len("NOT")

		inner, err := p.parseNot()
		if err != nil {
			return nil, err
		}

		return &node{kind: kindNot, left: inner}, nil
	}

	return p.parsePrimary()
}

func (p *parser) parsePrimary() (*node, error) {
	p.skipSpace()

	if p.pos >= len(p.src) {
		return nil, errUnexpectedEnd()
	}

	if p.src[p.pos] == '(' {
		p.pos++
		p.elements++

		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}

		if err := p.expect(')'); err != nil {
			return nil, err
		}

		return inner, nil
	}

	word := p.peekWord()

	switch word {
	case literalTrue, literalFalse:
		p.pos += len(word)
		p.elements++

		return &node{kind: kindLiteral, value: word == literalTrue}, nil
	case StateAlarm, StateOK, StateInsufficientData:
		p.pos += len(word)

		return p.parseCall(word)
	case "":
		return nil, fmt.Errorf("%w: unexpected %q at position %d", ErrSyntax, p.src[p.pos:p.pos+1], p.pos)
	default:
		return nil, fmt.Errorf("%w: unknown function or keyword %q", ErrSyntax, word)
	}
}

// parseCall parses the "(ref)" of a state call.
func (p *parser) parseCall(state string) (*node, error) {
	if err := p.expect('('); err != nil {
		return nil, err
	}

	ref, err := p.parseRef()
	if err != nil {
		return nil, err
	}

	if err := p.expect(')'); err != nil {
		return nil, err
	}

	p.elements++
	p.refs = append(p.refs, ref)

	return &node{kind: kindState, state: state, ref: ref}, nil
}

// parseRef reads an alarm name or ARN, bare or in double quotes. A bare name
// runs up to the closing parenthesis.
func (p *parser) parseRef() (string, error) {
	p.skipSpace()

	if p.pos >= len(p.src) {
		return "", errUnexpectedEnd()
	}

	var ref string

	if p.src[p.pos] == '"' {
		end := strings.IndexByte(p.src[p.pos+1:], '"')
		if end < 0 {
			return "", fmt.Errorf("%w: unterminated quoted alarm name", ErrSyntax)
		}

		ref = p.src[p.pos+1 : p.pos+1+end]
		p.pos += end + 2
	} else {
		end := strings.IndexByte(p.src[p.pos:], ')')
		if end < 0 {
			return "", errUnexpectedEnd()
		}

		ref = strings.TrimSpace(p.src[p.pos : p.pos+end])
		p.pos += end
	}

	if ref == "" {
		return "", fmt.Errorf("%w: empty alarm name", ErrSyntax)
	}

	return ref, nil
}

func (p *parser) expect(c byte) error {
	p.skipSpace()

	if p.pos >= len(p.src) {
		return errUnexpectedEnd()
	}

	if p.src[p.pos] != c {
		return fmt.Errorf("%w: expected %q at position %d", ErrSyntax, c, p.pos)
	}

	p.pos++

	return nil
}
