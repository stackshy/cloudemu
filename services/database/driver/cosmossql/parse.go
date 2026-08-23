package cosmossql

import (
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Parse parses a Cosmos SQL query. params resolves @name placeholders (already
// native values). It returns a cerrors.InvalidArgument error on malformed or
// unsupported input.
func Parse(query string, params map[string]any) (*Statement, error) {
	toks, err := lex(query)
	if err != nil {
		return nil, err
	}

	p := &parser{toks: toks, params: params}

	stmt, err := p.parseStatement()
	if err != nil {
		return nil, err
	}

	if p.peek().kind != tEOF {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unexpected trailing token %q", p.peek().text)
	}

	return stmt, nil
}

type parser struct {
	toks   []token
	pos    int
	params map[string]any
	alias  string
}

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) next() token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}

	return t
}

// keyword reports whether the current token is the identifier kw (case-insensitive).
func (p *parser) keyword(kw string) bool {
	t := p.peek()
	return t.kind == tIdent && strings.EqualFold(t.text, kw)
}

// accept consumes the current token when it is the keyword kw.
func (p *parser) accept(kw string) bool {
	if p.keyword(kw) {
		p.next()
		return true
	}

	return false
}

func (p *parser) expectKind(k tokKind, what string) error {
	if p.peek().kind != k {
		return cerrors.Newf(cerrors.InvalidArgument, "expected %s but found %q", what, p.peek().text)
	}

	p.next()

	return nil
}

func (p *parser) parseStatement() (*Statement, error) {
	if !p.accept("SELECT") {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "expected SELECT but found %q", p.peek().text)
	}

	stmt := &Statement{Limit: -1}
	stmt.Distinct = p.accept("DISTINCT")

	if p.accept("TOP") {
		n, err := p.parseIntToken()
		if err != nil {
			return nil, err
		}

		stmt.Top = n
	}

	proj, err := p.parseProjection()
	if err != nil {
		return nil, err
	}

	if aerr := p.parseFrom(); aerr != nil {
		return nil, aerr
	}

	if werr := p.parseTail(stmt); werr != nil {
		return nil, werr
	}

	stmt.Proj = p.stripProjection(proj)

	return stmt, nil
}

// parseTail parses the optional WHERE, ORDER BY and OFFSET/LIMIT clauses.
func (p *parser) parseTail(stmt *Statement) error {
	if p.accept("WHERE") {
		node, err := p.parsePredicate()
		if err != nil {
			return err
		}

		stmt.Where = node
	}

	if p.accept("ORDER") {
		if err := p.parseOrderBy(stmt); err != nil {
			return err
		}
	}

	return p.parseOffsetLimit(stmt)
}

func (p *parser) parseFrom() error {
	if !p.accept("FROM") {
		return cerrors.Newf(cerrors.InvalidArgument, "expected FROM but found %q", p.peek().text)
	}

	if p.peek().kind != tIdent {
		return cerrors.Newf(cerrors.InvalidArgument, "expected a container alias but found %q", p.peek().text)
	}

	p.alias = p.next().text

	return nil
}

func (p *parser) parseOrderBy(stmt *Statement) error {
	if !p.accept("BY") {
		return cerrors.Newf(cerrors.InvalidArgument, "expected BY after ORDER but found %q", p.peek().text)
	}

	for {
		segs, err := p.parseSegments()
		if err != nil {
			return err
		}

		term := OrderTerm{Path: p.strip(segs)}

		if p.accept("DESC") {
			term.Desc = true
		} else {
			p.accept("ASC")
		}

		stmt.OrderBy = append(stmt.OrderBy, term)

		if p.peek().kind != tComma {
			return nil
		}

		p.next()
	}
}

func (p *parser) parseOffsetLimit(stmt *Statement) error {
	if !p.accept("OFFSET") {
		return nil
	}

	off, err := p.parseIntToken()
	if err != nil {
		return err
	}

	stmt.Offset = off

	if !p.accept("LIMIT") {
		return cerrors.New(cerrors.InvalidArgument, "OFFSET must be followed by LIMIT")
	}

	lim, err := p.parseIntToken()
	if err != nil {
		return err
	}

	stmt.Limit = lim

	return nil
}

func (p *parser) parseIntToken() (int, error) {
	if p.peek().kind != tNumber {
		return 0, cerrors.Newf(cerrors.InvalidArgument, "expected a number but found %q", p.peek().text)
	}

	n, err := strconv.Atoi(p.next().text)
	if err != nil {
		return 0, cerrors.Newf(cerrors.InvalidArgument, "invalid integer %q", err)
	}

	return n, nil
}

// parseSegments parses a dotted path (alias.a.b) into its raw name segments.
// Aliases are stripped later once FROM is known.
func (p *parser) parseSegments() ([]string, error) {
	if p.peek().kind != tIdent {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "expected a property path but found %q", p.peek().text)
	}

	segs := []string{p.next().text}

	for p.peek().kind == tDot {
		p.next()

		if p.peek().kind != tIdent {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "expected a property name but found %q", p.peek().text)
		}

		segs = append(segs, p.next().text)
	}

	return segs, nil
}

// strip removes the FROM alias head from a path (alias.a → a). A lone alias
// (the whole document) becomes an empty path.
func (p *parser) strip(segs []string) []string {
	if len(segs) > 0 && segs[0] == p.alias {
		return segs[1:]
	}

	return segs
}
