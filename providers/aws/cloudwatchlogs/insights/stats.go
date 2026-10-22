package insights

import "strings"

// maxStatsCommands is how many stats commands one query may have.
const maxStatsCommands = 10

// statsCmd groups records by its by expressions and replaces them with one
// record per group holding the group values and the aggregated items.
type statsCmd struct {
	aggs  []*aggCall
	items []fieldItem
	by    []fieldItem
}

type statsGroup struct {
	vals []any
	recs []Record
}

// apply groups in order of first appearance, so with the default newest
// first order the newest group comes first. No records give no rows.
func (c statsCmd) apply(recs []Record) []Record {
	var order []string

	groups := map[string]*statsGroup{}

	for _, r := range recs {
		vals := make([]any, len(c.by))
		for i, b := range c.by {
			vals[i] = b.x.eval(r)
		}

		key := groupKey(vals)

		g, ok := groups[key]
		if !ok {
			order = append(order, key)
			g = &statsGroup{vals: vals}
			groups[key] = g
		}

		g.recs = append(g.recs, r)
	}

	out := make([]Record, 0, len(order))
	for _, key := range order {
		out = append(out, c.row(groups[key]))
	}

	return out
}

func (c statsCmd) row(g *statsGroup) Record {
	slots := Record{}
	for _, a := range c.aggs {
		slots[a.key] = a.fn(a, g.recs)
	}

	row := Record{}

	for i, b := range c.by {
		if g.vals[i] != nil {
			row[b.name] = g.vals[i]
		}
	}

	for _, it := range c.items {
		if v := it.x.eval(slots); v != nil {
			row[it.name] = v
		}
	}

	return row
}

// groupKey joins group values, telling a missing value from an empty one.
func groupKey(vals []any) string {
	var b strings.Builder

	for _, v := range vals {
		if v == nil {
			b.WriteString("\x01")
		} else {
			b.WriteString("\x02" + toStr(v))
		}

		b.WriteByte(0)
	}

	return b.String()
}

func (statsCmd) marksMatched() {}

// addStats parses `stats agg [as name], ... [by expr [as name], ...]`. Every
// item must use an aggregation; later commands see only its output fields.
func (q *Query) addStats(p *exprParser, head *token) error {
	if q.statsCount == maxStatsCommands {
		return compileErr(head.start, head.end, "a query can have at most 10 stats commands")
	}

	if q.sortOrLimit {
		return compileErr(head.start, head.end, "sort and limit must come after the last stats command")
	}

	var aggs []*aggCall

	p.aggs = &aggs

	var items []fieldItem

	for {
		before := len(aggs)
		start := p.peek()

		it, err := p.parseNamedItem()
		if err != nil {
			return err
		}

		if len(aggs) == before {
			return compileErr(start.start, start.end, "stats needs an aggregation function such as count() or sum()")
		}

		items = append(items, it)

		if !p.comma() {
			break
		}
	}

	p.aggs = nil

	by, err := p.parseBy()
	if err != nil {
		return err
	}

	q.cmds = append(q.cmds, statsCmd{aggs: aggs, items: items, by: by})
	q.statsCount++
	q.setStatsOutput(by, items)

	return p.done()
}

func (p *exprParser) parseBy() ([]fieldItem, error) {
	if !p.atKeyword("by") {
		return nil, nil
	}

	p.pos++

	var by []fieldItem

	for {
		it, err := p.parseNamedItem()
		if err != nil {
			return nil, err
		}

		by = append(by, it)

		if !p.comma() {
			return by, nil
		}
	}
}

// setStatsOutput makes the stats output the only fields later commands can
// reference, and the fields shown by default.
func (q *Query) setStatsOutput(by, items []fieldItem) {
	q.available = map[string]bool{}
	q.shown = nil

	for _, list := range [][]fieldItem{by, items} {
		for _, it := range list {
			q.available[it.name] = true

			if !contains(q.shown, it.name) {
				q.shown = append(q.shown, it.name)
			}
		}
	}
}
