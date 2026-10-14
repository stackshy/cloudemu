package bedrockagentruntime

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
)

// RetrievalFilter limits from the service model.
const (
	minFilterGroup = 2
	maxFilterGroup = 5
	maxFilterKey   = 100
)

// retrievalFilter is the RetrievalFilter union: exactly one member is set.
type retrievalFilter struct {
	Equals              *filterAttribute  `json:"equals"`
	NotEquals           *filterAttribute  `json:"notEquals"`
	GreaterThan         *filterAttribute  `json:"greaterThan"`
	GreaterThanOrEquals *filterAttribute  `json:"greaterThanOrEquals"`
	LessThan            *filterAttribute  `json:"lessThan"`
	LessThanOrEquals    *filterAttribute  `json:"lessThanOrEquals"`
	In                  *filterAttribute  `json:"in"`
	NotIn               *filterAttribute  `json:"notIn"`
	StartsWith          *filterAttribute  `json:"startsWith"`
	ListContains        *filterAttribute  `json:"listContains"`
	StringContains      *filterAttribute  `json:"stringContains"`
	AndAll              []retrievalFilter `json:"andAll"`
	OrAll               []retrievalFilter `json:"orAll"`
}

type filterAttribute struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// parseFilter decodes and validates a RetrievalFilter; an empty one is nil.
func parseFilter(raw json.RawMessage, field string) (*retrievalFilter, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil //nolint:nilnil // no filter is not an error.
	}

	var f retrievalFilter
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, errors.Newf(errors.InvalidArgument, "%s is not a valid RetrievalFilter", field)
	}

	var v violations

	f.validate(&v, field)

	if err := v.err(); err != nil {
		return nil, err
	}

	return &f, nil
}

func (f *retrievalFilter) validate(v *violations, field string) {
	set := len(f.attributes())
	if f.AndAll != nil {
		set++
	}

	if f.OrAll != nil {
		set++
	}

	switch {
	case set != 1:
		v.addf("%s must set exactly one filter member", field)
	case f.AndAll != nil:
		validateGroup(v, field+".andAll", f.AndAll)
	case f.OrAll != nil:
		validateGroup(v, field+".orAll", f.OrAll)
	default:
		if _, a := f.member(); a.Key == "" || len(a.Key) > maxFilterKey {
			v.addf("Value at '%s.key' failed to satisfy constraint: Member must have length between 1 and %d", field, maxFilterKey)
		}
	}
}

// validateGroup checks an andAll or orAll list and each filter in it.
func validateGroup(v *violations, field string, group []retrievalFilter) {
	if n := len(group); n < minFilterGroup || n > maxFilterGroup {
		v.addf("Value at '%s' failed to satisfy constraint: Member must have length between %d and %d",
			field, minFilterGroup, maxFilterGroup)
	}

	for i := range group {
		group[i].validate(v, field+".member")
	}
}

// member returns the filter's attribute test and its name, or "" for a group.
func (f *retrievalFilter) member() (string, *filterAttribute) {
	for _, m := range []struct {
		name string
		attr *filterAttribute
	}{
		{"equals", f.Equals}, {"notEquals", f.NotEquals}, {"greaterThan", f.GreaterThan},
		{"greaterThanOrEquals", f.GreaterThanOrEquals}, {"lessThan", f.LessThan}, {"lessThanOrEquals", f.LessThanOrEquals},
		{"in", f.In}, {"notIn", f.NotIn}, {"startsWith", f.StartsWith}, {"listContains", f.ListContains},
		{"stringContains", f.StringContains},
	} {
		if m.attr != nil {
			return m.name, m.attr
		}
	}

	return "", nil
}

func (f *retrievalFilter) attributes() []*filterAttribute {
	var out []*filterAttribute

	for _, a := range []*filterAttribute{
		f.Equals, f.NotEquals, f.GreaterThan, f.GreaterThanOrEquals, f.LessThan, f.LessThanOrEquals,
		f.In, f.NotIn, f.StartsWith, f.ListContains, f.StringContains,
	} {
		if a != nil {
			out = append(out, a)
		}
	}

	return out
}

// attributeTests evaluates each attribute member on a document's value (nil
// when the document lacks the key) and the filter's value.
//
//nolint:gochecknoglobals // fixed table, one entry per RetrievalFilter member.
var attributeTests = map[string]func(attr, want any) bool{
	"equals":              sameValue,
	"notEquals":           func(attr, want any) bool { return !sameValue(attr, want) },
	"greaterThan":         numberTest(func(a, b float64) bool { return a > b }),
	"greaterThanOrEquals": numberTest(func(a, b float64) bool { return a >= b }),
	"lessThan":            numberTest(func(a, b float64) bool { return a < b }),
	"lessThanOrEquals":    numberTest(func(a, b float64) bool { return a <= b }),
	"in":                  inList,
	"notIn":               func(attr, want any) bool { return !inList(attr, want) },
	"startsWith":          startsWith,
	"listContains":        listContains,
	"stringContains":      stringContains,
}

// matches reports whether a document's metadata passes the filter. A missing
// attribute fails every positive test and passes notEquals and notIn.
func (f *retrievalFilter) matches(md map[string]any) bool {
	switch {
	case f == nil:
		return true
	case f.AndAll != nil:
		return !slices.ContainsFunc(f.AndAll, func(g retrievalFilter) bool { return !g.matches(md) })
	case f.OrAll != nil:
		return slices.ContainsFunc(f.OrAll, func(g retrievalFilter) bool { return g.matches(md) })
	}

	name, a := f.member()
	if a == nil {
		return true
	}

	return attributeTests[name](md[a.Key], a.Value)
}

// sameValue compares a metadata value with a filter value decoded from JSON.
func sameValue(attr, want any) bool {
	switch a := attr.(type) {
	case string, float64, bool:
		return a == want
	case []string:
		return sameList(a, want)
	default:
		return false
	}
}

func sameList(attr []string, want any) bool {
	w, ok := want.([]any)
	if !ok || len(w) != len(attr) {
		return false
	}

	for i := range attr {
		if s, ok := w[i].(string); !ok || s != attr[i] {
			return false
		}
	}

	return true
}

func numberTest(cmp func(a, b float64) bool) func(attr, want any) bool {
	return func(attr, want any) bool {
		a, ok := attr.(float64)
		b, bok := want.(float64)

		return ok && bok && cmp(a, b)
	}
}

func startsWith(attr, want any) bool {
	s, ok := attr.(string)
	p, pok := want.(string)

	return ok && pok && strings.HasPrefix(s, p)
}

func listContains(attr, want any) bool {
	list, ok := attr.([]string)
	s, sok := want.(string)

	return ok && sok && slices.Contains(list, s)
}

func inList(attr, want any) bool {
	list, ok := want.([]any)

	return ok && slices.ContainsFunc(list, func(w any) bool { return sameValue(attr, w) })
}

func stringContains(attr, want any) bool {
	sub, ok := want.(string)
	if !ok {
		return false
	}

	switch a := attr.(type) {
	case string:
		return strings.Contains(a, sub)
	case []string:
		return slices.ContainsFunc(a, func(s string) bool { return strings.Contains(s, sub) })
	default:
		return false
	}
}
