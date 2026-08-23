package cosmossql

import (
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tString
	tNumber
	tParam
	tStar
	tOp
	tLParen
	tRParen
	tComma
	tDot
	tLBracket
	tRBracket
)

type token struct {
	kind tokKind
	text string
}

// lex tokenizes a Cosmos SQL query. Keywords and function names stay as tIdent
// (the parser matches them case-insensitively); string literals are unquoted.
func lex(s string) ([]token, error) {
	toks := []token{}
	i := 0

	for i < len(s) {
		if isSpace(s[i]) {
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

	return append(toks, token{kind: tEOF}), nil
}

func lexOne(s string, i int) (tok token, next int, err error) {
	c := s[i]

	switch {
	case isWordStart(c):
		tok, next = lexWord(s, i)
		return tok, next, nil
	case c == '@':
		return lexParam(s, i)
	case c == '\'' || c == '"':
		return lexString(s, i)
	case isDigit(c) || (c == '-' && i+1 < len(s) && isDigit(s[i+1])):
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

	return token{kind: tIdent, text: s[start:i]}, i
}

func lexParam(s string, i int) (tok token, next int, err error) {
	start := i
	i++ // consume '@'

	for i < len(s) && isWordChar(s[i]) {
		i++
	}

	if i == start+1 {
		return token{}, 0, cerrors.Newf(cerrors.InvalidArgument, "empty parameter at position %d", start)
	}

	return token{kind: tParam, text: s[start:i]}, i, nil
}

func lexString(s string, i int) (tok token, next int, err error) {
	quote := s[i]
	i++ // opening quote

	var b strings.Builder

	for i < len(s) {
		c := s[i]

		switch {
		case c == '\\' && i+1 < len(s):
			b.WriteByte(s[i+1])
			i += 2
		case c == quote:
			return token{kind: tString, text: b.String()}, i + 1, nil
		default:
			b.WriteByte(c)
			i++
		}
	}

	return token{}, 0, cerrors.New(cerrors.InvalidArgument, "unterminated string literal")
}

func lexNumber(s string, i int) (tok token, next int) {
	start := i

	if s[i] == '-' {
		i++
	}

	for i < len(s) && (isDigit(s[i]) || s[i] == '.') {
		i++
	}

	return token{kind: tNumber, text: s[start:i]}, i
}

func lexSymbol(s string, i int) (tok token, next int, err error) {
	switch s[i] {
	case '*':
		return token{kind: tStar, text: "*"}, i + 1, nil
	case '(':
		return token{kind: tLParen}, i + 1, nil
	case ')':
		return token{kind: tRParen}, i + 1, nil
	case ',':
		return token{kind: tComma}, i + 1, nil
	case '.':
		return token{kind: tDot}, i + 1, nil
	case '[':
		return token{kind: tLBracket}, i + 1, nil
	case ']':
		return token{kind: tRBracket}, i + 1, nil
	case '=':
		return token{kind: tOp, text: "="}, i + 1, nil
	case '!', '<', '>':
		return lexRelational(s, i)
	}

	return token{}, 0, cerrors.Newf(cerrors.InvalidArgument, "unexpected character %q at position %d", string(s[i]), i)
}

func lexRelational(s string, i int) (tok token, next int, err error) {
	const twoByte = 2

	if i+1 < len(s) {
		switch s[i : i+twoByte] {
		case "!=", "<>", "<=", ">=":
			return token{kind: tOp, text: s[i : i+twoByte]}, i + twoByte, nil
		}
	}

	if s[i] == '!' {
		return token{}, 0, cerrors.Newf(cerrors.InvalidArgument, "unexpected '!' at position %d", i)
	}

	return token{kind: tOp, text: string(s[i])}, i + 1, nil
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func isWordStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isWordChar(c byte) bool { return isWordStart(c) || isDigit(c) }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
