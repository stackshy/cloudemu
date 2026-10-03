// Package resourcegraph serves Azure Resource Graph (armresourcegraph) REST
// requests against a *resourcediscovery.Engine.
//
// Resource Graph queries use Kusto Query Language (KQL). This package evaluates
// the subset inventory callers use, against the rendered rows (the same rows a
// client receives), so a filter on any column sees exactly what the row shows:
//
//	Resources                                 (or ResourceContainers; may be omitted)
//	| where resourceGroup =~ 'rg' and (type == 'microsoft.compute/disks' or tags['env'] != 'prod')
//	| where location in~ ('eastus', 'westus') and name !in ('a') and sku.tier contains 'Prem'
//	| project id, name, rg = resourceGroup
//	| order by name asc                       (also: sort by; default desc)
//	| limit 10                                (also: take 10)
//	| count                                   (also: summarize count())
//
// Comparison operators: ==, !=, =~, !~, in, !in, in~, !in~, contains,
// !contains, startswith, endswith, has. Anything else is a 400 InvalidQuery,
// matching real Resource Graph's rejection of a query it cannot run.
package resourcegraph

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// row is one Resource Graph result row.
type row = map[string]any

// kqlQuery is a parsed query: the table it reads and its operators, in order.
type kqlQuery struct {
	table string
	ops   []func([]row) []row
}

const (
	tableResources  = "resources"
	tableContainers = "resourcecontainers"

	kwCount       = "count"
	opNotEq       = "!="
	opNotMatch    = "!~"
	opNotContains = "!contains"
)

// errInvalidQuery marks a query this package cannot run; the handler answers
// it with 400.
var errInvalidQuery = errors.New("invalid query")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errInvalidQuery, fmt.Sprintf(format, args...))
}

// parseKQL parses query, returning an error for syntax or operators it does
// not support.
func parseKQL(query string) (*kqlQuery, error) {
	toks, err := lexKQL(query)
	if err != nil {
		return nil, err
	}

	p := &kqlParser{toks: toks}

	table, err := p.table()
	if err != nil {
		return nil, err
	}

	q := &kqlQuery{table: table}

	for !p.done() {
		op, err := p.operator()
		if err != nil {
			return nil, err
		}

		q.ops = append(q.ops, op)

		if !p.accept("|") && !p.done() {
			return nil, invalid("unexpected '%s'", p.peek().text)
		}
	}

	return q, nil
}

// run applies the query's operators to rows.
func (q *kqlQuery) run(rows []row) []row {
	for _, op := range q.ops {
		rows = op(rows)
	}

	return rows
}

type tokKind int

const (
	tokIdent tokKind = iota
	tokString
	tokNumber
	tokSym
	tokEOF
)

type token struct {
	kind tokKind
	text string
}

// lexKQL splits a query into identifiers (paths such as tags['k'] or sku.tier
// are joined by the parser), quoted strings, numbers and symbols.
func lexKQL(s string) ([]token, error) {
	var toks []token

	for i := 0; i < len(s); {
		if strings.ContainsRune(" \t\n\r", rune(s[i])) {
			i++
			continue
		}

		t, n, err := lexToken(s[i:])
		if err != nil {
			return nil, err
		}

		toks = append(toks, t)
		i += n
	}

	return toks, nil
}

// lexToken reads the token at the start of s and its length in bytes.
func lexToken(s string) (token, int, error) {
	c := s[0]

	switch {
	case c == '\'' || c == '"':
		return lexString(s)
	case isDigit(c):
		n := scanWhile(s, func(b byte) bool { return isDigit(b) || b == '.' })
		return token{tokNumber, s[:n]}, n, nil
	case isIdentByte(c):
		n := scanWhile(s, func(b byte) bool { return isIdentByte(b) || isDigit(b) || b == '-' || b == '~' })
		return token{tokIdent, s[:n]}, n, nil
	}

	n := symLen(s)
	if n == 0 {
		return token{}, 0, invalid("unexpected character '%c'", c)
	}

	return token{tokSym, s[:n]}, n, nil
}

// lexString reads a quoted literal; its length includes both quotes.
func lexString(s string) (token, int, error) {
	j := strings.IndexByte(s[1:], s[0])
	if j < 0 {
		return token{}, 0, invalid("unterminated string literal")
	}

	end := 1 + j + 1

	return token{tokString, s[1 : end-1]}, end, nil
}

func scanWhile(s string, ok func(byte) bool) int {
	n := 0
	for n < len(s) && ok(s[n]) {
		n++
	}

	return n
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// symLen is the length of the longest operator or punctuation symbol that s
// starts with, or 0.
func symLen(s string) int {
	best := 0

	for op := range comparators {
		if len(op) > best && strings.HasPrefix(s, op) {
			best = len(op)
		}
	}

	for _, op := range []string{"!in~", "!in", "=", "|", "(", ")", ",", "[", "]", "."} {
		if len(op) > best && strings.HasPrefix(s, op) {
			best = len(op)
		}
	}

	return best
}

type kqlParser struct {
	toks []token
	pos  int
}

// table reads the leading table name: an identifier standing alone before the
// first '|'. A query that starts with an operator reads Resources.
func (p *kqlParser) table() (string, error) {
	t := p.peek()

	alone := p.pos+1 >= len(p.toks) || p.toks[p.pos+1].text == "|"
	if t.kind != tokIdent || !alone {
		return tableResources, nil
	}

	p.pos++
	p.accept("|")

	name := strings.ToLower(t.text)
	if name != tableResources && name != tableContainers {
		return "", invalid("table '%s' is not supported", t.text)
	}

	return name, nil
}

func (p *kqlParser) done() bool { return p.pos >= len(p.toks) }

func (p *kqlParser) peek() token {
	if p.done() {
		return token{kind: tokEOF}
	}

	return p.toks[p.pos]
}

func (p *kqlParser) next() token {
	t := p.peek()
	p.pos++

	return t
}

// accept consumes the next token when its text matches s (case-insensitively).
func (p *kqlParser) accept(s string) bool {
	if t := p.peek(); t.kind != tokEOF && t.kind != tokString && strings.EqualFold(t.text, s) {
		p.pos++
		return true
	}

	return false
}

func (p *kqlParser) operator() (func([]row) []row, error) {
	t := p.next()

	switch strings.ToLower(t.text) {
	case "where":
		pred, err := p.orExpr()
		if err != nil {
			return nil, err
		}

		return func(rows []row) []row { return filterRows(rows, pred) }, nil
	case "project":
		return p.project()
	case "limit", "take":
		return p.limit(t.text)
	case "order", "sort":
		return p.order()
	case kwCount:
		return countRows("Count"), nil
	case "summarize":
		return p.summarize()
	default:
		return nil, invalid("query operator '%s' is not supported", t.text)
	}
}

func (p *kqlParser) limit(op string) (func([]row) []row, error) {
	n, err := strconv.Atoi(p.next().text)
	if err != nil || n < 0 {
		return nil, invalid("%s needs a row count", op)
	}

	return func(rows []row) []row { return rows[:min(n, len(rows))] }, nil
}

func (p *kqlParser) summarize() (func([]row) []row, error) {
	if !p.accept(kwCount) || !p.accept("(") || !p.accept(")") {
		return nil, invalid("only 'summarize count()' is supported")
	}

	return countRows("count_"), nil
}

// countRows replaces the rows with one row holding their count in column.
func countRows(column string) func([]row) []row {
	return func(rows []row) []row { return []row{{column: len(rows)}} }
}

// path parses a column reference: name, name.sub, name['key'] or a mix.
func (p *kqlParser) path() ([]string, error) {
	t := p.next()
	if t.kind != tokIdent {
		return nil, invalid("expected a column name, got '%s'", t.text)
	}

	segs := []string{t.text}

	for {
		switch {
		case p.accept("."):
			s := p.next()
			if s.kind != tokIdent {
				return nil, invalid("expected a name after '.'")
			}

			segs = append(segs, s.text)
		case p.accept("["):
			s := p.next()
			if s.kind != tokString || !p.accept("]") {
				return nil, invalid("expected ['key']")
			}

			segs = append(segs, s.text)
		default:
			return segs, nil
		}
	}
}

func (p *kqlParser) project() (func([]row) []row, error) {
	type col struct {
		name string
		path []string
	}

	var cols []col

	for {
		path, err := p.path()
		if err != nil {
			return nil, err
		}

		c := col{name: strings.Join(path, "_"), path: path}

		if p.accept("=") {
			if len(path) != 1 {
				return nil, invalid("invalid project alias")
			}

			if c.path, err = p.path(); err != nil {
				return nil, err
			}
		}

		cols = append(cols, c)

		if !p.accept(",") {
			break
		}
	}

	return func(rows []row) []row {
		out := make([]row, 0, len(rows))

		for _, r := range rows {
			pr := make(row, len(cols))
			for _, c := range cols {
				pr[c.name] = lookup(r, c.path)
			}

			out = append(out, pr)
		}

		return out
	}, nil
}

func (p *kqlParser) order() (func([]row) []row, error) {
	if !p.accept("by") {
		return nil, invalid("expected 'by' after order/sort")
	}

	path, err := p.path()
	if err != nil {
		return nil, err
	}

	// KQL sorts descending unless asc is given.
	desc := true
	if p.accept("asc") {
		desc = false
	} else {
		p.accept("desc")
	}

	return func(rows []row) []row {
		out := append([]row(nil), rows...)
		sort.SliceStable(out, func(i, j int) bool {
			a, b := lookup(out[i], path), lookup(out[j], path)
			if desc {
				a, b = b, a
			}

			return lessValue(a, b)
		})

		return out
	}, nil
}

type predicate func(row) bool

func (p *kqlParser) orExpr() (predicate, error) {
	left, err := p.andExpr()
	if err != nil {
		return nil, err
	}

	for p.accept("or") {
		right, err := p.andExpr()
		if err != nil {
			return nil, err
		}

		l := left
		left = func(r row) bool { return l(r) || right(r) }
	}

	return left, nil
}

func (p *kqlParser) andExpr() (predicate, error) {
	left, err := p.term()
	if err != nil {
		return nil, err
	}

	for p.accept("and") {
		right, err := p.term()
		if err != nil {
			return nil, err
		}

		l := left
		left = func(r row) bool { return l(r) && right(r) }
	}

	return left, nil
}

func (p *kqlParser) term() (predicate, error) {
	if p.accept("(") {
		inner, err := p.orExpr()
		if err != nil {
			return nil, err
		}

		if !p.accept(")") {
			return nil, invalid("missing ')'")
		}

		return inner, nil
	}

	path, err := p.path()
	if err != nil {
		return nil, err
	}

	op := strings.ToLower(p.next().text)

	if strings.TrimPrefix(strings.TrimSuffix(op, "~"), "!") == "in" {
		return p.inPredicate(path, op)
	}

	lit := p.next()
	if !isLiteral(lit) {
		return nil, invalid("expected a literal after '%s'", op)
	}

	cmp, ok := comparators[op]
	if !ok {
		return nil, invalid("operator '%s' is not supported", op)
	}

	return func(r row) bool { return cmp(valueString(lookup(r, path)), lit.text) }, nil
}

// inPredicate handles in, !in, in~ and !in~ against a literal list.
func (p *kqlParser) inPredicate(path []string, op string) (predicate, error) {
	list, err := p.literalList()
	if err != nil {
		return nil, err
	}

	fold, negate := strings.HasSuffix(op, "~"), strings.HasPrefix(op, "!")

	return func(r row) bool {
		v := valueString(lookup(r, path))
		for _, item := range list {
			if v == item || fold && strings.EqualFold(v, item) {
				return !negate
			}
		}

		return negate
	}, nil
}

func isLiteral(t token) bool {
	return t.kind == tokString || t.kind == tokNumber || strings.EqualFold(t.text, "true") || strings.EqualFold(t.text, "false")
}

func (p *kqlParser) literalList() ([]string, error) {
	if !p.accept("(") {
		return nil, invalid("expected '(' after in")
	}

	var out []string

	for !p.accept(")") {
		t := p.next()
		if t.kind != tokString && t.kind != tokNumber {
			return nil, invalid("expected a literal in list, got '%s'", t.text)
		}

		out = append(out, t.text)

		if !p.accept(",") && p.peek().text != ")" {
			return nil, invalid("expected ',' or ')' in list")
		}
	}

	return out, nil
}

var comparators = map[string]func(v, lit string) bool{ //nolint:gochecknoglobals // static operator table
	"==":          func(v, l string) bool { return v == l },
	opNotEq:       func(v, l string) bool { return v != l },
	"=~":          strings.EqualFold,
	opNotMatch:    func(v, l string) bool { return !strings.EqualFold(v, l) },
	"contains":    func(v, l string) bool { return strings.Contains(strings.ToLower(v), strings.ToLower(l)) },
	opNotContains: func(v, l string) bool { return !strings.Contains(strings.ToLower(v), strings.ToLower(l)) },
	"has":         func(v, l string) bool { return strings.Contains(strings.ToLower(v), strings.ToLower(l)) },
	"startswith":  func(v, l string) bool { return strings.HasPrefix(strings.ToLower(v), strings.ToLower(l)) },
	"endswith":    func(v, l string) bool { return strings.HasSuffix(strings.ToLower(v), strings.ToLower(l)) },
}

func filterRows(rows []row, pred predicate) []row {
	var out []row

	for _, r := range rows {
		if pred(r) {
			out = append(out, r)
		}
	}

	return out
}

// lookup resolves a column path against a row. The first segment matches the
// column name case-insensitively; later segments index nested objects, tags
// included.
func lookup(r row, path []string) any {
	var cur any = r

	for _, seg := range path {
		switch m := cur.(type) {
		case map[string]any:
			cur = getFold(m, seg)
		case map[string]string:
			v, ok := m[seg]
			if !ok {
				return nil
			}

			cur = v
		default:
			return nil
		}
	}

	return cur
}

func getFold(m map[string]any, key string) any {
	if v, ok := m[key]; ok {
		return v
	}

	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v
		}
	}

	return nil
}

func valueString(v any) string {
	if v == nil {
		return ""
	}

	if s, ok := v.(string); ok {
		return s
	}

	return fmt.Sprint(v)
}

// lessValue orders two values numerically when both are numbers, otherwise as
// case-insensitive strings.
func lessValue(a, b any) bool {
	as, bs := valueString(a), valueString(b)

	af, aErr := strconv.ParseFloat(as, 64)
	bf, bErr := strconv.ParseFloat(bs, 64)

	if aErr == nil && bErr == nil {
		return af < bf
	}

	return strings.ToLower(as) < strings.ToLower(bs)
}
