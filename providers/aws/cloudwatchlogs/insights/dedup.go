package insights

// dedupCmd keeps the first record for each combination of its fields;
// records missing any of them are all kept.
type dedupCmd struct{ fields []string }

func (c dedupCmd) apply(recs []Record) []Record {
	seen := map[string]bool{}
	out := recs[:0]

	for _, r := range recs {
		vals := make([]any, len(c.fields))
		complete := true

		for i, f := range c.fields {
			vals[i] = r[f]
			complete = complete && vals[i] != nil
		}

		if complete {
			key := groupKey(vals)
			if seen[key] {
				continue
			}

			seen[key] = true
		}

		out = append(out, r)
	}

	return out
}

func (dedupCmd) marksMatched() {}

func (q *Query) addDedup(p *exprParser) error {
	var names []string

	for {
		t := p.peek()
		if t == nil || t.kind != tIdent {
			return p.errHere("field name expected in dedup")
		}

		names = append(names, t.text)
		p.refs = append(p.refs, *t)
		p.pos++

		if !p.comma() {
			break
		}
	}

	q.cmds = append(q.cmds, dedupCmd{fields: names})
	q.dedup = true

	return p.done()
}
