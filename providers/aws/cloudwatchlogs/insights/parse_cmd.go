package insights

import (
	"regexp"
	"strconv"
	"strings"
)

const cmdParse = "parse"

// parseCmd extracts fields from a field's text with a regular expression;
// a glob pattern is compiled to one. names[i] names capture group i+1.
type parseCmd struct {
	field string
	re    *regexp.Regexp
	names []string
}

// apply sets the extracted fields on records whose field matches; the rest
// pass through without them.
func (c parseCmd) apply(recs []Record) []Record {
	for _, r := range recs {
		v := r[c.field]
		if v == nil {
			continue
		}

		s := toStr(v)

		m := c.re.FindStringSubmatchIndex(s)
		if m == nil {
			continue
		}

		for i, name := range c.names {
			if lo, hi := m[2*i+2], m[2*i+3]; name != "" && lo >= 0 {
				r[name] = s[lo:hi]
			}
		}
	}

	return recs
}

// addParse parses `parse [field] "glob" as a, b` and `parse [field] /regex/`
// with named groups (or `as` names for its groups). The field defaults to
// @message.
func (q *Query) addParse(p *exprParser) error {
	field, err := p.parseSourceField()
	if err != nil {
		return err
	}

	t := p.peek()
	if t == nil || (t.kind != tString && t.kind != tRegex) {
		if t != nil && t.kind == tIdent {
			if modeErr := unsupportedParseMode(t); modeErr != nil {
				return modeErr
			}
		}

		return p.errHere("glob pattern in quotes or /regex/ expected in parse")
	}

	p.pos++

	cmd, err := p.parsePattern(t)
	if err != nil {
		return err
	}

	cmd.field = field
	q.cmds = append(q.cmds, cmd)
	p.defines = append(p.defines, cmd.names...)
	q.addParsedColumns(cmd.names)

	return p.done()
}

// addParsedColumns shows the extracted fields by default. After a stats
// command they join its output columns.
func (q *Query) addParsedColumns(names []string) {
	cols := &q.parsed
	if q.available != nil {
		cols = &q.shown
	}

	for _, name := range names {
		if name != "" && !contains(*cols, name) {
			*cols = append(*cols, name)
		}
	}
}

// parseSourceField reads parse's optional field, @message by default.
func (p *exprParser) parseSourceField() (string, error) {
	t := p.peek()
	if t == nil || t.kind != tIdent {
		return FieldMessage, nil
	}

	if err := unsupportedParseMode(t); err != nil {
		return "", err
	}

	p.refs = append(p.refs, *t)
	p.pos++

	return t.text, nil
}

// unsupportedParseMode rejects the parse modes the emulator does not run.
func unsupportedParseMode(t *token) error {
	switch strings.ToLower(t.text) {
	case "logfmt", "csv":
		return compileErr(t.start, t.end, "parse "+t.text+" is not supported")
	}

	return nil
}

func (p *exprParser) parsePattern(t *token) (parseCmd, error) {
	glob := t.kind == tString

	src := t.text
	if glob {
		src = globRegex(t.text)
	}

	re, err := regexp.Compile(src)
	if err != nil {
		return parseCmd{}, compileErr(t.start, t.end, "invalid regular expression /"+t.text+"/")
	}

	names := re.SubexpNames()[1:]

	aliases, err := p.parseAliases()
	if err != nil {
		return parseCmd{}, err
	}

	if p.atKeyword("multi") {
		return parseCmd{}, p.errHere("parse multi is not supported")
	}

	switch {
	case aliases != nil && len(aliases) != len(names):
		return parseCmd{}, compileErr(t.start, t.end,
			"parse pattern has "+strconv.Itoa(len(names))+" captures but "+strconv.Itoa(len(aliases))+" names")
	case aliases != nil:
		names = aliases
	case glob || !anyNamed(names):
		return parseCmd{}, compileErr(t.start, t.end, "parse needs named capture groups or as names")
	}

	return parseCmd{re: re, names: names}, nil
}

func (p *exprParser) parseAliases() ([]string, error) {
	if !p.atKeyword(kwAs) {
		return nil, nil
	}

	p.pos++

	var out []string

	for {
		t := p.peek()
		if t == nil || t.kind != tIdent {
			return nil, p.errHere("field name expected after as")
		}

		out = append(out, t.text)
		p.pos++

		if !p.comma() {
			return out, nil
		}
	}
}

func anyNamed(names []string) bool {
	for _, n := range names {
		if n != "" {
			return true
		}
	}

	return false
}

// globRegex compiles a parse glob: each * captures as little text as it can,
// except a trailing * which takes the rest of the value.
func globRegex(glob string) string {
	parts := strings.Split(glob, "*")

	var b strings.Builder

	b.WriteString("(?s)")

	for i, part := range parts {
		b.WriteString(regexp.QuoteMeta(part))

		switch {
		case i == len(parts)-1:
		case i == len(parts)-2 && parts[i+1] == "":
			b.WriteString("(.*)")
		default:
			b.WriteString("(.*?)")
		}
	}

	return b.String()
}
