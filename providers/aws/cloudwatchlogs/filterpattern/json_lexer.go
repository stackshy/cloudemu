package filterpattern

import (
	"strconv"
	"strings"
)

type tokenKind int

const (
	tokLParen tokenKind = iota
	tokRParen
	tokAnd
	tokOr
	tokOp
	tokSelector
	tokString
	tokRegex
	tokWord
)

type token struct {
	kind tokenKind
	text string
	// steps is set for tokSelector.
	steps []step
}

// stepKind is one hop of a JSON property selector.
type stepKind int

const (
	stepField stepKind = iota
	stepIndex
	stepWildcard
)

type step struct {
	kind  stepKind
	field string
	index int
}

// wordStop ends a bare word inside a JSON pattern.
const wordStop = " \t()&|=!<>"

// twoCharToken is the width of &&, || and the two-character comparisons.
const twoCharToken = 2

// lexJSON splits the body of a JSON pattern (the text between the braces)
// into tokens.
func lexJSON(s string) ([]token, error) {
	var toks []token

	for i := 0; i < len(s); {
		c := s[i]

		if c == ' ' || c == '\t' {
			i++
			continue
		}

		tok, next, err := lexOne(s, i)
		if err != nil {
			return nil, err
		}

		toks = append(toks, tok)
		i = next
	}

	return toks, nil
}

func lexOne(s string, i int) (token, int, error) {
	switch s[i] {
	case '(':
		return token{kind: tokLParen, text: "("}, i + 1, nil
	case ')':
		return token{kind: tokRParen, text: ")"}, i + 1, nil
	case '&', '|':
		return lexLogical(s, i)
	case '=', '!', '<', '>':
		return lexOperator(s, i)
	case '$':
		return lexSelector(s, i)
	case '"':
		return lexQuoted(s, i)
	case '%':
		return lexRegex(s, i)
	}

	end := strings.IndexAny(s[i:], wordStop)
	if end < 0 {
		end = len(s) - i
	}

	return token{kind: tokWord, text: s[i : i+end]}, i + end, nil
}

func lexLogical(s string, i int) (token, int, error) {
	c := s[i]
	if i+1 >= len(s) || s[i+1] != c {
		return token{}, 0, invalid("Invalid filter pattern: expected '" + string([]byte{c, c}) + "'")
	}

	if c == '&' {
		return token{kind: tokAnd, text: "&&"}, i + twoCharToken, nil
	}

	return token{kind: tokOr, text: "||"}, i + twoCharToken, nil
}

func lexRegex(s string, i int) (token, int, error) {
	end := strings.IndexByte(s[i+1:], '%')
	if end < 0 {
		return token{}, 0, invalid("Invalid filter pattern: unterminated regex")
	}

	return token{kind: tokRegex, text: s[i+1 : i+1+end]}, i + end + twoCharToken, nil
}

func lexOperator(s string, i int) (token, int, error) {
	if i+1 < len(s) && s[i+1] == '=' && s[i] != '=' {
		return token{kind: tokOp, text: s[i : i+twoCharToken]}, i + twoCharToken, nil
	}

	if s[i] == '!' {
		return token{}, 0, invalid("Invalid filter pattern: expected '!='")
	}

	return token{kind: tokOp, text: s[i : i+1]}, i + 1, nil
}

func lexQuoted(s string, i int) (token, int, error) {
	var b strings.Builder

	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			if j+1 < len(s) {
				j++
				b.WriteByte(s[j])
			}
		case '"':
			return token{kind: tokString, text: b.String()}, j + 1, nil
		default:
			b.WriteByte(s[j])
		}
	}

	return token{}, 0, invalid("Invalid filter pattern: unterminated quoted string")
}

// lexSelector reads a property selector such as $.a.b[0], $.*, $.list[*] or
// $.['dotted.key'].
func lexSelector(s string, start int) (token, int, error) {
	var steps []step

	i := start + 1

	for i < len(s) {
		var (
			st  step
			err error
		)

		switch {
		case s[i] == '.' && i+1 < len(s) && s[i+1] == '[':
			st, i, err = lexBracket(s, i+1)
		case s[i] == '.':
			st, i, err = lexDotStep(s, i+1)
		case s[i] == '[':
			st, i, err = lexBracket(s, i)
		default:
			return selectorToken(s, start, i, steps)
		}

		if err != nil {
			return token{}, 0, err
		}

		steps = append(steps, st)
	}

	return selectorToken(s, start, i, steps)
}

func selectorToken(s string, start, end int, steps []step) (token, int, error) {
	if len(steps) == 0 {
		return token{}, 0, invalid("Invalid property selector '" + s[start:end] + "'")
	}

	return token{kind: tokSelector, text: s[start:end], steps: steps}, end, nil
}

func lexDotStep(s string, i int) (step, int, error) {
	if i < len(s) && s[i] == '*' {
		return step{kind: stepWildcard}, i + 1, nil
	}

	j := i
	for j < len(s) && isSelectorChar(s[j]) {
		j++
	}

	if j == i {
		return step{}, 0, invalid("Invalid property selector near '" + s[i-1:] + "'")
	}

	return step{kind: stepField, field: s[i:j]}, j, nil
}

func isSelectorChar(c byte) bool {
	return c == '_' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// lexBracket reads [n], [*] or ['key'] starting at the '['.
func lexBracket(s string, i int) (step, int, error) {
	end := strings.IndexByte(s[i:], ']')
	if end < 0 {
		return step{}, 0, invalid("Invalid property selector: missing ']'")
	}

	inner := s[i+1 : i+end]
	next := i + end + 1

	switch {
	case inner == "*":
		return step{kind: stepWildcard}, next, nil
	case len(inner) >= 2 && inner[0] == '\'' && inner[len(inner)-1] == '\'':
		return step{kind: stepField, field: inner[1 : len(inner)-1]}, next, nil
	}

	n, err := strconv.Atoi(inner)
	if err != nil || n < 0 {
		return step{}, 0, invalid("Invalid property selector index [" + inner + "]")
	}

	return step{kind: stepIndex, index: n}, next, nil
}
