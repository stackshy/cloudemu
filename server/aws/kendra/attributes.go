package kendra

import (
	"encoding/json"
	"time"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// docAttrValueJSON is the wire shape of a DocumentAttributeValue. A date is an
// epoch-seconds number.
type docAttrValueJSON struct {
	StringValue     *string  `json:"StringValue,omitempty"`
	StringListValue []string `json:"StringListValue,omitempty"`
	LongValue       *int64   `json:"LongValue,omitempty"`
	DateValue       *float64 `json:"DateValue,omitempty"`
}

// docAttrJSON is the wire shape of a DocumentAttribute.
type docAttrJSON struct {
	Key   string           `json:"Key"`
	Value docAttrValueJSON `json:"Value"`
}

const nanosPerSecond = 1e9

func valueFromWire(v docAttrValueJSON) driver.DocumentAttributeValue {
	out := driver.DocumentAttributeValue{
		StringValue: v.StringValue, StringListValue: v.StringListValue, LongValue: v.LongValue,
	}

	if v.DateValue != nil {
		t := time.Unix(0, int64(*v.DateValue*nanosPerSecond)).UTC()
		out.DateValue = &t
	}

	return out
}

func valueToWire(v driver.DocumentAttributeValue) docAttrValueJSON {
	out := docAttrValueJSON{StringValue: v.StringValue, StringListValue: v.StringListValue, LongValue: v.LongValue}

	if v.DateValue != nil {
		f := float64(v.DateValue.UnixNano()) / nanosPerSecond
		out.DateValue = &f
	}

	return out
}

func attrsFromWire(in []docAttrJSON) []driver.DocumentAttribute {
	if in == nil {
		return nil
	}

	out := make([]driver.DocumentAttribute, len(in))
	for i := range in {
		out[i] = driver.DocumentAttribute{Key: in[i].Key, Value: valueFromWire(in[i].Value)}
	}

	return out
}

func attrsToWire(in []driver.DocumentAttribute) []docAttrJSON {
	out := make([]docAttrJSON, len(in))
	for i := range in {
		out[i] = docAttrJSON{Key: in[i].Key, Value: valueToWire(in[i].Value)}
	}

	return out
}

func attrPtrFromWire(a *docAttrJSON) *driver.DocumentAttribute {
	if a == nil {
		return nil
	}

	return &driver.DocumentAttribute{Key: a.Key, Value: valueFromWire(a.Value)}
}

// attributeFilterJSON is the wire shape of an AttributeFilter.
type attributeFilterJSON struct {
	AndAllFilters       []attributeFilterJSON `json:"AndAllFilters,omitempty"`
	OrAllFilters        []attributeFilterJSON `json:"OrAllFilters,omitempty"`
	NotFilter           *attributeFilterJSON  `json:"NotFilter,omitempty"`
	EqualsTo            *docAttrJSON          `json:"EqualsTo,omitempty"`
	ContainsAll         *docAttrJSON          `json:"ContainsAll,omitempty"`
	ContainsAny         *docAttrJSON          `json:"ContainsAny,omitempty"`
	GreaterThan         *docAttrJSON          `json:"GreaterThan,omitempty"`
	GreaterThanOrEquals *docAttrJSON          `json:"GreaterThanOrEquals,omitempty"`
	LessThan            *docAttrJSON          `json:"LessThan,omitempty"`
	LessThanOrEquals    *docAttrJSON          `json:"LessThanOrEquals,omitempty"`
}

func filterFromWire(f *attributeFilterJSON) *driver.AttributeFilter {
	if f == nil {
		return nil
	}

	out := &driver.AttributeFilter{
		EqualsTo: attrPtrFromWire(f.EqualsTo), ContainsAll: attrPtrFromWire(f.ContainsAll),
		ContainsAny: attrPtrFromWire(f.ContainsAny), GreaterThan: attrPtrFromWire(f.GreaterThan),
		GreaterThanOrEquals: attrPtrFromWire(f.GreaterThanOrEquals), LessThan: attrPtrFromWire(f.LessThan),
		LessThanOrEquals: attrPtrFromWire(f.LessThanOrEquals), Not: filterFromWire(f.NotFilter),
	}

	if f.AndAllFilters != nil {
		out.AndAll = make([]driver.AttributeFilter, len(f.AndAllFilters))
		for i := range f.AndAllFilters {
			out.AndAll[i] = *filterFromWire(&f.AndAllFilters[i])
		}
	}

	if f.OrAllFilters != nil {
		out.OrAll = make([]driver.AttributeFilter, len(f.OrAllFilters))
		for i := range f.OrAllFilters {
			out.OrAll[i] = *filterFromWire(&f.OrAllFilters[i])
		}
	}

	return out
}

// rawJSON passes an arbitrary JSON block through verbatim.
type rawJSON = json.RawMessage
