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
)

// parseKQL parses query, returning an error for syntax or operators it does
// not support.
func parseKQL(query string) (*kqlQuery, error) {
	toks, err := lexKQL(query)
	if err != nil {
		return nil, err
	}

	p := &kqlParser{toks: toks}
	q := &kqlQuery{table: tableResources}

	if t := p.peek(); t.kind == tokIdent && !isOperatorWord(t.text) {
		q.table = strings.ToLower(p.next().text)
		if q.table != tableResources && q.table != tableContainers {
			return nil, fmt.Errorf("table '%s' is not supported", t.text)
		}

		if !p.accept("|") && !p.done() {
			return nil, fmt.Errorf("expected '|' after table name, got '%s'", p.peek().text)
		}
	}

	for !p.done() {
		op, err := p.operator()
		if err != nil {
			return nil, err
		}

		q.ops = append(q.ops, op)

		if !p.accept("|") && !p.done() {
			return nil, fmt.Errorf("unexpected '%s'", p.peek().text)
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

func isOperatorWord(s string) bool {
	switch strings.ToLower(s) {
	case "where", "project", "limit", "take", "order", "sort", "count", "summarize":
		return true
	default:
		return false
	}
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
		c := s[i]

		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '\'' || c == '"':
			j := strings.IndexByte(s[i+1:], c)
			if j < 0 {
				return nil, fmt.Errorf("unterminated string literal")
			}

			toks = append(toks, token{tokString, s[i+1 : i+1+j]})
			i += j + 2
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
				j++
			}

			toks = append(toks, token{tokNumber, s[i:j]})
			i = j
		case isIdentByte(c):
			j := i
			for j < len(s) && (isIdentByte(s[j]) || s[j] >= '0' && s[j] <= '9' || s[j] == '-' || s[j] == '~') {
				j++
			}

			toks = append(toks, token{tokIdent, s[i:j]})
			i = j
		default:
			n := symLen(s[i:])
			if n == 0 {
				return nil, fmt.Errorf("unexpected character '%c'", c)
			}

			toks = append(toks, token{tokSym, s[i : i+n]})
			i += n
		}
	}

	return toks, nil
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func symLen(s string) int {
	for _, op := range []string{"==", "!=", "=~", "!~", "!in~", "!in", "!contains", "=", "|", "(", ")", ",", "[", "]", "."} {
		if strings.HasPrefix(s, op) {
			return len(op)
		}
	}

	return 0
}

type kqlParser struct {
	toks []token
	pos  int
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
		n, err := strconv.Atoi(p.next().text)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("%s needs a row count", t.text)
		}

		return func(rows []row) []row { return rows[:min(n, len(rows))] }, nil
	case "order", "sort":
		return p.order()
	case "count":
		return func(rows []row) []row { return []row{{"Count": len(rows)}} }, nil
	case "summarize":
		if !p.accept("count") || !p.accept("(") || !p.accept(")") {
			return nil, fmt.Errorf("only 'summarize count()' is supported")
		}

		return func(rows []row) []row { return []row{{"count_": len(rows)}} }, nil
	default:
		return nil, fmt.Errorf("query operator '%s' is not supported", t.text)
	}
}

// path parses a column reference: name, name.sub, name['key'] or a mix.
func (p *kqlParser) path() ([]string, error) {
	t := p.next()
	if t.kind != tokIdent {
		return nil, fmt.Errorf("expected a column name, got '%s'", t.text)
	}

	segs := []string{t.text}

	for {
		switch {
		case p.accept("."):
			s := p.next()
			if s.kind != tokIdent {
				return nil, fmt.Errorf("expected a name after '.'")
			}

			segs = append(segs, s.text)
		case p.accept("["):
			s := p.next()
			if s.kind != tokString || !p.accept("]") {
				return nil, fmt.Errorf("expected ['key']")
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
				return nil, fmt.Errorf("invalid project alias")
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
		return nil, fmt.Errorf("expected 'by' after order/sort")
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
			return nil, fmt.Errorf("missing ')'")
		}

		return inner, nil
	}

	path, err := p.path()
	if err != nil {
		return nil, err
	}

	op := strings.ToLower(p.next().text)

	if op == "in" || op == "in~" || op == "!in" || op == "!in~" {
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

	lit := p.next()
	if lit.kind != tokString && lit.kind != tokNumber && !strings.EqualFold(lit.text, "true") && !strings.EqualFold(lit.text, "false") {
		return nil, fmt.Errorf("expected a literal after '%s'", op)
	}

	cmp, ok := comparators[op]
	if !ok {
		return nil, fmt.Errorf("operator '%s' is not supported", op)
	}

	return func(r row) bool { return cmp(valueString(lookup(r, path)), lit.text) }, nil
}

func (p *kqlParser) literalList() ([]string, error) {
	if !p.accept("(") {
		return nil, fmt.Errorf("expected '(' after in")
	}

	var out []string

	for !p.accept(")") {
		t := p.next()
		if t.kind != tokString && t.kind != tokNumber {
			return nil, fmt.Errorf("expected a literal in list, got '%s'", t.text)
		}

		out = append(out, t.text)

		if !p.accept(",") && p.peek().text != ")" {
			return nil, fmt.Errorf("expected ',' or ')' in list")
		}
	}

	return out, nil
}

var comparators = map[string]func(v, lit string) bool{ //nolint:gochecknoglobals // static operator table
	"==":         func(v, l string) bool { return v == l },
	"!=":         func(v, l string) bool { return v != l },
	"=~":         strings.EqualFold,
	"!~":         func(v, l string) bool { return !strings.EqualFold(v, l) },
	"contains":   func(v, l string) bool { return strings.Contains(strings.ToLower(v), strings.ToLower(l)) },
	"!contains":  func(v, l string) bool { return !strings.Contains(strings.ToLower(v), strings.ToLower(l)) },
	"has":        func(v, l string) bool { return strings.Contains(strings.ToLower(v), strings.ToLower(l)) },
	"startswith": func(v, l string) bool { return strings.HasPrefix(strings.ToLower(v), strings.ToLower(l)) },
	"endswith":   func(v, l string) bool { return strings.HasSuffix(strings.ToLower(v), strings.ToLower(l)) },
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
	var cur any = map[string]any(r)

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
