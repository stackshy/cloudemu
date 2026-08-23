package expr

import (
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

type tokenKind int

const (
	tEOF tokenKind = iota
	tIdent
	tAlias
	tValue
	tNumber
	tOp
	tAnd
	tOr
	tNot
	tIn
	tBetween
	tFunc
	tLParen
	tRParen
	tComma
	tDot
	tLBracket
	tRBracket
)

// twoByteOp is the length of a two-character relational operator (<= <> >=).
const twoByteOp = 2

type token struct {
	kind tokenKind
	text string
}

//nolint:gochecknoglobals // static keyword lookup table.
var keywords = map[string]tokenKind{
	"and":     tAnd,
	"or":      tOr,
	"not":     tNot,
	"in":      tIn,
	"between": tBetween,
}

//nolint:gochecknoglobals // static set of recognized DynamoDB functions.
var functions = map[string]bool{
	fnAttrExists:           true,
	"attribute_not_exists": true,
	"attribute_type":       true,
	"begins_with":          true,
	"contains":             true,
	fnSize:                 true,
}

// lex tokenizes a DynamoDB condition/filter expression. Keywords and
// function names are case-insensitive; attribute names are case-sensitive.
func lex(s string) ([]token, error) {
	toks := []token{}
	i := 0

	for i < len(s) {
		if isSpace(s[i]) {
			i++

			continue
		}

		tok, next, err := lexToken(s, i)
		if err != nil {
			return nil, err
		}

		toks = append(toks, tok)
		i = next
	}

	return append(toks, token{kind: tEOF}), nil
}

// lexToken scans the single token starting at s[i], which is known not to be
// whitespace.
func lexToken(s string, i int) (tok token, next int, err error) {
	c := s[i]

	switch {
	case isWordStart(c):
		tok, next = lexWord(s, i)
		return tok, next, nil
	case c == '#' || c == ':':
		return lexRef(s, i)
	case isDigit(c):
		tok, next = lexNumber(s, i)
		return tok, next, nil
	default:
		return lexSymbol(s, i)
	}
}

func lexWord(s string, i int) (tok token, next int) {
	start := i

	for i < len(s) && isWordChar(s[i]) {
		i++
	}

	word := s[start:i]
	lower := strings.ToLower(word)

	if k, ok := keywords[lower]; ok {
		return token{kind: k, text: lower}, i
	}

	if functions[lower] {
		return token{kind: tFunc, text: lower}, i
	}

	return token{kind: tIdent, text: word}, i
}

func lexRef(s string, i int) (tok token, next int, err error) {
	prefix := s[i]
	start := i
	i++

	for i < len(s) && isWordChar(s[i]) {
		i++
	}

	if i == start+1 {
		return token{}, 0, cerrors.Newf(cerrors.InvalidArgument, "empty %c reference at position %d", prefix, start)
	}

	kind := tAlias
	if prefix == ':' {
		kind = tValue
	}

	return token{kind: kind, text: s[start:i]}, i, nil
}

func lexNumber(s string, i int) (tok token, next int) {
	start := i

	for i < len(s) && isDigit(s[i]) {
		i++
	}

	return token{kind: tNumber, text: s[start:i]}, i
}

func lexSymbol(s string, i int) (tok token, next int, err error) {
	if kind, ok := punctToken(s[i]); ok {
		return token{kind: kind}, i + 1, nil
	}

	switch s[i] {
	case '=', '+', '-':
		return token{kind: tOp, text: string(s[i])}, i + 1, nil
	case '<', '>':
		return lexRelational(s, i)
	}

	return token{}, 0, cerrors.Newf(cerrors.InvalidArgument, "unexpected character %q at position %d", string(s[i]), i)
}

// punctToken maps a single-character punctuation byte to its token kind.
func punctToken(c byte) (tokenKind, bool) {
	switch c {
	case '(':
		return tLParen, true
	case ')':
		return tRParen, true
	case ',':
		return tComma, true
	case '.':
		return tDot, true
	case '[':
		return tLBracket, true
	case ']':
		return tRBracket, true
	}

	return tEOF, false
}

func lexRelational(s string, i int) (tok token, next int, err error) {
	c := s[i]

	if i+1 < len(s) {
		switch {
		case c == '<' && s[i+1] == '=':
			return token{kind: tOp, text: "<="}, i + twoByteOp, nil
		case c == '<' && s[i+1] == '>':
			return token{kind: tOp, text: "<>"}, i + twoByteOp, nil
		case c == '>' && s[i+1] == '=':
			return token{kind: tOp, text: ">="}, i + twoByteOp, nil
		}
	}

	return token{kind: tOp, text: string(c)}, i + 1, nil
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func isWordStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isWordChar(c byte) bool {
	return isWordStart(c) || isDigit(c)
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}
