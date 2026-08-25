package eventgrid

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// Event Grid advanced filter operator types (ARM's AdvancedFilterOperatorType
// enum). Only the operators that make sense against the mock's untyped JSON
// "data" body are implemented; the rest fall through matchAdvancedFilter's
// default case and never reject an event (matching real Event Grid's design
// where filter evaluation only narrows delivery, never errors the publish).
const (
	opStringIn            = "StringIn"
	opStringNotIn         = "StringNotIn"
	opStringBeginsWith    = "StringBeginsWith"
	opStringNotBeginsWith = "StringNotBeginsWith"
	opStringEndsWith      = "StringEndsWith"
	opStringNotEndsWith   = "StringNotEndsWith"
	opStringContains      = "StringContains"
	opStringNotContains   = "StringNotContains"
	opNumberIn            = "NumberIn"
	opNumberNotIn         = "NumberNotIn"
	opNumberGreaterThan   = "NumberGreaterThan"
	opNumberGreaterOrEq   = "NumberGreaterThanOrEquals"
	opNumberLessThan      = "NumberLessThan"
	opNumberLessOrEq      = "NumberLessThanOrEquals"
	opBoolEquals          = "BoolEquals"
	opIsNullOrUndefined   = "IsNullOrUndefined"
	opIsNotNull           = "IsNotNull"
)

// advancedFilter is one entry of an ARM EventSubscriptionFilter.advancedFilters
// array (AdvancedFilterOperatorType discriminated union, flattened: the
// operators carry either a scalar Value or a Values array, never both).
type advancedFilter struct {
	OperatorType string          `json:"operatorType"`
	Key          string          `json:"key"`
	Value        json.RawMessage `json:"value,omitempty"`
	Values       json.RawMessage `json:"values,omitempty"`
}

// subscriptionFilter is the parsed form of an ARM EventSubscriptionFilter
// (properties.filter on an event subscription). It is the Azure-only
// counterpart to the generic driver.Rule.EventPattern content filter AWS
// EventBridge uses — Event Grid's filter shape (subject prefix/suffix,
// included event types, advanced per-field operators) has no equivalent in
// the portable eventbus driver, so it stays local to this provider package.
type subscriptionFilter struct {
	SubjectBeginsWith      string
	SubjectEndsWith        string
	IsSubjectCaseSensitive bool
	IncludedEventTypes     []string
	AdvancedFilters        []advancedFilter
}

// parseSubscriptionFilter extracts the "filter" object from a raw ARM
// EventSubscription properties JSON blob. An absent or unparsable filter
// yields the zero value, which matches every event (ARM's default when no
// filter is configured).
func parseSubscriptionFilter(rawProperties string) subscriptionFilter {
	if rawProperties == "" {
		return subscriptionFilter{}
	}

	var body struct {
		Filter struct {
			SubjectBeginsWith      string           `json:"subjectBeginsWith"`
			SubjectEndsWith        string           `json:"subjectEndsWith"`
			IsSubjectCaseSensitive bool             `json:"isSubjectCaseSensitive"`
			IncludedEventTypes     []string         `json:"includedEventTypes"`
			AdvancedFilters        []advancedFilter `json:"advancedFilters"`
		} `json:"filter"`
	}

	if err := json.Unmarshal([]byte(rawProperties), &body); err != nil {
		return subscriptionFilter{}
	}

	return subscriptionFilter{
		SubjectBeginsWith:      body.Filter.SubjectBeginsWith,
		SubjectEndsWith:        body.Filter.SubjectEndsWith,
		IsSubjectCaseSensitive: body.Filter.IsSubjectCaseSensitive,
		IncludedEventTypes:     body.Filter.IncludedEventTypes,
		AdvancedFilters:        body.Filter.AdvancedFilters,
	}
}

// matches reports whether event satisfies every configured filter criterion.
// All criteria are ANDed together, matching real Event Grid subscription
// filtering.
func (f *subscriptionFilter) matches(event *driver.Event) bool {
	if !f.subjectMatches(event.Subject) {
		return false
	}

	if len(f.IncludedEventTypes) > 0 && !stringInSlice(event.DetailType, f.IncludedEventTypes) {
		return false
	}

	data := parseEventData(event.Detail)
	for i := range f.AdvancedFilters {
		if !matchAdvancedFilter(&f.AdvancedFilters[i], event, data) {
			return false
		}
	}

	return true
}

func (f *subscriptionFilter) subjectMatches(subject string) bool {
	s, begins, ends := subject, f.SubjectBeginsWith, f.SubjectEndsWith
	if !f.IsSubjectCaseSensitive {
		s, begins, ends = strings.ToLower(s), strings.ToLower(begins), strings.ToLower(ends)
	}

	if begins != "" && !strings.HasPrefix(s, begins) {
		return false
	}

	if ends != "" && !strings.HasSuffix(s, ends) {
		return false
	}

	return true
}

func stringInSlice(v string, list []string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}

	return false
}

// parseEventData unmarshals the event's JSON detail body for advanced-filter
// field lookups. An empty or unparsable body yields an empty object so a
// filter referencing "data.*" simply finds nothing rather than panicking.
func parseEventData(detail string) map[string]any {
	if detail == "" {
		return map[string]any{}
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(detail), &m); err != nil {
		return map[string]any{}
	}

	return m
}

// filterField resolves an advanced filter's "key" against the event. "subject"
// and "eventType" address the envelope directly; a "data."-prefixed (or bare)
// key walks the parsed event data body by dotted path, matching how Event Grid
// addresses fields inside the event payload.
func filterField(key string, event *driver.Event, data map[string]any) (any, bool) {
	switch key {
	case "subject":
		return event.Subject, true
	case "eventType":
		return event.DetailType, true
	case "id":
		return event.ID, true
	}

	path := strings.TrimPrefix(key, "data.")

	var cur any = data
	for _, seg := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}

		cur, ok = obj[seg]
		if !ok {
			return nil, false
		}
	}

	return cur, true
}

// matchAdvancedFilter evaluates one advanced filter operator against the
// resolved field value. Split by operator category (string/number/bool) to
// keep each switch small; an unrecognized/unsupported operator never blocks
// delivery on a filter this mock doesn't model.
func matchAdvancedFilter(af *advancedFilter, event *driver.Event, data map[string]any) bool {
	val, found := filterField(af.Key, event, data)

	switch af.OperatorType {
	case opIsNullOrUndefined:
		return !found || val == nil
	case opIsNotNull:
		return found && val != nil
	}

	if !found {
		// Per Event Grid's filtering semantics, a missing key evaluates as
		// MATCHED only for the two negative-membership operators (the field
		// genuinely can't be in the excluded set); every other operator is
		// NOT-matched on a missing key. See Microsoft Learn "Event Filtering".
		return af.OperatorType == opStringNotIn || af.OperatorType == opNumberNotIn
	}

	switch {
	case strings.HasPrefix(af.OperatorType, "String"):
		return matchStringFilter(af, val)
	case strings.HasPrefix(af.OperatorType, "Number"):
		return matchNumberFilter(af, val)
	case af.OperatorType == opBoolEquals:
		var want bool
		_ = json.Unmarshal(af.Value, &want)

		return asBool(val) == want
	default:
		return true
	}
}

func matchStringFilter(af *advancedFilter, val any) bool {
	s := asString(val)

	switch af.OperatorType {
	case opStringIn:
		return stringInSlice(s, decodeStrings(af.Values))
	case opStringNotIn:
		return !stringInSlice(s, decodeStrings(af.Values))
	case opStringBeginsWith:
		return anyPrefixSuffix(s, decodeStrings(af.Values), strings.HasPrefix)
	case opStringNotBeginsWith:
		return !anyPrefixSuffix(s, decodeStrings(af.Values), strings.HasPrefix)
	case opStringEndsWith:
		return anyPrefixSuffix(s, decodeStrings(af.Values), strings.HasSuffix)
	case opStringNotEndsWith:
		return !anyPrefixSuffix(s, decodeStrings(af.Values), strings.HasSuffix)
	case opStringContains:
		return anyPrefixSuffix(s, decodeStrings(af.Values), strings.Contains)
	case opStringNotContains:
		return !anyPrefixSuffix(s, decodeStrings(af.Values), strings.Contains)
	default:
		return true
	}
}

func matchNumberFilter(af *advancedFilter, val any) bool {
	n := asNumber(val)

	switch af.OperatorType {
	case opNumberIn:
		return numberInSlice(val, decodeNumbers(af.Values))
	case opNumberNotIn:
		return !numberInSlice(val, decodeNumbers(af.Values))
	case opNumberGreaterThan:
		return n > decodeNumber(af.Value)
	case opNumberGreaterOrEq:
		return n >= decodeNumber(af.Value)
	case opNumberLessThan:
		return n < decodeNumber(af.Value)
	case opNumberLessOrEq:
		return n <= decodeNumber(af.Value)
	default:
		return true
	}
}

func anyPrefixSuffix(v string, prefixes []string, cmp func(s, prefix string) bool) bool {
	for _, p := range prefixes {
		if cmp(v, p) {
			return true
		}
	}

	return false
}

func numberInSlice(v any, list []float64) bool {
	n := asNumber(v)
	for _, item := range list {
		if item == n {
			return true
		}
	}

	return false
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}

	return ""
}

func asNumber(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	default:
		return 0
	}
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func decodeStrings(raw json.RawMessage) []string {
	var out []string
	_ = json.Unmarshal(raw, &out)

	return out
}

func decodeNumbers(raw json.RawMessage) []float64 {
	var out []float64
	_ = json.Unmarshal(raw, &out)

	return out
}

func decodeNumber(raw json.RawMessage) float64 {
	var f float64
	_ = json.Unmarshal(raw, &f)

	return f
}
