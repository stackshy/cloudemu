package filterpattern

import (
	"strconv"
	"strings"
)

// ellipsis stands for any number of unnamed fields in a space-delimited
// pattern.
const ellipsis = "..."

// delimField is one comma-separated entry of a [ ... ] pattern: a named or
// unnamed field with an optional condition, or an ellipsis.
type delimField struct {
	name     string
	ellipsis bool
	cond     fieldCond
}

// delimPattern is a parsed space-delimited pattern such as
// [ip, user, ..., status_code=4*, bytes>1000].
type delimPattern struct {
	fields      []delimField
	hasEllipsis bool
	regexCount  int
}

// span is the half-open range of message fields bound to a pattern field.
type span struct{ start, end int }

func parseDelimitedPattern(s string) (*delimPattern, error) {
	if !strings.HasSuffix(s, "]") {
		return nil, invalid("Invalid filter pattern: space-delimited pattern must end with ']'")
	}

	parts, err := splitFields(s[1 : len(s)-1])
	if err != nil {
		return nil, err
	}

	p := &delimPattern{}

	for _, part := range parts {
		f, regexes, ferr := parseDelimField(part)
		if ferr != nil {
			return nil, ferr
		}

		p.regexCount += regexes
		p.hasEllipsis = p.hasEllipsis || f.ellipsis
		p.fields = append(p.fields, f)
	}

	if p.regexCount > maxRegexPerStructuredPattern {
		return nil, invalid("Invalid filter pattern: at most 2 regular expressions are allowed in a space-delimited filter pattern")
	}

	return p, nil
}

// splitFields splits a pattern body on commas that are not inside a %regex%
// or a quoted string. An empty body yields no fields; "[]" matches any event.
func splitFields(body string) ([]string, error) {
	if strings.TrimSpace(body) == "" {
		return nil, nil
	}

	var (
		parts []string
		sc    fieldScanner
		start int
	)

	for i := 0; i < len(body); i++ {
		if sc.step(body[i]) {
			parts = append(parts, body[start:i])
			start = i + 1
		}
	}

	if sc.inRegex || sc.inQuote {
		return nil, invalid("Invalid filter pattern: unterminated regex or quoted string")
	}

	return append(parts, body[start:]), nil
}

// fieldScanner tracks whether the scan is inside a %regex% or a quoted
// string, where a comma does not separate fields.
type fieldScanner struct{ inRegex, inQuote bool }

// step consumes one byte and reports whether it is a field separator.
func (f *fieldScanner) step(c byte) bool {
	switch {
	case c == '"' && !f.inRegex:
		f.inQuote = !f.inQuote
	case c == '%' && !f.inQuote:
		f.inRegex = !f.inRegex
	case c == ',':
		return !f.inRegex && !f.inQuote
	}

	return false
}

func parseDelimField(text string) (delimField, int, error) {
	text = strings.TrimSpace(text)

	switch text {
	case "":
		return delimField{}, 0, nil
	case ellipsis:
		return delimField{ellipsis: true}, 0, nil
	}

	toks, err := lexJSON(text)
	if err != nil {
		return delimField{}, 0, err
	}

	if toks[0].kind != tokWord || !isFieldName(toks[0].text) {
		return delimField{}, 0, invalid("Invalid field '" + text + "' in space-delimited filter pattern")
	}

	f := delimField{name: toks[0].text}
	if len(toks) == 1 {
		return f, 0, nil
	}

	cp := &condParser{toks: toks, name: f.name}

	cond, err := cp.parseOr()
	if err != nil {
		return delimField{}, 0, err
	}

	if cp.pos != len(cp.toks) {
		return delimField{}, 0, invalid("Invalid field '" + text + "' in space-delimited filter pattern")
	}

	f.cond = cond

	return f, cp.regexes, nil
}

func isFieldName(s string) bool {
	for i := range len(s) {
		if !isSelectorChar(s[i]) || s[i] == '-' {
			return false
		}
	}

	return s != ""
}

func (p *delimPattern) match(message string) bool {
	_, _, ok := p.bind(message)

	return ok
}

// bind assigns the message's fields to the pattern's fields. Without an
// ellipsis the last pattern field takes the rest of the message, which is why
// the docs ask for a trailing blank field ([w1=ERROR, w2]). With an ellipsis
// each named field takes exactly one message field.
func (p *delimPattern) bind(message string) ([]string, []span, bool) {
	tokens := tokenizeMessage(message)
	if len(p.fields) == 0 {
		return tokens, nil, true
	}

	spans := make([]span, len(p.fields))

	return tokens, spans, p.bindFrom(tokens, spans, 0, 0)
}

func (p *delimPattern) bindFrom(tokens []string, spans []span, fi, ti int) bool {
	if fi == len(p.fields) {
		return ti == len(tokens)
	}

	f := p.fields[fi]

	if f.ellipsis {
		for j := ti; j <= len(tokens); j++ {
			spans[fi] = span{ti, j}
			if p.bindFrom(tokens, spans, fi+1, j) {
				return true
			}
		}

		return false
	}

	if ti >= len(tokens) {
		return false
	}

	end := ti + 1
	if !p.hasEllipsis && fi == len(p.fields)-1 {
		end = len(tokens)
	}

	if f.cond != nil && !f.cond.eval(strings.Join(tokens[ti:end], " ")) {
		return false
	}

	spans[fi] = span{ti, end}

	return p.bindFrom(tokens, spans, fi+1, end)
}

// extract names each bound message field: "$name" for a named field and
// "$N" (1-based position in the message) for unnamed fields and the fields an
// ellipsis covers.
func (p *delimPattern) extract(message string) map[string]string {
	out := map[string]string{}

	tokens, spans, ok := p.bind(message)
	if !ok {
		return out
	}

	if len(p.fields) == 0 {
		for i, t := range tokens {
			out[positional(i)] = t
		}

		return out
	}

	for i, f := range p.fields {
		sp := spans[i]

		switch {
		case f.ellipsis:
			for t := sp.start; t < sp.end; t++ {
				out[positional(t)] = tokens[t]
			}
		case f.name != "":
			out["$"+f.name] = strings.Join(tokens[sp.start:sp.end], " ")
		default:
			out[positional(sp.start)] = strings.Join(tokens[sp.start:sp.end], " ")
		}
	}

	return out
}

func positional(i int) string { return "$" + strconv.Itoa(i+1) }

// validRef reports whether "$name" names one of the pattern's fields, or
// "$N" is a field position.
func (p *delimPattern) validRef(ref string) bool {
	name, ok := strings.CutPrefix(ref, "$")
	if !ok || name == "" {
		return false
	}

	if n, err := strconv.Atoi(name); err == nil {
		return n >= 1
	}

	for _, f := range p.fields {
		if f.name == name {
			return true
		}
	}

	return false
}

// tokenizeMessage splits a log event into space-delimited fields. Text in
// double quotes or square brackets is one field, without its delimiters.
func tokenizeMessage(message string) []string {
	var tokens []string

	for i := 0; i < len(message); {
		if message[i] == ' ' || message[i] == '\t' {
			i++
			continue
		}

		tok, next := readField(message, i)
		tokens = append(tokens, tok)
		i = next
	}

	return tokens
}

func readField(message string, i int) (field string, next int) {
	closing := byte(0)

	switch message[i] {
	case '"':
		closing = '"'
	case '[':
		closing = ']'
	}

	if closing != 0 {
		if end := strings.IndexByte(message[i+1:], closing); end >= 0 {
			return message[i+1 : i+1+end], i + end + delimiterPair
		}
	}

	end := strings.IndexAny(message[i:], " \t")
	if end < 0 {
		end = len(message) - i
	}

	return message[i : i+end], i + end
}
