package metricmath

// SEARCH(' {Namespace, DimensionName, ...} SearchTerm ', 'Statistic'[, period])
// returns one series per metric that matches. The rules follow the
// CloudWatch user guide:
// https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/search-expression-syntax.html
//
//   - The metric schema in braces is optional. A metric matches it when its
//     namespace is the schema's namespace and its dimension names are exactly
//     the schema's dimension names.
//   - A bare word is a partial match on tokens of the namespace, the metric
//     name, a dimension name or a dimension value. A double-quoted string is
//     an exact match on one of them.
//   - Name=value (MetricName, Namespace or a dimension name) limits the match
//     to that property.
//   - AND, OR and NOT are case sensitive, terms with no operator between them
//     are joined with AND, and parentheses group.
//
// The cross-account :aws.AccountId designator is not supported, so a search
// that uses it is outside the supported syntax.

import (
	"sort"
	"strings"
	"unicode"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// searchQuery is a parsed search string.
type searchQuery struct {
	schema *metricSchema
	term   searchTerm // nil matches every metric of the schema
}

// metricSchema is the {Namespace, DimensionName, ...} part.
type metricSchema struct {
	namespace string
	dims      []string
}

// searchTerm is one node of the boolean search term.
type searchTerm interface {
	matches(m *driver.MetricIdentifier) bool
}

// searchNode is SEARCH(query, stat[, period]).
type searchNode struct {
	query  searchQuery
	stat   string
	period int
}

// Lister lists the metrics a SEARCH can find: those with data in the past
// two weeks.
type Lister func() ([]driver.MetricIdentifier, error)

func (n searchNode) evaluate(s scope) (result, error) {
	if s.e.lister == nil {
		return result{isArray: true, array: []Labeled{}}, nil
	}

	metrics, err := s.e.lister()
	if err != nil {
		return result{}, err
	}

	period := n.period
	if period == 0 {
		period = s.gridPeriod
	}

	out := []Labeled{}

	for i := range metrics {
		m := &metrics[i]
		if !n.query.matches(m) {
			continue
		}

		series, err := s.e.fetch(&driver.MetricStat{
			Namespace: m.Namespace, MetricName: m.MetricName, Dimensions: m.Dimensions, Period: period, Stat: n.stat,
		}, period)
		if err != nil {
			return result{}, err
		}

		out = append(out, Labeled{Label: searchLabel(m), Series: series})
	}

	return result{isArray: true, array: out}, nil
}

// searchLabel is the label of one found metric: its dimension values in
// dimension-name order, then its metric name. The user guide does not state
// the label GetMetricData gives a SEARCH result, so this is the emulator's
// choice; a Label on the query is put in front of it.
func searchLabel(m *driver.MetricIdentifier) string {
	names := make([]string, 0, len(m.Dimensions))
	for k := range m.Dimensions {
		names = append(names, k)
	}

	sort.Strings(names)

	parts := make([]string, 0, len(names)+1)
	for _, k := range names {
		parts = append(parts, m.Dimensions[k])
	}

	return strings.Join(append(parts, m.MetricName), " ")
}

func (q searchQuery) matches(m *driver.MetricIdentifier) bool {
	if q.schema != nil && !q.schema.matches(m) {
		return false
	}

	return q.term == nil || q.term.matches(m)
}

func (sc *metricSchema) matches(m *driver.MetricIdentifier) bool {
	if m.Namespace != sc.namespace || len(m.Dimensions) != len(sc.dims) {
		return false
	}

	for _, d := range sc.dims {
		if _, ok := m.Dimensions[d]; !ok {
			return false
		}
	}

	return true
}

type andTerm struct{ left, right searchTerm }

func (t andTerm) matches(m *driver.MetricIdentifier) bool {
	return t.left.matches(m) && t.right.matches(m)
}

type orTerm struct{ left, right searchTerm }

func (t orTerm) matches(m *driver.MetricIdentifier) bool {
	return t.left.matches(m) || t.right.matches(m)
}

type notTerm struct{ inner searchTerm }

func (t notTerm) matches(m *driver.MetricIdentifier) bool { return !t.inner.matches(m) }

// valueTerm is an exact or partial match, on every property or on one.
type valueTerm struct {
	property string // "" for any property
	value    string
	exact    bool
}

// Property names a designator can use besides a dimension name.
const (
	propMetricName = "MetricName"
	propNamespace  = "Namespace"
)

func (t valueTerm) matches(m *driver.MetricIdentifier) bool {
	for _, field := range t.fields(m) {
		if t.exact && field == t.value {
			return true
		}

		if !t.exact && partialMatch(t.value, field) {
			return true
		}
	}

	return false
}

// fields returns the strings the term is matched against.
func (t valueTerm) fields(m *driver.MetricIdentifier) []string {
	switch t.property {
	case "":
		out := []string{m.Namespace, m.MetricName}
		for k, v := range m.Dimensions {
			out = append(out, k, v)
		}

		return out
	case propMetricName:
		return []string{m.MetricName}
	case propNamespace:
		return []string{m.Namespace}
	default:
		if v, ok := m.Dimensions[t.property]; ok {
			return []string{v}
		}

		return nil
	}
}

// partialMatch reports whether a search word matches a field by tokens. A
// word that is a single token matches any token of the field, ignoring case,
// or the whole field with its delimiters removed. A word of several tokens
// matches tokens that appear one after another in the field. Within a
// delimiter-free part of the word made of several tokens, such as
// CustomCount, case must match; parts split by delimiters, such as
// network/errors, ignore case.
func partialMatch(word, field string) bool {
	want := wordTokens(word)
	if len(want) == 0 {
		return false
	}

	have := splitTokens(field)

	if len(want) == 1 && strings.EqualFold(want[0].text, strings.Join(alnumParts(field), "")) {
		return true
	}

	for start := 0; start+len(want) <= len(have); start++ {
		if tokensAt(have[start:], want) {
			return true
		}
	}

	return false
}

func tokensAt(have []string, want []wantToken) bool {
	for i, w := range want {
		if w.caseSensitive && have[i] != w.text {
			return false
		}

		if !w.caseSensitive && !strings.EqualFold(have[i], w.text) {
			return false
		}
	}

	return true
}

// wantToken is one token of a search word and whether case must match.
type wantToken struct {
	text          string
	caseSensitive bool
}

func wordTokens(word string) []wantToken {
	var out []wantToken

	for _, part := range alnumParts(word) {
		toks := camelTokens(part)
		for _, t := range toks {
			out = append(out, wantToken{text: t, caseSensitive: len(toks) > 1})
		}
	}

	return out
}

// splitTokens returns the tokens of a field, in order, in their own case.
func splitTokens(field string) []string {
	var out []string

	for _, part := range alnumParts(field) {
		out = append(out, camelTokens(part)...)
	}

	return out
}

// alnumParts splits s at every run of non-alphanumeric characters.
func alnumParts(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// camelTokens splits an alphanumeric string where its case changes from
// lower to upper, before the last capital of a capital run that a lowercase
// letter follows (SDBFailure is SDB, Failure), and between letters and
// digits.
func camelTokens(s string) []string {
	r := []rune(s)

	var (
		out   []string
		start int
	)

	for i := 1; i < len(r); i++ {
		if tokenBoundary(r, i) {
			out = append(out, string(r[start:i]))
			start = i
		}
	}

	return append(out, string(r[start:]))
}

func tokenBoundary(r []rune, i int) bool {
	prev, cur := r[i-1], r[i]

	switch {
	case unicode.IsDigit(prev) != unicode.IsDigit(cur):
		return true
	case unicode.IsLower(prev) && unicode.IsUpper(cur):
		return true
	case unicode.IsUpper(prev) && unicode.IsUpper(cur) && i+1 < len(r) && unicode.IsLower(r[i+1]):
		return true
	default:
		return false
	}
}

// ---- search string parser ----

// Search token kinds.
const (
	sWord   = 'w'
	sQuoted = 'q'
	sEquals = '='
	sOpen   = '('
	sClose  = ')'
	sLBrace = '{'
	sRBrace = '}'
	sBad    = '?'
)

type searchToken struct {
	kind byte
	text string
}

// parseSearchQuery parses a search string. ok is false when it is outside
// the supported syntax.
func parseSearchQuery(src string) (searchQuery, bool) {
	toks, ok := lexSearch(src)
	if !ok {
		return searchQuery{}, false
	}

	p := &searchParser{toks: toks}

	var q searchQuery

	if p.peek(sLBrace) {
		sc, ok := p.parseSchema()
		if !ok {
			return searchQuery{}, false
		}

		q.schema = sc
	}

	if p.done() {
		return q, q.schema != nil
	}

	term, ok := p.parseOr()
	if !ok || !p.done() {
		return searchQuery{}, false
	}

	q.term = term

	return q, true
}

// lexSearch splits a search string into tokens. Commas and white space
// separate tokens.
func lexSearch(src string) ([]searchToken, bool) {
	var out []searchToken

	for i := 0; i < len(src); {
		c := src[i]

		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == ',':
			i++
		case c == '=' || c == '(' || c == ')' || c == '{' || c == '}':
			out = append(out, searchToken{kind: c})
			i++
		case c == '"':
			text, next, ok := lexQuoted(src, i)
			if !ok {
				return nil, false
			}

			out = append(out, searchToken{kind: sQuoted, text: text})
			i = next
		default:
			j := i
			for j < len(src) && !strings.ContainsRune(" \t\n,=(){}\"", rune(src[j])) {
				j++
			}

			word := src[i:j]
			if strings.HasPrefix(word, ":") {
				// :aws.AccountId and other designators are not supported.
				return nil, false
			}

			out = append(out, searchToken{kind: sWord, text: word})
			i = j
		}
	}

	return out, true
}

// lexQuoted reads a double-quoted string. A backslash escapes the next
// character.
func lexQuoted(src string, start int) (text string, next int, ok bool) {
	var b strings.Builder

	for i := start + 1; i < len(src); i++ {
		switch src[i] {
		case '\\':
			if i+1 < len(src) {
				i++
				b.WriteByte(src[i])
			}
		case '"':
			return b.String(), i + 1, true
		default:
			b.WriteByte(src[i])
		}
	}

	return "", 0, false
}

type searchParser struct {
	toks []searchToken
	pos  int
}

func (p *searchParser) done() bool { return p.pos >= len(p.toks) }

func (p *searchParser) peek(kind byte) bool {
	return !p.done() && p.toks[p.pos].kind == kind
}

func (p *searchParser) peekWord(word string) bool {
	return p.peek(sWord) && p.toks[p.pos].text == word
}

// parseSchema reads {Namespace, DimensionName, ...}.
func (p *searchParser) parseSchema() (*metricSchema, bool) {
	p.pos++

	var names []string

	for !p.done() && (p.peek(sWord) || p.peek(sQuoted)) {
		names = append(names, p.toks[p.pos].text)
		p.pos++
	}

	if !p.peek(sRBrace) || len(names) == 0 {
		return nil, false
	}

	p.pos++

	return &metricSchema{namespace: names[0], dims: names[1:]}, true
}

func (p *searchParser) parseOr() (searchTerm, bool) {
	left, ok := p.parseAnd()
	if !ok {
		return nil, false
	}

	for p.peekWord("OR") {
		p.pos++

		right, ok := p.parseAnd()
		if !ok {
			return nil, false
		}

		left = orTerm{left: left, right: right}
	}

	return left, true
}

// parseAnd joins terms with AND, written or implied.
func (p *searchParser) parseAnd() (searchTerm, bool) {
	left, ok := p.parseUnary()
	if !ok {
		return nil, false
	}

	for !p.done() && !p.peek(sClose) && !p.peekWord("OR") {
		if p.peekWord("AND") {
			p.pos++
		}

		right, ok := p.parseUnary()
		if !ok {
			return nil, false
		}

		left = andTerm{left: left, right: right}
	}

	return left, true
}

func (p *searchParser) parseUnary() (searchTerm, bool) {
	if p.peekWord("NOT") {
		p.pos++

		inner, ok := p.parseUnary()
		if !ok {
			return nil, false
		}

		return notTerm{inner: inner}, true
	}

	return p.parsePrimary()
}

func (p *searchParser) parsePrimary() (searchTerm, bool) {
	if p.done() {
		return nil, false
	}

	tok := p.toks[p.pos]

	switch tok.kind {
	case sOpen:
		p.pos++

		inner, ok := p.parseOr()
		if !ok || !p.peek(sClose) {
			return nil, false
		}

		p.pos++

		return inner, true
	case sQuoted:
		p.pos++
		return valueTerm{value: tok.text, exact: true}, true
	case sWord:
		if tok.text == "AND" || tok.text == "OR" {
			return nil, false
		}

		p.pos++

		if p.peek(sEquals) {
			return p.parseDesignated(tok.text)
		}

		return valueTerm{value: tok.text}, true
	default:
		return nil, false
	}
}

// parseDesignated reads the value after Name=.
func (p *searchParser) parseDesignated(property string) (searchTerm, bool) {
	p.pos++

	if p.done() {
		return nil, false
	}

	tok := p.toks[p.pos]

	switch tok.kind {
	case sQuoted:
		p.pos++
		return valueTerm{property: property, value: tok.text, exact: true}, true
	case sWord:
		p.pos++
		return valueTerm{property: property, value: tok.text}, true
	default:
		return nil, false
	}
}
