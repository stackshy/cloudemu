package glue

import (
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
)

// Comparison operators recognized in a partition-filter expression.
const (
	opEq = "="
	opNe = "<>"
	opGt = ">"
	opLt = "<"
	opGe = ">="
	opLe = "<="
)

// twoCharOpLen is the byte length of a two-character comparison operator.
const twoCharOpLen = 2

// filterErrf builds an InvalidArgument-typed error for a malformed filter.
func filterErrf(format string, args ...any) error {
	return errors.Newf(errors.InvalidArgument, format, args...)
}

// This file implements the subset of the AWS Glue GetPartitions filter grammar
// that Terraform and Athena emit in practice: comparisons of a partition key
// against a string or number literal (= <> > < >= <=), combined with AND / OR
// and grouped with parentheses. Keys are matched positionally against a
// partition's Values via the table's partition-key order. Numeric literals
// compare numerically when the value parses as a number, else lexically. An
// unsupported or malformed expression is reported as an InvalidInputException by
// the caller.

// partPredicate reports whether a partition's ordered Values satisfy a filter.
// idx maps a partition-key name to its position in Values.
type partPredicate func(values []string, idx map[string]int) bool

type tokenKind int

const (
	tkEOF tokenKind = iota
	tkIdent
	tkString
	tkNumber
	tkOp
	tkAnd
	tkOr
	tkLParen
	tkRParen
)

type token struct {
	kind tokenKind
	text string
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isDigitOrSign(c byte) bool { return isDigit(c) || c == '-' || c == '+' }

func isOpStart(c byte) bool { return c == '=' || c == '<' || c == '>' }

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool { return isIdentStart(c) || isDigit(c) }

// tokenizeFilter splits a filter expression into tokens, skipping whitespace.
func tokenizeFilter(s string) ([]token, error) {
	var toks []token

	for i := 0; i < len(s); {
		if isSpace(s[i]) {
			i++

			continue
		}

		t, next, err := nextToken(s, i)
		if err != nil {
			return nil, err
		}

		toks = append(toks, t)
		i = next
	}

	return append(toks, token{kind: tkEOF}), nil
}

// nextToken scans the single token beginning at s[i].
func nextToken(s string, i int) (token, int, error) {
	c := s[i]

	switch {
	case c == '(':
		return token{kind: tkLParen}, i + 1, nil
	case c == ')':
		return token{kind: tkRParen}, i + 1, nil
	case c == '\'':
		return scanString(s, i)
	case isOpStart(c):
		t, n := scanOp(s, i)

		return t, n, nil
	case isDigitOrSign(c):
		t, n := scanNumber(s, i)

		return t, n, nil
	case isIdentStart(c):
		t, n := scanIdentOrKeyword(s, i)

		return t, n, nil
	default:
		return token{}, i, filterErrf("unexpected character %q", string(c))
	}
}

// scanString reads a single-quoted string literal starting at s[i].
func scanString(s string, i int) (token, int, error) {
	var b strings.Builder

	for j := i + 1; j < len(s); j++ {
		if s[j] == '\'' {
			return token{kind: tkString, text: b.String()}, j + 1, nil
		}

		b.WriteByte(s[j])
	}

	return token{}, i, filterErrf("unterminated string literal")
}

// scanOp reads a comparison operator (one or two characters) starting at s[i].
func scanOp(s string, i int) (tok token, next int) {
	if i+twoCharOpLen <= len(s) {
		if two := s[i : i+twoCharOpLen]; two == opGe || two == opLe || two == opNe {
			return token{kind: tkOp, text: two}, i + twoCharOpLen
		}
	}

	return token{kind: tkOp, text: s[i : i+1]}, i + 1
}

// scanNumber reads a signed decimal number starting at s[i].
func scanNumber(s string, i int) (tok token, next int) {
	j := i
	if s[j] == '-' || s[j] == '+' {
		j++
	}

	for j < len(s) && (isDigit(s[j]) || s[j] == '.') {
		j++
	}

	return token{kind: tkNumber, text: s[i:j]}, j
}

// scanIdentOrKeyword reads an identifier, mapping AND/OR to boolean keywords.
func scanIdentOrKeyword(s string, i int) (tok token, next int) {
	j := i
	for j < len(s) && isIdentPart(s[j]) {
		j++
	}

	word := s[i:j]

	switch strings.ToUpper(word) {
	case "AND":
		return token{kind: tkAnd, text: word}, j
	case "OR":
		return token{kind: tkOr, text: word}, j
	default:
		return token{kind: tkIdent, text: word}, j
	}
}

type filterParser struct {
	toks []token
	pos  int
}

func (p *filterParser) peek() token { return p.toks[p.pos] }

func (p *filterParser) next() token {
	t := p.toks[p.pos]
	p.pos++

	return t
}

// parseOr parses OR-joined AND groups (lowest precedence).
func (p *filterParser) parseOr() (partPredicate, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	for p.peek().kind == tkOr {
		p.next()

		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}

		left = orPred(left, right)
	}

	return left, nil
}

// parseAnd parses AND-joined primary comparisons.
func (p *filterParser) parseAnd() (partPredicate, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	for p.peek().kind == tkAnd {
		p.next()

		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}

		left = andPred(left, right)
	}

	return left, nil
}

// parsePrimary parses a parenthesized expression or a single comparison.
func (p *filterParser) parsePrimary() (partPredicate, error) {
	if p.peek().kind == tkLParen {
		p.next()

		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}

		if p.next().kind != tkRParen {
			return nil, filterErrf("expected closing parenthesis")
		}

		return inner, nil
	}

	return p.parseComparison()
}

// parseComparison parses "<key> <op> <literal>".
func (p *filterParser) parseComparison() (partPredicate, error) {
	key := p.next()
	if key.kind != tkIdent {
		return nil, filterErrf("expected a partition key")
	}

	op := p.next()
	if op.kind != tkOp {
		return nil, filterErrf("expected a comparison operator")
	}

	lit := p.next()
	if lit.kind != tkString && lit.kind != tkNumber {
		return nil, filterErrf("expected a string or number literal")
	}

	return comparePred(key.text, op.text, lit), nil
}

func orPred(a, b partPredicate) partPredicate {
	return func(v []string, idx map[string]int) bool { return a(v, idx) || b(v, idx) }
}

func andPred(a, b partPredicate) partPredicate {
	return func(v []string, idx map[string]int) bool { return a(v, idx) && b(v, idx) }
}

// comparePred builds a predicate comparing one partition key to a literal.
func comparePred(key, op string, lit token) partPredicate {
	isNum := lit.kind == tkNumber
	num, _ := strconv.ParseFloat(lit.text, 64)

	return func(v []string, idx map[string]int) bool {
		i, ok := idx[key]
		if !ok || i >= len(v) {
			return false
		}

		return matchValue(v[i], op, lit.text, isNum, num)
	}
}

// matchValue compares a partition value against a literal, numerically when both
// are numbers and lexically otherwise.
func matchValue(val, op, lit string, isNum bool, num float64) bool {
	if isNum {
		if fv, err := strconv.ParseFloat(val, 64); err == nil {
			return satisfies(op, compareFloat(fv, num))
		}
	}

	return satisfies(op, strings.Compare(val, lit))
}

// satisfies maps a three-way comparison result to a comparison operator.
func satisfies(op string, cmp int) bool {
	switch op {
	case opEq:
		return cmp == 0
	case opNe:
		return cmp != 0
	case opGt:
		return cmp > 0
	case opLt:
		return cmp < 0
	case opGe:
		return cmp >= 0
	case opLe:
		return cmp <= 0
	default:
		return false
	}
}

// compareFloat returns -1, 0 or 1 as a is less than, equal to, or greater than b.
func compareFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// compilePartitionFilter parses a Glue partition-filter expression into a
// predicate. A malformed or unsupported expression yields an error.
func compilePartitionFilter(expr string) (partPredicate, error) {
	toks, err := tokenizeFilter(expr)
	if err != nil {
		return nil, err
	}

	p := &filterParser{toks: toks}

	pred, err := p.parseOr()
	if err != nil {
		return nil, err
	}

	if p.peek().kind != tkEOF {
		return nil, filterErrf("unexpected trailing tokens in expression")
	}

	return pred, nil
}
