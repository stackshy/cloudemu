package vtl

import (
	"fmt"
	"strconv"
	"strings"
)

// Template is a parsed VTL template.
type Template struct {
	body []node
}

// ParseError reports a template that could not be parsed.
type ParseError struct {
	Pos int
	Msg string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("vtl: parse error at offset %d: %s", e.Pos, e.Msg)
}

// Parse parses src into a Template.
func Parse(src string) (*Template, error) {
	p := &parser{src: src}

	body, term, err := p.parseBlock()
	if err != nil {
		return nil, err
	}

	if term != "" {
		return nil, p.errf("unexpected #%s", term)
	}

	return &Template{body: body}, nil
}

type parser struct {
	src string
	pos int
}

func (p *parser) errf(format string, args ...any) error {
	return &ParseError{Pos: p.pos, Msg: fmt.Sprintf(format, args...)}
}

// Directive names.
const (
	dirSet     = "set"
	dirIf      = "if"
	dirElseIf  = "elseif"
	dirElse    = "else"
	dirEnd     = "end"
	dirForeach = "foreach"
	dirBreak   = "break"
	dirStop    = "stop"
	dirReturn  = "return"
)

// knownDirectives are the directives the subset implements.
var knownDirectives = map[string]bool{ //nolint:gochecknoglobals // read-only lookup table
	dirSet: true, dirIf: true, dirElseIf: true, dirElse: true, dirEnd: true,
	dirForeach: true, dirBreak: true, dirStop: true, dirReturn: true,
}

// Directives the subset rejects outright.
var unsupportedDirectives = map[string]bool{ //nolint:gochecknoglobals // read-only lookup table
	"macro": true, "define": true, "parse": true, "include": true, "evaluate": true,
}

// parseBlock parses nodes until EOF or a block terminator (#else, #elseif,
// #end), which it returns. For #elseif the parser is left at its condition.
func (p *parser) parseBlock() ([]node, string, error) {
	var nodes []node

	for p.pos < len(p.src) {
		i := strings.IndexAny(p.src[p.pos:], "$#\\")
		if i < 0 {
			nodes = appendText(nodes, p.src[p.pos:])
			p.pos = len(p.src)

			break
		}

		nodes = appendText(nodes, p.src[p.pos:p.pos+i])
		p.pos += i

		switch p.src[p.pos] {
		case '\\':
			nodes = p.parseEscape(nodes)
		case '$':
			var err error

			nodes, err = p.parseOutputRef(nodes)
			if err != nil {
				return nil, "", err
			}
		default:
			var (
				term string
				err  error
			)

			nodes, term, err = p.parseHash(nodes)
			if err != nil || term != "" {
				return nodes, term, err
			}
		}
	}

	return nodes, "", nil
}

func appendText(nodes []node, s string) []node {
	if s == "" {
		return nodes
	}

	if n := len(nodes); n > 0 {
		if t, ok := nodes[n-1].(*textNode); ok {
			t.text += s

			return nodes
		}
	}

	return append(nodes, &textNode{text: s})
}

// parseEscape handles a backslash: \$ and \# print the next character
// literally; any other backslash is plain text.
func (p *parser) parseEscape(nodes []node) []node {
	if p.pos+1 < len(p.src) && (p.src[p.pos+1] == '$' || p.src[p.pos+1] == '#') {
		nodes = appendText(nodes, p.src[p.pos+1:p.pos+2])
		p.pos += 2

		return nodes
	}

	p.pos++

	return appendText(nodes, `\`)
}

// parseOutputRef parses a reference in text. A '$' that does not start a
// reference is plain text.
func (p *parser) parseOutputRef(nodes []node) ([]node, error) {
	start := p.pos

	ref, quiet, ok, err := p.parseRef()
	if err != nil {
		return nil, err
	}

	if !ok {
		p.pos = start + 1

		return appendText(nodes, "$"), nil
	}

	return append(nodes, &refNode{ref: ref, quiet: quiet}), nil
}

// parseHash handles '#': comments, unparsed blocks and directives. A '#' that
// starts none of them is plain text.
func (p *parser) parseHash(nodes []node) ([]node, string, error) {
	start := p.pos
	rest := p.src[p.pos:]

	switch {
	case strings.HasPrefix(rest, "##"):
		end := strings.IndexByte(rest, '\n')
		if end < 0 {
			p.pos = len(p.src)
		} else {
			p.pos += end + 1
		}

		return nodes, "", nil
	case strings.HasPrefix(rest, "#*"):
		end := strings.Index(rest[2:], "*#")
		if end < 0 {
			return nil, "", p.errf("unterminated #* comment")
		}

		p.pos += end + 4

		return p.gobble(nodes, start), "", nil
	case strings.HasPrefix(rest, "#[["):
		end := strings.Index(rest, "]]#")
		if end < 0 {
			return nil, "", p.errf("unterminated #[[ block")
		}

		p.pos += end + 3

		return appendText(nodes, rest[3:end]), "", nil
	}

	name, braced := p.directiveName()
	if unsupportedDirectives[name] {
		return nil, "", p.errf("#%s is not supported", name)
	}

	return p.parseDirective(nodes, start, name, braced)
}

// directiveName reads the identifier after '#' (optionally #{name}) without
// consuming it.
func (p *parser) directiveName() (string, bool) {
	i := p.pos + 1
	braced := i < len(p.src) && p.src[i] == '{'

	if braced {
		i++
	}

	j := i
	for j < len(p.src) && isIdentChar(p.src[j]) {
		j++
	}

	return p.src[i:j], braced
}

// consumeDirectiveName advances past #name or #{name}.
func (p *parser) consumeDirectiveName(name string, braced bool) bool {
	p.pos++

	if braced {
		p.pos++
	}

	p.pos += len(name)

	if braced {
		if p.pos >= len(p.src) || p.src[p.pos] != '}' {
			return false
		}

		p.pos++
	}

	return true
}

func (p *parser) parseDirective(nodes []node, start int, name string, braced bool) ([]node, string, error) {
	if !knownDirectives[name] {
		p.pos++

		return appendText(nodes, "#"), "", nil
	}

	if !p.consumeDirectiveName(name, braced) {
		return nil, "", p.errf("malformed #{%s}", name)
	}

	switch name {
	case dirElse, dirEnd:
		return p.gobble(nodes, start), name, nil
	case dirElseIf:
		// The caller parses the condition; gobbling waits until it has.
		return nodes, name, nil
	case dirIf:
		return p.parseIf(nodes, start)
	case dirForeach:
		return p.parseForeach(nodes, start)
	case dirReturn:
		return p.parseReturn(nodes, start)
	}

	n, err := p.parseSimpleDirective(name)
	if err != nil {
		return nil, "", err
	}

	return append(p.gobble(nodes, start), n), "", nil
}

// parseSimpleDirective parses the directives that produce a single node.
func (p *parser) parseSimpleDirective(name string) (node, error) {
	switch name {
	case dirBreak:
		return &breakNode{}, nil
	case dirStop:
		return &stopNode{}, nil
	default:
		return p.parseSet()
	}
}

// gobble drops the whitespace-only line around a directive that sits alone on
// its line: the indentation before it (already emitted as text) and the
// trailing whitespace and newline after it.
func (p *parser) gobble(nodes []node, start int) []node {
	end, ok := p.blankLineEnd(start)
	if !ok {
		return nodes
	}

	p.pos = end

	if n := len(nodes); n > 0 {
		if t, ok := nodes[n-1].(*textNode); ok {
			t.text = strings.TrimRight(t.text, " \t")
		}
	}

	return nodes
}

// blankLineEnd reports whether the directive spanning start..p.pos is alone on
// its line, and returns the offset just past that line's newline.
func (p *parser) blankLineEnd(start int) (int, bool) {
	lineStart := strings.LastIndexByte(p.src[:start], '\n') + 1
	if strings.TrimLeft(p.src[lineStart:start], " \t") != "" {
		return 0, false
	}

	rest := p.src[p.pos:]
	nl := strings.IndexByte(rest, '\n')

	if nl < 0 {
		return len(p.src), strings.TrimLeft(rest, " \t\r") == ""
	}

	return p.pos + nl + 1, strings.TrimLeft(rest[:nl], " \t\r") == ""
}

func (p *parser) parseSet() (node, error) {
	if err := p.expectOpenParen(); err != nil {
		return nil, err
	}

	p.skipSpace()

	target, _, ok, err := p.parseRef()
	if err != nil {
		return nil, err
	}

	if !ok {
		return nil, p.errf("#set needs a reference")
	}

	p.skipSpace()

	if !p.consume("=") {
		return nil, p.errf("#set needs '='")
	}

	value, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	if err := p.expectCloseParen(); err != nil {
		return nil, err
	}

	return &setNode{target: target, value: value}, nil
}

func (p *parser) parseCondition() (expr, error) {
	if err := p.expectOpenParen(); err != nil {
		return nil, err
	}

	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	if err := p.expectCloseParen(); err != nil {
		return nil, err
	}

	return cond, nil
}

func (p *parser) parseIf(nodes []node, start int) ([]node, string, error) {
	cond, err := p.parseCondition()
	if err != nil {
		return nil, "", err
	}

	nodes = p.gobble(nodes, start)
	n := &ifNode{}

	for {
		body, term, berr := p.parseBlock()
		if berr != nil {
			return nil, "", berr
		}

		n.branches = append(n.branches, ifBranch{cond: cond, body: body})

		switch term {
		case dirElseIf:
			elseStart := strings.LastIndex(p.src[:p.pos], "#")

			if cond, err = p.parseCondition(); err != nil {
				return nil, "", err
			}

			p.gobbleTrailing(&n.branches[len(n.branches)-1].body, elseStart)

			continue
		case dirElse:
			if n.elseBody, err = p.parseElse(); err != nil {
				return nil, "", err
			}
		case dirEnd:
		default:
			return nil, "", p.errf("#if without #end")
		}

		return append(nodes, n), "", nil
	}
}

// parseElse parses an #else body up to its #end. The result is never nil, so
// the evaluator can tell an empty #else from none.
func (p *parser) parseElse() ([]node, error) {
	body, term, err := p.parseBlock()
	if err != nil {
		return nil, err
	}

	if term != dirEnd {
		return nil, p.errf("#else without #end")
	}

	if body == nil {
		body = []node{}
	}

	return body, nil
}

// gobbleTrailing applies gobble to a body that has already been closed.
func (p *parser) gobbleTrailing(body *[]node, start int) {
	*body = p.gobble(*body, start)
}

func (p *parser) parseForeach(nodes []node, start int) ([]node, string, error) {
	if err := p.expectOpenParen(); err != nil {
		return nil, "", err
	}

	p.skipSpace()

	ref, _, ok, err := p.parseRef()
	if err != nil {
		return nil, "", err
	}

	if !ok || len(ref.chain) > 0 {
		return nil, "", p.errf("#foreach needs a plain loop variable")
	}

	p.skipSpace()

	if !p.consumeWord("in") {
		return nil, "", p.errf("#foreach needs 'in'")
	}

	iter, err := p.parseExpr()
	if err != nil {
		return nil, "", err
	}

	if cerr := p.expectCloseParen(); cerr != nil {
		return nil, "", cerr
	}

	nodes = p.gobble(nodes, start)

	body, term, err := p.parseBlock()
	if err != nil {
		return nil, "", err
	}

	if term != dirEnd {
		return nil, "", p.errf("#foreach without #end")
	}

	return append(nodes, &foreachNode{varName: ref.name, iter: iter, body: body}), "", nil
}

func (p *parser) parseReturn(nodes []node, start int) ([]node, string, error) {
	n := &returnNode{}

	save := p.pos
	p.skipSpace()

	if p.peek() != '(' {
		p.pos = save

		return append(p.gobble(nodes, start), n), "", nil
	}

	p.pos++
	p.skipSpace()

	if p.peek() != ')' {
		v, err := p.parseExpr()
		if err != nil {
			return nil, "", err
		}

		n.value = v
	}

	if err := p.expectCloseParen(); err != nil {
		return nil, "", err
	}

	return append(p.gobble(nodes, start), n), "", nil
}

// parseRef parses $name, $!name, ${name} and $!{name} with their accessor
// chain. ok is false (and the position unchanged) when the text at the cursor
// is not a reference.
func (p *parser) parseRef() (ref *refExpr, quiet, ok bool, err error) {
	start := p.pos
	i := p.pos + 1

	if i < len(p.src) && p.src[i] == '!' {
		quiet = true
		i++
	}

	braced := i < len(p.src) && p.src[i] == '{'
	if braced {
		i++
	}

	if i >= len(p.src) || !isIdentStart(p.src[i]) {
		return nil, false, false, nil
	}

	p.pos = i
	ref = &refExpr{name: p.ident()}

	if err := p.parseChain(ref); err != nil {
		return nil, false, false, err
	}

	if braced {
		if p.peek() != '}' {
			p.pos = start

			return nil, false, false, nil
		}

		p.pos++
	}

	return ref, quiet, true, nil
}

// parseChain reads .prop, .method(args) and [index] steps.
func (p *parser) parseChain(ref *refExpr) error {
	for p.pos < len(p.src) {
		switch c := p.src[p.pos]; {
		case c == '.' && p.pos+1 < len(p.src) && isIdentStart(p.src[p.pos+1]):
			p.pos++
			name := p.ident()

			if p.peek() != '(' {
				ref.chain = append(ref.chain, accessor{kind: accProperty, name: name})

				continue
			}

			p.pos++

			args, err := p.parseArgs(')')
			if err != nil {
				return err
			}

			ref.chain = append(ref.chain, accessor{kind: accMethod, name: name, args: args})
		case c == '[':
			p.pos++

			idx, err := p.parseExpr()
			if err != nil {
				return err
			}

			p.skipSpace()

			if !p.consume("]") {
				return p.errf("expected ']'")
			}

			ref.chain = append(ref.chain, accessor{kind: accIndex, index: idx})
		default:
			return nil
		}
	}

	return nil
}

// parseArgs parses a comma-separated expression list up to closer.
func (p *parser) parseArgs(closer byte) ([]expr, error) {
	var args []expr

	p.skipSpace()

	if p.peek() == closer {
		p.pos++

		return args, nil
	}

	for {
		a, err := p.parseExpr()
		if err != nil {
			return nil, err
		}

		args = append(args, a)

		p.skipSpace()

		switch p.peek() {
		case ',':
			p.pos++
		case closer:
			p.pos++

			return args, nil
		default:
			return nil, p.errf("expected ',' or '%c'", closer)
		}
	}
}

// Expression parsing, lowest precedence first.

func (p *parser) parseExpr() (expr, error) { return p.parseOr() }

func (p *parser) parseOr() (expr, error) {
	return p.parseBinary(p.parseAnd, map[string]string{opOr: opOr, "or": opOr})
}

func (p *parser) parseAnd() (expr, error) {
	return p.parseBinary(p.parseEquality, map[string]string{opAnd: opAnd, "and": opAnd})
}

func (p *parser) parseEquality() (expr, error) {
	return p.parseBinary(p.parseRelational, map[string]string{opEq: opEq, opNe: opNe, "eq": opEq, "ne": opNe})
}

func (p *parser) parseRelational() (expr, error) {
	return p.parseBinary(p.parseAdditive, map[string]string{
		opLe: opLe, opGe: opGe, opLt: opLt, opGt: opGt, "le": opLe, "ge": opGe, "lt": opLt, "gt": opGt,
	})
}

func (p *parser) parseAdditive() (expr, error) {
	return p.parseBinary(p.parseMultiplicative, map[string]string{opAdd: opAdd, opSub: opSub})
}

func (p *parser) parseMultiplicative() (expr, error) {
	return p.parseBinary(p.parseUnary, map[string]string{opMul: opMul, opDiv: opDiv, opMod: opMod})
}

// parseBinary parses a left-associative chain of the operators in ops.
func (p *parser) parseBinary(next func() (expr, error), ops map[string]string) (expr, error) {
	l, err := next()
	if err != nil {
		return nil, err
	}

	for {
		op, ok := p.matchOperator(ops)
		if !ok {
			return l, nil
		}

		r, err := next()
		if err != nil {
			return nil, err
		}

		l = &binaryExpr{op: op, l: l, r: r}
	}
}

// matchOperator consumes the longest operator in ops at the cursor. Word
// operators must not be followed by an identifier character.
func (p *parser) matchOperator(ops map[string]string) (string, bool) {
	p.skipSpace()

	best := ""

	for tok := range ops {
		if !strings.HasPrefix(p.src[p.pos:], tok) || len(tok) <= len(best) {
			continue
		}

		end := p.pos + len(tok)
		if isIdentStart(tok[0]) && end < len(p.src) && isIdentChar(p.src[end]) {
			continue
		}

		best = tok
	}

	if best == "" {
		return "", false
	}

	p.pos += len(best)

	return ops[best], true
}

func (p *parser) parseUnary() (expr, error) {
	p.skipSpace()

	switch {
	case p.peek() == '!' && !strings.HasPrefix(p.src[p.pos:], opNe):
		p.pos++

		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}

		return &unaryExpr{op: opNot, x: x}, nil
	case p.consumeWord("not"):
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}

		return &unaryExpr{op: opNot, x: x}, nil
	case p.peek() == '-' && p.pos+1 < len(p.src) && !isDigit(p.src[p.pos+1]):
		p.pos++

		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}

		return &unaryExpr{op: opSub, x: x}, nil
	}

	return p.parsePrimary()
}

func (p *parser) parsePrimary() (expr, error) {
	p.skipSpace()

	if p.pos >= len(p.src) {
		return nil, p.errf("unexpected end of template in expression")
	}

	switch c := p.src[p.pos]; {
	case c == '$':
		ref, _, ok, err := p.parseRef()
		if err != nil {
			return nil, err
		}

		if !ok {
			return nil, p.errf("invalid reference")
		}

		return ref, nil
	case c == '"':
		return p.parseDoubleQuoted()
	case c == '\'':
		return p.parseSingleQuoted()
	case isDigit(c) || c == '-':
		return p.parseNumber()
	case isIdentStart(c):
		return p.parseKeyword()
	default:
		return p.parseBracketed(c)
	}
}

// parseBracketed parses a list or range, a map literal or a parenthesised
// expression.
func (p *parser) parseBracketed(c byte) (expr, error) {
	switch c {
	case '[':
		p.pos++

		return p.parseListOrRange()
	case '{':
		p.pos++

		return p.parseMap()
	case '(':
		p.pos++

		x, err := p.parseExpr()
		if err != nil {
			return nil, err
		}

		if err := p.expectCloseParen(); err != nil {
			return nil, err
		}

		return x, nil
	default:
		return nil, p.errf("unexpected %q in expression", c)
	}
}

func (p *parser) parseKeyword() (expr, error) {
	start := p.pos

	switch word := p.ident(); word {
	case "true":
		return &literal{value: true}, nil
	case "false":
		return &literal{value: false}, nil
	case "null":
		return &literal{value: nil}, nil
	default:
		p.pos = start

		return nil, p.errf("unexpected %q in expression", word)
	}
}

func (p *parser) parseDoubleQuoted() (expr, error) {
	s, err := p.quoted('"')
	if err != nil {
		return nil, err
	}

	if !strings.ContainsAny(s, "$#") {
		return &literal{value: s}, nil
	}

	sub := &parser{src: s}

	body, term, err := sub.parseBlock()
	if err != nil {
		return nil, err
	}

	if term != "" {
		return nil, p.errf("unexpected #%s in string", term)
	}

	return &interpolated{body: body}, nil
}

func (p *parser) parseSingleQuoted() (expr, error) {
	s, err := p.quoted('\'')
	if err != nil {
		return nil, err
	}

	return &literal{value: s}, nil
}

// quoted reads a string delimited by q; a doubled delimiter is an escaped one.
func (p *parser) quoted(q byte) (string, error) {
	p.pos++

	var b strings.Builder

	for p.pos < len(p.src) {
		c := p.src[p.pos]

		p.pos++

		if c != q {
			b.WriteByte(c)

			continue
		}

		if p.pos < len(p.src) && p.src[p.pos] == q {
			b.WriteByte(q)

			p.pos++

			continue
		}

		return b.String(), nil
	}

	return "", p.errf("unterminated string")
}

func (p *parser) parseNumber() (expr, error) {
	start := p.pos

	if p.peek() == '-' {
		p.pos++
	}

	p.skipDigits()

	isFloat := p.pos+1 < len(p.src) && p.src[p.pos] == '.' && isDigit(p.src[p.pos+1])
	if isFloat {
		p.pos++
		p.skipDigits()
	}

	text := p.src[start:p.pos]

	if isFloat {
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, p.errf("invalid number %q", text)
		}

		return &literal{value: f}, nil
	}

	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return nil, p.errf("invalid number %q", text)
	}

	return &literal{value: n}, nil
}

func (p *parser) skipDigits() {
	for p.pos < len(p.src) && isDigit(p.src[p.pos]) {
		p.pos++
	}
}

func (p *parser) parseListOrRange() (expr, error) {
	p.skipSpace()

	if p.peek() == ']' {
		p.pos++

		return &listExpr{}, nil
	}

	first, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	p.skipSpace()

	if p.consume("..") {
		to, err := p.parseExpr()
		if err != nil {
			return nil, err
		}

		p.skipSpace()

		if !p.consume("]") {
			return nil, p.errf("expected ']' after range")
		}

		return &rangeExpr{from: first, to: to}, nil
	}

	items := []expr{first}

	if !p.consume("]") {
		if !p.consume(",") {
			return nil, p.errf("expected ',' or ']'")
		}

		rest, err := p.parseArgs(']')
		if err != nil {
			return nil, err
		}

		items = append(items, rest...)
	}

	return &listExpr{items: items}, nil
}

func (p *parser) parseMap() (expr, error) {
	m := &mapExpr{}

	p.skipSpace()

	if p.consume("}") {
		return m, nil
	}

	for {
		k, err := p.parseExpr()
		if err != nil {
			return nil, err
		}

		p.skipSpace()

		if !p.consume(":") {
			return nil, p.errf("expected ':' in map literal")
		}

		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}

		m.keys = append(m.keys, k)
		m.vals = append(m.vals, v)

		p.skipSpace()

		switch {
		case p.consume(","):
		case p.consume("}"):
			return m, nil
		default:
			return nil, p.errf("expected ',' or '}' in map literal")
		}
	}
}

func (p *parser) expectOpenParen() error {
	p.skipSpace()

	if !p.consume("(") {
		return p.errf("expected '('")
	}

	return nil
}

func (p *parser) expectCloseParen() error {
	p.skipSpace()

	if !p.consume(")") {
		return p.errf("expected ')'")
	}

	return nil
}

func (p *parser) skipSpace() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *parser) peek() byte {
	if p.pos >= len(p.src) {
		return 0
	}

	return p.src[p.pos]
}

func (p *parser) consume(tok string) bool {
	if strings.HasPrefix(p.src[p.pos:], tok) {
		p.pos += len(tok)

		return true
	}

	return false
}

// consumeWord consumes word when it is not followed by an identifier character.
func (p *parser) consumeWord(word string) bool {
	end := p.pos + len(word)
	if !strings.HasPrefix(p.src[p.pos:], word) || (end < len(p.src) && isIdentChar(p.src[end])) {
		return false
	}

	p.pos = end

	return true
}

func (p *parser) ident() string {
	start := p.pos

	for p.pos < len(p.src) && isIdentChar(p.src[p.pos]) {
		p.pos++
	}

	return p.src[start:p.pos]
}

func isIdentStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// isIdentChar follows Velocity 1.7, which allows '-' inside identifiers.
func isIdentChar(c byte) bool { return isIdentStart(c) || isDigit(c) || c == '-' }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
