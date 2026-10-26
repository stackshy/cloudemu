// Package insights implements the core of the CloudWatch Logs Insights query
// language: the fields, filter (where), stats, parse, sort, limit, dedup and
// display commands, the expression grammar they share, and the system and
// auto-discovered JSON fields of a log event. See
// https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/CWL_QuerySyntax.html.
package insights

import (
	"strings"
	"unicode"
)

type tokKind int

const (
	tIdent tokKind = iota
	tNumber
	tString
	tRegex
	tOp
	tComma
	tLParen
	tRParen
	tLBracket
	tRBracket
	tPipe
)

type token struct {
	kind       tokKind
	text       string
	start, end int
}

// Keywords and operators of the query language.
const (
	kwAnd     = "and"
	kwOr      = "or"
	kwNot     = "not"
	kwLike    = "like"
	kwIn      = "in"
	kwAs      = "as"
	cmdFilter = "filter"
	cmdWhere  = "where"
	cmdSort   = "sort"
	cmdLimit  = "limit"

	opEq       = "="
	opNe       = "!="
	opLt       = "<"
	opLe       = "<="
	opGt       = ">"
	opGe       = ">="
	opMatch    = "=~"
	opNotMatch = "!~"
)

// keywords that may precede a /regex/ literal; after any other identifier a
// slash is division.
//
//nolint:gochecknoglobals // fixed keyword set.
var regexKeywords = map[string]bool{kwLike: true, kwAnd: true, kwOr: true, kwNot: true, cmdFilter: true, cmdWhere: true}

// twoCharOps are the operators spelled with two characters.
//
//nolint:gochecknoglobals // fixed operator set.
var twoCharOps = map[string]bool{opNe: true, opLe: true, opGe: true, opMatch: true, opNotMatch: true}

const singleCharOps = "=<>+-*/%^"

// twoChars is the width of a two-character operator and of the pair of
// delimiters around a quoted name.
const twoChars = 2

// lex splits a query into tokens. # starts a comment that runs to the end of
// the line.
func lex(q string) ([]token, error) {
	var toks []token

	for i := 0; i < len(q); {
		switch q[i] {
		case ' ', '\t', '\n', '\r':
			i++
			continue
		case '#':
			for i < len(q) && q[i] != '\n' {
				i++
			}

			continue
		}

		t, next, err := lexToken(q, i, toks)
		if err != nil {
			return nil, err
		}

		toks = append(toks, t)
		i = next
	}

	return toks, nil
}

func lexToken(q string, i int, prev []token) (token, int, error) {
	c := q[i]

	if t, ok := punct(c, i); ok {
		return t, i + 1, nil
	}

	switch {
	case c == '"' || c == '\'':
		return lexString(q, i)
	case c == '`':
		return lexBacktick(q, i)
	case c == '/' && regexAllowed(prev):
		return lexRegex(q, i)
	case c >= '0' && c <= '9':
		return lexNumber(q, i), endOfNumber(q, i), nil
	case isIdentStart(c):
		return lexIdent(q, i)
	}

	return lexOperator(q, i)
}

func lexIdent(q string, i int) (token, int, error) {
	end := i
	for end < len(q) && isIdentChar(q[end]) {
		end++
	}

	return token{kind: tIdent, text: q[i:end], start: i, end: end}, end, nil
}

func lexOperator(q string, i int) (token, int, error) {
	if i+1 < len(q) && twoCharOps[q[i:i+twoChars]] {
		return token{kind: tOp, text: q[i : i+twoChars], start: i, end: i + twoChars}, i + twoChars, nil
	}

	if strings.IndexByte(singleCharOps, q[i]) >= 0 {
		return token{kind: tOp, text: q[i : i+1], start: i, end: i + 1}, i + 1, nil
	}

	return token{}, 0, compileErr(i, i+1, "unexpected character '"+string(q[i])+"'")
}

func punct(c byte, i int) (token, bool) {
	kinds := map[byte]tokKind{',': tComma, '(': tLParen, ')': tRParen, '[': tLBracket, ']': tRBracket, '|': tPipe}

	k, ok := kinds[c]

	return token{kind: k, text: string(c), start: i, end: i + 1}, ok
}

// regexAllowed reports whether a slash here starts a /regex/ literal rather
// than a division.
func regexAllowed(prev []token) bool {
	if len(prev) == 0 {
		return true
	}

	p := prev[len(prev)-1]

	switch p.kind {
	case tIdent:
		return regexKeywords[strings.ToLower(p.text)] || parseOperand(prev)
	case tOp, tComma, tLParen, tLBracket, tPipe:
		return true
	case tNumber, tString, tRegex, tRParen, tRBracket:
		return false
	}

	return false
}

// parseOperand reports whether the next token is parse's pattern: it comes
// right after the parse command name or after the field that follows it.
func parseOperand(prev []token) bool {
	last := len(prev) - 1

	return commandHead(prev, last, cmdParse) || commandHead(prev, last-1, cmdParse)
}

// commandHead reports whether prev[i] is the named command at the start of
// a command.
func commandHead(prev []token, i int, name string) bool {
	if i < 0 || prev[i].kind != tIdent || !strings.EqualFold(prev[i].text, name) {
		return false
	}

	return i == 0 || prev[i-1].kind == tPipe
}

func lexString(q string, i int) (token, int, error) {
	quote := q[i]

	var b strings.Builder

	for j := i + 1; j < len(q); j++ {
		switch q[j] {
		case '\\':
			if j+1 < len(q) {
				j++
				b.WriteByte(q[j])
			}
		case quote:
			return token{kind: tString, text: b.String(), start: i, end: j + 1}, j + 1, nil
		default:
			b.WriteByte(q[j])
		}
	}

	return token{}, 0, compileErr(i, len(q), "unterminated string")
}

func lexBacktick(q string, i int) (token, int, error) {
	end := strings.IndexByte(q[i+1:], '`')
	if end < 0 {
		return token{}, 0, compileErr(i, len(q), "unterminated backtick field name")
	}

	return token{kind: tIdent, text: q[i+1 : i+1+end], start: i, end: i + end + twoChars}, i + end + twoChars, nil
}

func lexRegex(q string, i int) (token, int, error) {
	for j := i + 1; j < len(q); j++ {
		if q[j] == '\\' {
			j++
			continue
		}

		if q[j] == '/' {
			return token{kind: tRegex, text: q[i+1 : j], start: i, end: j + 1}, j + 1, nil
		}
	}

	return token{}, 0, compileErr(i, len(q), "unterminated regular expression")
}

func lexNumber(q string, i int) token {
	end := endOfNumber(q, i)

	return token{kind: tNumber, text: q[i:end], start: i, end: end}
}

func endOfNumber(q string, i int) int {
	end := i
	for end < len(q) && (q[end] >= '0' && q[end] <= '9' || q[end] == '.') {
		end++
	}

	// A duration such as 5m or 30s lexes as one token.
	for end < len(q) && unicode.IsLetter(rune(q[end])) {
		end++
	}

	return end
}

func isIdentStart(c byte) bool {
	return c == '@' || c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || c == '.' || (c >= '0' && c <= '9')
}
