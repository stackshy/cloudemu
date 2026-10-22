package insights

import (
	"sort"
	"strconv"
	"strings"
)

// System fields every log event carries.
const (
	FieldTimestamp     = "@timestamp"
	FieldMessage       = "@message"
	FieldLogStream     = "@logStream"
	FieldLog           = "@log"
	FieldPtr           = "@ptr"
	FieldIngestionTime = "@ingestionTime"
)

// Field is one field/value pair of a result row.
type Field struct{ Name, Value string }

type command interface {
	apply(recs []Record) []Record
}

type fieldItem struct {
	name string
	x    expr
}

type fieldsCmd struct{ items []fieldItem }

func (c fieldsCmd) apply(recs []Record) []Record {
	for _, r := range recs {
		for _, it := range c.items {
			if v := it.x.eval(r); v != nil {
				r[it.name] = v
			} else {
				delete(r, it.name)
			}
		}
	}

	return recs
}

type filterCmd struct{ x expr }

func (c filterCmd) apply(recs []Record) []Record {
	out := recs[:0]

	for _, r := range recs {
		if truthy(c.x.eval(r)) {
			out = append(out, r)
		}
	}

	return out
}

type sortKey struct {
	field string
	desc  bool
}

type sortCmd struct{ keys []sortKey }

func (c sortCmd) apply(recs []Record) []Record {
	sort.SliceStable(recs, func(i, j int) bool {
		for _, k := range c.keys {
			if d := sortCompare(recs[i][k.field], recs[j][k.field]); d != 0 {
				return (d < 0) != k.desc
			}
		}

		return false
	})

	return recs
}

// sortCompare orders values for sort: non-numbers before numbers, numbers
// numerically, missing values last.
func sortCompare(a, b any) int {
	if a == nil || b == nil {
		return boolOrder(a == nil, b == nil)
	}

	_, an := toNum(a)
	_, bn := toNum(b)

	if an != bn {
		return boolOrder(an, bn)
	}

	return compare(a, b)
}

// boolOrder sorts false before true.
func boolOrder(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	default:
		return -1
	}
}

type limitCmd struct{ n int }

func (c limitCmd) apply(recs []Record) []Record {
	if len(recs) > c.n {
		return recs[:c.n]
	}

	return recs
}

// Query is a parsed Logs Insights query.
type Query struct {
	cmds    []command
	shown   []string
	display []string
}

// Result is the output of running a query.
type Result struct {
	Rows [][]Field
	// Matched counts the records that passed every filter.
	Matched int
}

// Run evaluates the query over records and keeps at most limit rows. Without
// a sort command, records are taken newest first, which is the order the
// console's default query uses.
func (q *Query) Run(recs []Record, limit int) Result {
	recs, matched := q.pipeline(recs)

	if limit > 0 && len(recs) > limit {
		recs = recs[:limit]
	}

	cols := q.columns()
	rows := make([][]Field, 0, len(recs))

	for _, r := range recs {
		row := make([]Field, 0, len(cols)+1)

		for _, c := range cols {
			if v, ok := r[c]; ok && v != nil {
				row = append(row, Field{Name: c, Value: toStr(v)})
			}
		}

		rows = append(rows, append(row, Field{Name: FieldPtr, Value: toStr(r[FieldPtr])}))
	}

	return Result{Rows: rows, Matched: matched}
}

// pipeline applies the commands in order, newest record first unless a sort
// says otherwise. matched counts the records left when the first limit
// command applies (or at the end), which is what recordsMatched reports.
func (q *Query) pipeline(recs []Record) (out []Record, matched int) {
	sortCmd{keys: []sortKey{{field: FieldTimestamp, desc: true}}}.apply(recs)

	matched = -1

	for _, c := range q.cmds {
		if _, isLimit := c.(limitCmd); isLimit && matched < 0 {
			matched = len(recs)
		}

		recs = c.apply(recs)
	}

	if matched < 0 {
		matched = len(recs)
	}

	return recs, matched
}

// columns are the displayed fields: the last display command, else every
// fields command's fields, else @timestamp and @message.
func (q *Query) columns() []string {
	switch {
	case len(q.display) > 0:
		return q.display
	case len(q.shown) > 0:
		return q.shown
	}

	return []string{FieldTimestamp, FieldMessage}
}

// Parse parses a Logs Insights query. A query that does not parse returns a
// *CompileError.
func Parse(query string) (*Query, error) {
	toks, err := lex(query)
	if err != nil {
		return nil, err
	}

	q := &Query{}

	for _, part := range splitPipes(toks) {
		if len(part) == 0 {
			return nil, compileErr(len(query), len(query), "empty command")
		}

		if err := q.addCommand(part, len(query)); err != nil {
			return nil, err
		}
	}

	if len(q.cmds) == 0 && len(q.display) == 0 {
		return nil, compileErr(0, len(query), "query is empty")
	}

	return q, nil
}

func splitPipes(toks []token) [][]token {
	parts := [][]token{{}}

	for _, t := range toks {
		if t.kind == tPipe {
			parts = append(parts, []token{})
			continue
		}

		parts[len(parts)-1] = append(parts[len(parts)-1], t)
	}

	return parts
}

func (q *Query) addCommand(part []token, end int) error {
	head := part[0]
	p := &exprParser{toks: part[1:], end: end}

	switch strings.ToLower(head.text) {
	case "fields":
		return q.addFields(p)
	case cmdFilter, cmdWhere:
		x, err := p.parseOr()
		if err != nil {
			return err
		}

		q.cmds = append(q.cmds, filterCmd{x: x})

		return p.done()
	case "sort":
		return q.addSort(p)
	case "limit":
		return q.addLimit(p)
	case "display":
		return q.addDisplay(p)
	}

	return compileErr(head.start, head.end, "unsupported or unknown command '"+head.text+"'")
}

func (p *exprParser) done() error {
	if t := p.peek(); t != nil {
		return compileErr(t.start, t.end, "unexpected '"+t.text+"'")
	}

	return nil
}

func (q *Query) addFields(p *exprParser) error {
	var items []fieldItem

	for {
		startTok := p.peek()

		x, err := p.parseOr()
		if err != nil {
			return err
		}

		name := exprName(x, startTok, p)

		if p.atKeyword(kwAs) {
			p.pos++

			t := p.peek()
			if t == nil || t.kind != tIdent {
				return p.errHere("field name expected after as")
			}

			name = t.text
			p.pos++
		}

		items = append(items, fieldItem{name: name, x: x})

		if !p.comma() {
			break
		}
	}

	q.cmds = append(q.cmds, fieldsCmd{items: items})

	for _, it := range items {
		if !contains(q.shown, it.name) {
			q.shown = append(q.shown, it.name)
		}
	}

	return p.done()
}

// exprName names an unaliased fields expression: a field keeps its own name,
// anything else is named by its source tokens.
func exprName(x expr, start *token, p *exprParser) string {
	if f, ok := x.(fieldRef); ok {
		return f.name
	}

	var parts []string

	for _, t := range p.toks {
		if start != nil && t.start >= start.start && (p.pos >= len(p.toks) || t.start < p.toks[p.pos].start) {
			parts = append(parts, t.text)
		}
	}

	return strings.Join(parts, "")
}

func (p *exprParser) comma() bool {
	if t := p.peek(); t != nil && t.kind == tComma {
		p.pos++
		return true
	}

	return false
}

func (q *Query) addSort(p *exprParser) error {
	var keys []sortKey

	for {
		t := p.peek()
		if t == nil || t.kind != tIdent {
			return p.errHere("field name expected in sort")
		}

		p.pos++

		k := sortKey{field: t.text, desc: true}

		if p.atKeyword("asc") || p.atKeyword("desc") {
			k.desc = p.atKeyword("desc")
			p.pos++
		}

		keys = append(keys, k)

		if !p.comma() {
			break
		}
	}

	q.cmds = append(q.cmds, sortCmd{keys: keys})

	return p.done()
}

func (q *Query) addLimit(p *exprParser) error {
	t := p.peek()
	if t == nil || t.kind != tNumber {
		return p.errHere("number expected after limit")
	}

	n, err := strconv.Atoi(t.text)
	if err != nil || n <= 0 {
		return compileErr(t.start, t.end, "limit must be a positive integer")
	}

	p.pos++

	q.cmds = append(q.cmds, limitCmd{n: n})

	return p.done()
}

func (q *Query) addDisplay(p *exprParser) error {
	var names []string

	for {
		t := p.peek()
		if t == nil || t.kind != tIdent {
			return p.errHere("field name expected in display")
		}

		names = append(names, t.text)
		p.pos++

		if !p.comma() {
			break
		}
	}

	q.display = names

	return p.done()
}
