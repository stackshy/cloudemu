package metricmath

// A recursive-descent parser for the supported metric-math syntax: numbers,
// references to other entry IDs, the arithmetic operators + - * /, unary
// minus, parentheses, the comparison operators == != < <= > >=, the logical
// operators AND (&&) and OR (||), and the functions IF, FILL and SEARCH.
// ANOMALY_DETECTION_BAND(id[, k]) is accepted when it is the whole expression.

import (
	"errors"
	"strconv"
)

// errMathParse marks a malformed expression. Callers treat it as no data.
var errMathParse = errors.New("malformed metric math expression")

// parseExpression parses expr. ok is false when it is not in the supported
// syntax.
func parseExpression(expr string) (n node, ok bool) {
	tokens := tokenizeMath(expr)
	if band, isBand := parseBand(tokens); isBand {
		return band, true
	}

	p := &mathParser{tokens: tokens}

	n, err := p.parse()
	if err != nil || !p.atEnd() || !keywordsPlaced(n) {
		return nil, false
	}

	return n, true
}

// Token kinds that are not a single operator rune.
const (
	tokNumber  = 'n'
	tokIdent   = 'i'
	tokString  = 's'
	tokCompare = 'c'
	tokAnd     = '&'
	tokOr      = '|'
	tokInvalid = '?'
)

// mathToken is one lexical unit of an expression.
type mathToken struct {
	kind  byte // one of the tok kinds, or an operator/paren/comma rune
	num   float64
	ident string // identifier name, string literal body or comparison operator
}

func tokenizeMath(expr string) []mathToken {
	var tokens []mathToken

	for i := 0; i < len(expr); {
		c := expr[i]

		switch {
		case isSpace(c):
			i++
		case isOperatorOrParen(c):
			tokens = append(tokens, mathToken{kind: c})
			i++
		case isComparisonStart(c) || c == '&' || c == '|':
			tok, next := lexSymbol(expr, i)
			tokens = append(tokens, tok)
			i = next
		case c == '\'':
			tok, next := lexString(expr, i)
			tokens = append(tokens, tok)
			i = next
		case isNumberStart(c):
			tok, next := lexNumber(expr, i)
			tokens = append(tokens, tok)
			i = next
		case isIdentStart(c):
			tok, next := lexIdent(expr, i)
			tokens = append(tokens, tok)
			i = next
		default:
			// Unknown character: emit a sentinel the parser rejects.
			tokens = append(tokens, mathToken{kind: tokInvalid})
			i++
		}
	}

	return tokens
}

// Comparison and logical operator spellings.
const (
	opEq  = "=="
	opNe  = "!="
	opLt  = "<"
	opLe  = "<="
	opGt  = ">"
	opGe  = ">="
	opAnd = "&&"
	opOr  = "||"
)

// lexSymbol reads a comparison or logical operator.
func lexSymbol(expr string, start int) (tok mathToken, next int) {
	if start+len(opEq) <= len(expr) {
		two := expr[start : start+len(opEq)]

		switch two {
		case opEq, opNe, opLe, opGe:
			return mathToken{kind: tokCompare, ident: two}, start + len(two)
		case opAnd:
			return mathToken{kind: tokAnd}, start + len(two)
		case opOr:
			return mathToken{kind: tokOr}, start + len(two)
		}
	}

	switch one := expr[start : start+1]; one {
	case opLt, opGt:
		return mathToken{kind: tokCompare, ident: one}, start + 1
	default:
		return mathToken{kind: tokInvalid}, start + 1
	}
}

// lexString reads a single-quoted string. A backslash escapes the next
// character, so a SEARCH term can hold a quote.
func lexString(expr string, start int) (tok mathToken, next int) {
	var body []byte

	for i := start + 1; i < len(expr); i++ {
		switch expr[i] {
		case '\\':
			if i+1 < len(expr) {
				body = append(body, expr[i], expr[i+1])
				i++
			}
		case '\'':
			return mathToken{kind: tokString, ident: string(body)}, i + 1
		default:
			body = append(body, expr[i])
		}
	}

	return mathToken{kind: tokInvalid}, len(expr)
}

func lexNumber(expr string, start int) (tok mathToken, next int) {
	i := start
	for i < len(expr) && isNumberStart(expr[i]) {
		i++
	}

	val, err := strconv.ParseFloat(expr[start:i], 64)
	if err != nil {
		return mathToken{kind: tokInvalid}, i
	}

	return mathToken{kind: tokNumber, num: val}, i
}

func lexIdent(expr string, start int) (tok mathToken, next int) {
	i := start
	for i < len(expr) && isIdentPart(expr[i]) {
		i++
	}

	word := expr[start:i]

	// AND and OR are the logical operators. IDs start with a lowercase
	// letter, so they never clash with an entry.
	switch word {
	case kwAnd:
		return mathToken{kind: tokAnd}, i
	case kwOr:
		return mathToken{kind: tokOr}, i
	}

	return mathToken{kind: tokIdent, ident: word}, i
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
func isOperatorOrParen(c byte) bool {
	return c == '+' || c == '-' || c == '*' || c == '/' || c == '(' || c == ')' || c == ','
}
func isComparisonStart(c byte) bool { return c == '=' || c == '!' || c == '<' || c == '>' }
func isDigit(c byte) bool           { return c >= '0' && c <= '9' }
func isNumberStart(c byte) bool     { return isDigit(c) || c == '.' }
func isIdentStart(c byte) bool      { return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isIdentPart(c byte) bool       { return isIdentStart(c) || isDigit(c) }

// mathParser is a recursive-descent parser over a token slice.
type mathParser struct {
	tokens []mathToken
	pos    int
}

func (p *mathParser) atEnd() bool { return p.pos >= len(p.tokens) }

func (p *mathParser) peek() (mathToken, bool) {
	if p.atEnd() {
		return mathToken{}, false
	}

	return p.tokens[p.pos], true
}

// peekKind reports whether the next token has this kind.
func (p *mathParser) peekKind(kind byte) bool {
	tok, ok := p.peek()

	return ok && tok.kind == kind
}

// expect consumes a token of this kind.
func (p *mathParser) expect(kind byte) error {
	if !p.peekKind(kind) {
		return errMathParse
	}

	p.pos++

	return nil
}

func (p *mathParser) parse() (node, error) {
	if len(p.tokens) == 0 {
		return nil, errMathParse
	}

	return p.parseOr()
}

// parseOr: or := and (OR and)*. OR binds loosest.
func (p *mathParser) parseOr() (node, error) {
	n, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	for p.peekKind(tokOr) {
		p.pos++

		right, rerr := p.parseAnd()
		if rerr != nil {
			return nil, rerr
		}

		n = logicNode{and: false, left: n, right: right}
	}

	return n, nil
}

// parseAnd: and := compare (AND compare)*.
func (p *mathParser) parseAnd() (node, error) {
	n, err := p.parseCompare()
	if err != nil {
		return nil, err
	}

	for p.peekKind(tokAnd) {
		p.pos++

		right, rerr := p.parseCompare()
		if rerr != nil {
			return nil, rerr
		}

		n = logicNode{and: true, left: n, right: right}
	}

	return n, nil
}

// parseCompare: compare := expr (cmp expr)?.
func (p *mathParser) parseCompare() (node, error) {
	n, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	tok, ok := p.peek()
	if !ok || tok.kind != tokCompare {
		return n, nil
	}

	p.pos++

	right, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	return compareNode{op: tok.ident, left: n, right: right}, nil
}

func (p *mathParser) parseExpr() (node, error) {
	n, err := p.parseTerm()
	if err != nil {
		return nil, err
	}

	for {
		tok, ok := p.peek()
		if !ok || (tok.kind != '+' && tok.kind != '-') {
			return n, nil
		}

		p.pos++

		right, rerr := p.parseTerm()
		if rerr != nil {
			return nil, rerr
		}

		n = binaryNode{op: tok.kind, left: n, right: right}
	}
}

func (p *mathParser) parseTerm() (node, error) {
	n, err := p.parseFactor()
	if err != nil {
		return nil, err
	}

	for {
		tok, ok := p.peek()
		if !ok || (tok.kind != '*' && tok.kind != '/') {
			return n, nil
		}

		p.pos++

		right, rerr := p.parseFactor()
		if rerr != nil {
			return nil, rerr
		}

		n = binaryNode{op: tok.kind, left: n, right: right}
	}
}

func (p *mathParser) parseFactor() (node, error) {
	tok, ok := p.peek()
	if !ok {
		return nil, errMathParse
	}

	if tok.kind == '-' {
		p.pos++

		operand, err := p.parseFactor()
		if err != nil {
			return nil, err
		}

		return negNode{operand: operand}, nil
	}

	return p.parsePrimary()
}

func (p *mathParser) parsePrimary() (node, error) {
	tok, ok := p.peek()
	if !ok {
		return nil, errMathParse
	}

	switch tok.kind {
	case tokNumber:
		p.pos++
		return numberNode{val: tok.num}, nil
	case tokIdent:
		p.pos++

		if p.peekKind('(') {
			return p.parseCall(tok.ident)
		}

		if !isLowerStart(tok.ident) {
			// An uppercase word outside a call is a keyword such as REPEAT,
			// which only FILL reads.
			return keywordNode{word: tok.ident}, nil
		}

		return refNode{id: tok.ident}, nil
	case '(':
		p.pos++

		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}

		if err := p.expect(')'); err != nil {
			return nil, err
		}

		return inner, nil
	default:
		return nil, errMathParse
	}
}

// isLowerStart reports whether an identifier can name an entry: IDs start
// with a lowercase letter.
func isLowerStart(id string) bool {
	return id != "" && id[0] >= 'a' && id[0] <= 'z'
}

// parseCall parses the argument list of a function call and builds its node.
func (p *mathParser) parseCall(name string) (node, error) {
	if err := p.expect('('); err != nil {
		return nil, err
	}

	var args []node

	if !p.peekKind(')') {
		for {
			arg, err := p.parseArg()
			if err != nil {
				return nil, err
			}

			args = append(args, arg)

			if !p.peekKind(',') {
				break
			}

			p.pos++
		}
	}

	if err := p.expect(')'); err != nil {
		return nil, err
	}

	return buildCall(name, args)
}

// parseArg parses one call argument: a string literal or an expression.
func (p *mathParser) parseArg() (node, error) {
	if tok, ok := p.peek(); ok && tok.kind == tokString {
		p.pos++
		return stringNode{val: tok.ident}, nil
	}

	return p.parseOr()
}

// Function names and keywords of the supported metric-math functions.
const (
	fnIf      = "IF"
	fnFill    = "FILL"
	fnSearch  = "SEARCH"
	kwRepeat  = "REPEAT"
	kwLinear  = "LINEAR"
	kwAnd     = "AND"
	kwOr      = "OR"
	kwNot     = "NOT"
	ifMinArgs = 2
	ifMaxArgs = 3
	fillArgs  = 2
)

// Argument counts of SEARCH: the term and the statistic, then an optional
// period.
const (
	searchMinArgs = 2
	searchMaxArgs = 3
)

// buildCall checks a call's arguments and returns its node.
func buildCall(name string, args []node) (node, error) {
	switch name {
	case fnIf:
		return buildIf(args)
	case fnFill:
		return buildFill(args)
	case fnSearch:
		return buildSearch(args)
	default:
		return nil, errMathParse
	}
}

// buildIf checks IF(cond, a[, b]).
func buildIf(args []node) (node, error) {
	if len(args) < ifMinArgs || len(args) > ifMaxArgs || hasString(args) {
		return nil, errMathParse
	}

	n := ifNode{cond: args[0], then: args[1]}
	if len(args) == ifMaxArgs {
		n.otherwise = args[2]
	}

	return n, nil
}

// buildFill checks FILL(x, S|TS|REPEAT|LINEAR).
func buildFill(args []node) (node, error) {
	if len(args) != fillArgs || hasString(args) {
		return nil, errMathParse
	}

	if kw, ok := args[1].(keywordNode); ok && kw.word != kwRepeat && kw.word != kwLinear {
		return nil, errMathParse
	}

	return fillNode{input: args[0], with: args[1]}, nil
}

// buildSearch checks SEARCH('term', 'Stat'[, period]).
func buildSearch(args []node) (node, error) {
	if len(args) < searchMinArgs || len(args) > searchMaxArgs {
		return nil, errMathParse
	}

	term, isTerm := args[0].(stringNode)
	stat, isStat := args[1].(stringNode)

	if !isTerm || !isStat || stat.val == "" {
		return nil, errMathParse
	}

	q, ok := parseSearchQuery(term.val)
	if !ok {
		return nil, errMathParse
	}

	n := searchNode{query: q, stat: stat.val}

	if len(args) == searchMaxArgs {
		period, ok := searchPeriod(args[2])
		if !ok {
			return nil, errMathParse
		}

		n.period = period
	}

	return n, nil
}

// searchPeriod reads the period argument of SEARCH: a positive whole number.
func searchPeriod(arg node) (int, bool) {
	num, ok := arg.(numberNode)
	if !ok || num.val <= 0 || num.val != float64(int(num.val)) {
		return 0, false
	}

	return int(num.val), true
}

// hasString reports whether a string literal is used where a value belongs.
func hasString(args []node) bool {
	for _, a := range args {
		if _, ok := a.(stringNode); ok {
			return true
		}
	}

	return false
}

// bandFunction is the metric-math function that returns an anomaly band.
const bandFunction = "ANOMALY_DETECTION_BAND"

// defaultBandWidth is the number of standard deviations when the call
// leaves it out.
const defaultBandWidth = 2

// Token counts of the two band call forms: F ( id ) and F ( id , k ).
const (
	bandCallTokens      = 4
	bandCallTokensWithK = 6
)

// isBandCall reports whether t starts ANOMALY_DETECTION_BAND(id and ends
// with a closing parenthesis.
func isBandCall(t []mathToken) bool {
	return t[0].kind == tokIdent && t[0].ident == bandFunction && t[1].kind == '(' && t[2].kind == tokIdent &&
		t[len(t)-1].kind == ')'
}

// parseBand matches ANOMALY_DETECTION_BAND(id) and ANOMALY_DETECTION_BAND(id, k).
func parseBand(t []mathToken) (bandNode, bool) {
	if len(t) != bandCallTokens && len(t) != bandCallTokensWithK {
		return bandNode{}, false
	}

	if !isBandCall(t) {
		return bandNode{}, false
	}

	n := bandNode{input: t[2].ident, k: defaultBandWidth}

	if len(t) == bandCallTokensWithK {
		if t[3].kind != ',' || t[4].kind != tokNumber {
			return bandNode{}, false
		}

		n.k = t[4].num
	}

	return n, true
}
