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

	// Parse-time state for the command ordering rules.
	statsCount  int
	sortOrLimit bool
	dedup       bool
	// available is the fields a command may reference after a stats
	// command: that stats command's output and fields defined since. It is
	// nil before the first stats command, when any field may be referenced.
	available map[string]bool
}

// matchPoint is a command that ends the matching part of a query: the
// records reaching it are what recordsMatched counts.
type matchPoint interface{ marksMatched() }

func (limitCmd) marksMatched() {}

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

		// Aggregated stats rows have no @ptr.
		if ptr, ok := r[FieldPtr]; ok {
			row = append(row, Field{Name: FieldPtr, Value: toStr(ptr)})
		}

		rows = append(rows, row)
	}

	return Result{Rows: rows, Matched: matched}
}

// pipeline applies the commands in order, newest record first unless a sort
// says otherwise. matched counts the records left when the first limit,
// stats or dedup command applies (or at the end), which is what
// recordsMatched reports.
func (q *Query) pipeline(recs []Record) (out []Record, matched int) {
	sortCmd{keys: []sortKey{{field: FieldTimestamp, desc: true}}}.apply(recs)

	matched = -1

	for _, c := range q.cmds {
		if _, ends := c.(matchPoint); ends && matched < 0 {
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

		if err := q.addCommand(part, query); err != nil {
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

// commandParsers parse each command's arguments after its name.
//
//nolint:gochecknoglobals // fixed command table.
var commandParsers = map[string]func(q *Query, p *exprParser, head *token) error{
	"fields":  func(q *Query, p *exprParser, _ *token) error { return q.addFields(p) },
	cmdFilter: func(q *Query, p *exprParser, _ *token) error { return q.addFilter(p) },
	cmdWhere:  func(q *Query, p *exprParser, _ *token) error { return q.addFilter(p) },
	cmdSort:   func(q *Query, p *exprParser, _ *token) error { return q.addSort(p) },
	cmdLimit:  func(q *Query, p *exprParser, _ *token) error { return q.addLimit(p) },
	"display": func(q *Query, p *exprParser, _ *token) error { return q.addDisplay(p) },
	"stats":   (*Query).addStats,
	cmdParse:  func(q *Query, p *exprParser, _ *token) error { return q.addParse(p) },
	"dedup":   func(q *Query, p *exprParser, _ *token) error { return q.addDedup(p) },
}

func (q *Query) addCommand(part []token, src string) error {
	head := &part[0]
	name := strings.ToLower(head.text)
	p := &exprParser{toks: part[1:], end: len(src), src: src}

	add, ok := commandParsers[name]
	if !ok {
		return compileErr(head.start, head.end, "unsupported or unknown command '"+head.text+"'")
	}

	if q.dedup && name != cmdLimit {
		return compileErr(head.start, head.end, "only limit can follow dedup")
	}

	if name == cmdSort || name == cmdLimit {
		q.sortOrLimit = true
	}

	// References are checked against the fields available before this
	// command, which a stats command replaces.
	available := q.available

	if err := add(q, p, head); err != nil {
		return err
	}

	if err := checkRefs(p.refs, available); err != nil {
		return err
	}

	if q.available != nil {
		for _, name := range p.defines {
			q.available[name] = true
		}
	}

	return nil
}

// checkRefs rejects references to fields the preceding stats command did
// not define.
func checkRefs(refs []token, available map[string]bool) error {
	if available == nil {
		return nil
	}

	for _, t := range refs {
		if !available[t.text] {
			return compileErr(t.start, t.end, "field '"+t.text+"' is not defined by the preceding stats command")
		}
	}

	return nil
}

func (q *Query) addFilter(p *exprParser) error {
	x, err := p.parseOr()
	if err != nil {
		return err
	}

	q.cmds = append(q.cmds, filterCmd{x: x})

	return p.done()
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
		it, err := p.parseNamedItem()
		if err != nil {
			return err
		}

		items = append(items, it)

		if !p.comma() {
			break
		}
	}

	q.cmds = append(q.cmds, fieldsCmd{items: items})

	for _, it := range items {
		if !contains(q.shown, it.name) {
			q.shown = append(q.shown, it.name)
		}

		p.defines = append(p.defines, it.name)
	}

	return p.done()
}

// parseNamedItem parses `expr [as name]`. Without an alias a field keeps
// its own name and any other expression is named by its source text.
func (p *exprParser) parseNamedItem() (fieldItem, error) {
	startTok := p.peek()

	x, err := p.parseOr()
	if err != nil {
		return fieldItem{}, err
	}

	name := exprName(x, startTok, p)

	if p.atKeyword(kwAs) {
		p.pos++

		t := p.peek()
		if t == nil || t.kind != tIdent {
			return fieldItem{}, p.errHere("field name expected after as")
		}

		name = t.text
		p.pos++
	}

	return fieldItem{name: name, x: x}, nil
}

// exprName names an unaliased expression: a field keeps its own name,
// anything else is named by its source text.
func exprName(x expr, start *token, p *exprParser) string {
	if f, ok := x.(fieldRef); ok {
		return f.name
	}

	if start == nil || p.pos == 0 {
		return ""
	}

	return p.src[start.start:p.toks[p.pos-1].end]
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
		p.refs = append(p.refs, *t)

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
		p.refs = append(p.refs, *t)
		p.pos++

		if !p.comma() {
			break
		}
	}

	q.display = names

	return p.done()
}
