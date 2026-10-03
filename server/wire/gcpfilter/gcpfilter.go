// Package gcpfilter compiles and evaluates the Compute Engine list filter
// grammar (https://cloud.google.com/compute/docs/reference/rest/v1/instances/list):
// "field op value" comparisons with op one of = != > < >= <= :, or the RE2
// full-match forms "field eq value" / "field ne value". Comparisons combine
// with AND, OR and parentheses; adjacent comparisons are AND-ed. As in
// AIP-160, OR binds tighter than AND. Fields traverse the item's JSON wire
// form, so "labels.env" or "scheduling.automaticRestart" work on any resource.
package gcpfilter

import (
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxExprLen = 2048
	maxDepth   = 32
)

const (
	opEq      = "="
	opNe      = "!="
	opLt      = "<"
	opLe      = "<="
	opGt      = ">"
	opGe      = ">="
	opHas     = ":"
	opRegexEq = "eq"
	opRegexNe = "ne"
)

// ErrInvalid reports a filter that does not parse. Handlers map it to 400
// INVALID_ARGUMENT.
var ErrInvalid = errors.New("invalid list filter expression")

// Filter is a compiled list filter. A nil *Filter matches everything.
type Filter struct {
	root  node
	paths [][]string
}

type node interface {
	eval(item map[string]any) bool
}

type andNode []node

func (a andNode) eval(item map[string]any) bool {
	for _, n := range a {
		if !n.eval(item) {
			return false
		}
	}

	return true
}

type orNode []node

func (o orNode) eval(item map[string]any) bool {
	for _, n := range o {
		if n.eval(item) {
			return true
		}
	}

	return false
}

type cmpNode struct {
	path  []string
	op    string
	value string
	// re is the compiled eq/ne regex, or the '*' wildcard form of = and !=.
	re *regexp.Regexp
}

// Compile parses expr. An empty expression compiles to a nil Filter.
func Compile(expr string) (*Filter, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, nil
	}

	if len(expr) > maxExprLen {
		return nil, ErrInvalid
	}

	p := &parser{src: expr}

	root, err := p.parseAnd(0)
	if err != nil {
		return nil, err
	}

	p.skipSpace()

	if p.pos < len(p.src) {
		return nil, ErrInvalid
	}

	if mixesRegex(root) {
		return nil, ErrInvalid
	}

	return &Filter{root: root, paths: p.paths}, nil
}

// Validate rejects a filter that names a field the item type t does not
// declare, the way Compute answers 400 for a field outside the resource
// schema. Map keys (labels.x) and anything under an interface or raw JSON are
// open, so an optional field that is merely unset on an item is not unknown.
func (f *Filter) Validate(t reflect.Type) error {
	if f == nil {
		return nil
	}

	for _, path := range f.paths {
		if !hasPath(t, path) {
			return ErrInvalid
		}
	}

	return nil
}

func hasPath(t reflect.Type, path []string) bool {
	for ; len(path) > 0; path = path[1:] {
		var open bool
		if t, open = elem(t); open {
			return true
		}

		if t.Kind() == reflect.Map {
			t = t.Elem()
			continue
		}

		if t.Kind() != reflect.Struct {
			return false
		}

		ft, ok := jsonField(t, path[0])
		if !ok {
			return false
		}

		t = ft
	}

	return true
}

// elem strips pointers, slices and arrays. open reports a type whose contents
// are not declared (an interface or raw JSON bytes), under which any field
// path is accepted.
func elem(t reflect.Type) (inner reflect.Type, open bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return t, true
		}

		t = t.Elem()
	}

	return t, t.Kind() == reflect.Interface
}

// jsonField finds the type of the field encoding/json writes under name,
// looking through embedded structs.
func jsonField(t reflect.Type, name string) (reflect.Type, bool) {
	for i := range t.NumField() {
		sf := t.Field(i)
		tag, _, _ := strings.Cut(sf.Tag.Get("json"), ",")

		if sf.Anonymous && tag == "" {
			et := sf.Type
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}

			if ft, ok := jsonField(et, name); ok {
				return ft, true
			}

			continue
		}

		if !sf.IsExported() || tag == "-" {
			continue
		}

		if tag == "" {
			tag = sf.Name
		}

		if tag == name {
			return sf.Type, true
		}
	}

	return nil, false
}

// Match reports whether item (any JSON-marshalable wire struct) satisfies f.
func (f *Filter) Match(item any) bool {
	if f == nil {
		return true
	}

	raw, err := json.Marshal(item)
	if err != nil {
		return false
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}

	return f.root.eval(m)
}

type parser struct {
	src   string
	pos   int
	paths [][]string
}

func (p *parser) skipSpace() {
	for p.pos < len(p.src) && isSpace(p.src[p.pos]) {
		p.pos++
	}
}

func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// parseAnd reads terms joined by AND or by juxtaposition.
func (p *parser) parseAnd(depth int) (node, error) {
	var terms andNode

	for {
		p.skipSpace()

		if p.pos >= len(p.src) || p.src[p.pos] == ')' {
			break
		}

		if len(terms) > 0 && p.keyword("AND") {
			p.skipSpace()
		}

		t, err := p.parseOr(depth)
		if err != nil {
			return nil, err
		}

		terms = append(terms, t)
	}

	if len(terms) == 0 {
		return nil, ErrInvalid
	}

	if len(terms) == 1 {
		return terms[0], nil
	}

	return terms, nil
}

func (p *parser) parseOr(depth int) (node, error) {
	first, err := p.parseTerm(depth)
	if err != nil {
		return nil, err
	}

	terms := orNode{first}

	for {
		p.skipSpace()

		if !p.keyword("OR") {
			break
		}

		t, err := p.parseTerm(depth)
		if err != nil {
			return nil, err
		}

		terms = append(terms, t)
	}

	if len(terms) == 1 {
		return first, nil
	}

	return terms, nil
}

// keyword consumes kw when it appears as a whole word at the cursor.
func (p *parser) keyword(kw string) bool {
	end := p.pos + len(kw)
	if end > len(p.src) || p.src[p.pos:end] != kw {
		return false
	}

	if end < len(p.src) && !isSpace(p.src[end]) && p.src[end] != '(' {
		return false
	}

	p.pos = end

	return true
}

func (p *parser) parseTerm(depth int) (node, error) {
	p.skipSpace()

	if p.pos >= len(p.src) {
		return nil, ErrInvalid
	}

	if p.src[p.pos] != '(' {
		return p.parseComparison()
	}

	if depth >= maxDepth {
		return nil, ErrInvalid
	}

	p.pos++

	inner, err := p.parseAnd(depth + 1)
	if err != nil {
		return nil, err
	}

	p.skipSpace()

	if p.pos >= len(p.src) || p.src[p.pos] != ')' {
		return nil, ErrInvalid
	}

	p.pos++

	return inner, nil
}

// scan consumes bytes up to the next space or a byte in stop.
func (p *parser) scan(stop string) string {
	start := p.pos

	for p.pos < len(p.src) && !isSpace(p.src[p.pos]) && strings.IndexByte(stop, p.src[p.pos]) < 0 {
		p.pos++
	}

	return p.src[start:p.pos]
}

func (p *parser) parseComparison() (node, error) {
	field := p.scan("()=!<>:")
	if field == "" || field == "AND" || field == "OR" || !isLetter(field[0]) {
		return nil, ErrInvalid
	}

	p.skipSpace()

	op := p.parseOp()
	if op == "" {
		return nil, ErrInvalid
	}

	p.skipSpace()

	value, err := p.parseValue()
	if err != nil {
		return nil, err
	}

	re, err := compilePattern(op, value)
	if err != nil {
		return nil, err
	}

	path := strings.Split(field, ".")
	p.paths = append(p.paths, path)

	return &cmpNode{path: path, op: op, value: value, re: re}, nil
}

// compilePattern returns the RE2 full-match regex for eq/ne, the wildcard
// regex for a '*' in an = or != literal, or nil for a plain comparison.
func compilePattern(op, value string) (*regexp.Regexp, error) {
	switch {
	case op == opRegexEq || op == opRegexNe:
		re, err := regexp.Compile("^(?:" + value + ")$")
		if err != nil {
			return nil, ErrInvalid
		}

		return re, nil
	case (op == opEq || op == opNe) && strings.Contains(value, "*"):
		return regexp.MustCompile("^" + strings.ReplaceAll(regexp.QuoteMeta(value), `\*`, ".*") + "$"), nil
	}

	return nil, nil
}

func (p *parser) parseOp() string {
	for _, op := range []string{opNe, opLe, opGe, opEq, opLt, opGt, opHas} {
		if strings.HasPrefix(p.src[p.pos:], op) {
			p.pos += len(op)
			return op
		}
	}

	for _, op := range []string{opRegexEq, opRegexNe} {
		if p.keyword(op) {
			return op
		}
	}

	return ""
}

// parseValue reads a quoted literal, or a bare one that runs to the next
// space or parenthesis (quote a regex that needs parentheses).
func (p *parser) parseValue() (string, error) {
	if p.pos >= len(p.src) {
		return "", ErrInvalid
	}

	if q := p.src[p.pos]; q == '"' || q == '\'' {
		end := strings.IndexByte(p.src[p.pos+1:], q)
		if end < 0 {
			return "", ErrInvalid
		}

		v := p.src[p.pos+1 : p.pos+1+end]
		p.pos += end + 2

		return v, nil
	}

	v := p.scan("()")
	if v == "" {
		return "", ErrInvalid
	}

	return v, nil
}

// mixesRegex reports a filter that combines eq/ne with the comparison
// operators, which Compute rejects.
func mixesRegex(n node) bool {
	var regex, plain bool

	var walk func(node)
	walk = func(n node) {
		switch t := n.(type) {
		case andNode:
			for _, c := range t {
				walk(c)
			}
		case orNode:
			for _, c := range t {
				walk(c)
			}
		case *cmpNode:
			if t.op == opRegexEq || t.op == opRegexNe {
				regex = true
			} else {
				plain = true
			}
		}
	}
	walk(n)

	return regex && plain
}

// eval tests the comparison against every value the path reaches. A field
// that is unset on this item (an absent label, an omitted optional field)
// reaches no values: = and eq do not match, while != and ne do, as on real
// Compute where "labels.env != prod" includes items with no env label.
func (c *cmpNode) eval(item map[string]any) bool {
	leaves := resolve(item, c.path)

	switch c.op {
	case opNe:
		return !anyLeaf(leaves, c.equal)
	case opRegexNe:
		return !anyLeaf(leaves, func(v any) bool { return matchString(v, c.re.MatchString) })
	case opRegexEq:
		return anyLeaf(leaves, func(v any) bool { return matchString(v, c.re.MatchString) })
	case opHas:
		return anyLeaf(leaves, c.has)
	case opEq:
		return anyLeaf(leaves, c.equal)
	default:
		return anyLeaf(leaves, func(v any) bool { return c.ordered(v) })
	}
}

func anyLeaf(leaves []any, f func(any) bool) bool {
	for _, v := range leaves {
		if f(v) {
			return true
		}
	}

	return false
}

// resolve walks path through item, fanning out over arrays, and returns every
// value found at the end. A missing field yields no values.
func resolve(v any, path []string) []any {
	if len(path) == 0 {
		if arr, ok := v.([]any); ok {
			return arr
		}

		return []any{v}
	}

	switch t := v.(type) {
	case map[string]any:
		next, ok := t[path[0]]
		if !ok {
			return nil
		}

		return resolve(next, path[1:])
	case []any:
		var out []any
		for _, e := range t {
			out = append(out, resolve(e, path)...)
		}

		return out
	}

	return nil
}

// scalar renders a JSON leaf for string comparison. Maps and arrays have no
// scalar form.
func scalar(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(t), true
	}

	return "", false
}

// matchString applies f to the value and, for resource URLs such as zone or
// machineType, to the URL's last segment too, so "zone = us-central1-a"
// matches the full zone link.
func matchString(v any, f func(string) bool) bool {
	s, ok := scalar(v)
	if !ok {
		return false
	}

	if f(s) {
		return true
	}

	if i := strings.LastIndexByte(s, '/'); i >= 0 && strings.Contains(s, "://") {
		return f(s[i+1:])
	}

	return false
}

// equal compares a leaf with the literal. A '*' in the literal is a wildcard.
func (c *cmpNode) equal(v any) bool {
	if n, ok := v.(float64); ok {
		f, err := strconv.ParseFloat(c.value, 64)
		return err == nil && f == n
	}

	if c.re != nil {
		return matchString(v, c.re.MatchString)
	}

	return matchString(v, func(s string) bool { return s == c.value })
}

// has implements ':'. "field:*" tests presence; on a map "labels:env" tests
// for the key; on a string it is a substring match; otherwise it is '='.
func (c *cmpNode) has(v any) bool {
	if c.value == "*" {
		return true
	}

	switch t := v.(type) {
	case map[string]any:
		_, ok := t[c.value]
		return ok
	case string:
		return strings.Contains(t, c.value)
	}

	return c.equal(v)
}

func (c *cmpNode) ordered(v any) bool {
	var cmp int

	if n, ok := v.(float64); ok {
		f, err := strconv.ParseFloat(c.value, 64)
		if err != nil {
			return false
		}

		switch {
		case n < f:
			cmp = -1
		case n > f:
			cmp = 1
		}
	} else {
		s, ok := scalar(v)
		if !ok {
			return false
		}

		cmp = strings.Compare(s, c.value)
	}

	switch c.op {
	case opLt:
		return cmp < 0
	case opLe:
		return cmp <= 0
	case opGt:
		return cmp > 0
	default:
		return cmp >= 0
	}
}
