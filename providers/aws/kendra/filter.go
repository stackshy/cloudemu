package kendra

import (
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// valueTypeString is the DocumentAttributeValueType of a string attribute.
const valueTypeString = "STRING_VALUE"

// maxFilterDepth bounds the nesting of AndAll / OrAll / Not filters.
const maxFilterDepth = 20

func valueTypeOf(v driver.DocumentAttributeValue) string {
	switch {
	case v.StringListValue != nil:
		return "STRING_LIST_VALUE"
	case v.LongValue != nil:
		return "LONG_VALUE"
	case v.DateValue != nil:
		return "DATE_VALUE"
	default:
		return valueTypeString
	}
}

// valueLabels flattens a value into the labels facets count (a list attribute
// counts each member).
func valueLabels(v driver.DocumentAttributeValue) []string {
	switch {
	case v.StringListValue != nil:
		return v.StringListValue
	case v.LongValue != nil:
		return []string{strconv.FormatInt(*v.LongValue, 10)}
	case v.DateValue != nil:
		return []string{v.DateValue.UTC().Format(time.RFC3339)}
	case v.StringValue != nil:
		return []string{*v.StringValue}
	default:
		return nil
	}
}

// singleValue rebuilds a value holding just one facet label.
func singleValue(v driver.DocumentAttributeValue, label string) driver.DocumentAttributeValue {
	switch {
	case v.StringListValue != nil:
		return driver.DocumentAttributeValue{StringValue: &label}
	case v.LongValue != nil, v.DateValue != nil:
		return copyAttributeValue(v)
	default:
		return driver.DocumentAttributeValue{StringValue: &label}
	}
}

// evalFilter reports whether a document's attributes satisfy an attribute
// filter. A nil filter matches everything; a filter node must set exactly one
// member and a comparison must use a value of a matching type, otherwise the
// request is invalid.
func evalFilter(f *driver.AttributeFilter, attrs []driver.DocumentAttribute) (bool, error) {
	return evalNode(f, attrs, 0)
}

func evalNode(f *driver.AttributeFilter, attrs []driver.DocumentAttribute, depth int) (bool, error) {
	if f == nil {
		return true, nil
	}

	if depth > maxFilterDepth {
		return false, validation("AttributeFilter is nested too deeply")
	}

	if n := filterMembers(f); n != 1 {
		return false, validation("each AttributeFilter must set exactly one member, got %d", n)
	}

	switch {
	case f.AndAll != nil:
		return evalAll(f.AndAll, attrs, depth, true)
	case f.OrAll != nil:
		return evalAll(f.OrAll, attrs, depth, false)
	case f.Not != nil:
		ok, err := evalNode(f.Not, attrs, depth+1)

		return !ok, err
	}

	return evalComparison(f, attrs)
}

func filterMembers(f *driver.AttributeFilter) int {
	n := 0

	for _, set := range []bool{
		f.AndAll != nil, f.OrAll != nil, f.Not != nil, f.EqualsTo != nil, f.ContainsAll != nil,
		f.ContainsAny != nil, f.GreaterThan != nil, f.GreaterThanOrEquals != nil, f.LessThan != nil,
		f.LessThanOrEquals != nil,
	} {
		if set {
			n++
		}
	}

	return n
}

func evalAll(filters []driver.AttributeFilter, attrs []driver.DocumentAttribute, depth int, all bool) (bool, error) {
	result := all

	for i := range filters {
		ok, err := evalNode(&filters[i], attrs, depth+1)
		if err != nil {
			return false, err
		}

		if all && !ok {
			result = false
		}

		if !all && ok {
			result = true
		}
	}

	return result, nil
}

func evalComparison(f *driver.AttributeFilter, attrs []driver.DocumentAttribute) (bool, error) {
	switch {
	case f.EqualsTo != nil:
		return docEquals(f.EqualsTo, attrs), nil
	case f.ContainsAll != nil:
		return docContains(f.ContainsAll, attrs, true)
	case f.ContainsAny != nil:
		return docContains(f.ContainsAny, attrs, false)
	case f.GreaterThan != nil:
		return docOrdered(f.GreaterThan, attrs, func(c int) bool { return c > 0 })
	case f.GreaterThanOrEquals != nil:
		return docOrdered(f.GreaterThanOrEquals, attrs, func(c int) bool { return c >= 0 })
	case f.LessThan != nil:
		return docOrdered(f.LessThan, attrs, func(c int) bool { return c < 0 })
	default:
		return docOrdered(f.LessThanOrEquals, attrs, func(c int) bool { return c <= 0 })
	}
}

func docEquals(want *driver.DocumentAttribute, attrs []driver.DocumentAttribute) bool {
	got, ok := attrOf(attrs, want.Key)
	if !ok {
		return false
	}

	switch {
	case want.Value.StringValue != nil:
		return got.StringValue != nil && *got.StringValue == *want.Value.StringValue
	case want.Value.LongValue != nil:
		return got.LongValue != nil && *got.LongValue == *want.Value.LongValue
	case want.Value.DateValue != nil:
		return got.DateValue != nil && got.DateValue.Equal(*want.Value.DateValue)
	case want.Value.StringListValue != nil:
		a := slices.Clone(got.StringListValue)
		b := slices.Clone(want.Value.StringListValue)

		sort.Strings(a)
		sort.Strings(b)

		return slices.Equal(a, b)
	}

	return false
}

func docContains(want *driver.DocumentAttribute, attrs []driver.DocumentAttribute, all bool) (bool, error) {
	if want.Value.StringListValue == nil {
		return false, validation("ContainsAll and ContainsAny require a StringListValue")
	}

	got, ok := attrOf(attrs, want.Key)
	if !ok {
		return false, nil
	}

	have := got.StringListValue
	if have == nil && got.StringValue != nil {
		have = []string{*got.StringValue}
	}

	matched := 0

	for _, w := range want.Value.StringListValue {
		if slices.Contains(have, w) {
			matched++
		}
	}

	if all {
		return matched == len(want.Value.StringListValue), nil
	}

	return matched > 0, nil
}

func docOrdered(want *driver.DocumentAttribute, attrs []driver.DocumentAttribute, test func(int) bool) (bool, error) {
	if want.Value.LongValue == nil && want.Value.DateValue == nil {
		return false, validation("GreaterThan and LessThan comparisons require a LongValue or DateValue")
	}

	got, ok := attrOf(attrs, want.Key)
	if !ok {
		return false, nil
	}

	switch {
	case want.Value.LongValue != nil && got.LongValue != nil:
		return test(cmpInt(*got.LongValue, *want.Value.LongValue)), nil
	case want.Value.DateValue != nil && got.DateValue != nil:
		return test(cmpTime(*got.DateValue, *want.Value.DateValue)), nil
	}

	return false, nil
}
