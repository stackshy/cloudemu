// Package filterpattern implements the CloudWatch Logs filter pattern
// language used by FilterLogEvents, metric filters, subscription filters and
// TestMetricFilter: unstructured terms, %regex% terms, JSON property patterns,
// and space-delimited field patterns. See
// https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/FilterAndPatternSyntax.html.
package filterpattern

import (
	"strings"
	"unicode/utf8"

	"github.com/stackshy/cloudemu/v2/errors"
)

// Limits from the filter pattern syntax reference.
const (
	// maxPatternLength is the longest filterPattern the API accepts.
	maxPatternLength = 1024
	// maxRegexPerStructuredPattern caps %regex% values in one JSON or
	// space-delimited pattern.
	maxRegexPerStructuredPattern = 2
	// maxWildcardsPerSelector caps * selectors in one property selector.
	maxWildcardsPerSelector = 1
	// maxWildcardsPerPattern caps * selectors across a compound JSON pattern.
	maxWildcardsPerPattern = 3
)

// matcher is one parsed pattern form.
type matcher interface {
	match(message string) bool
}

// Pattern is a parsed filter pattern. The zero value is not usable; build one
// with Parse.
type Pattern struct {
	m        matcher
	hasRegex bool
	json     *jsonPattern
	delim    *delimPattern
}

// Parse parses a filter pattern. An empty or blank pattern matches every
// event. A malformed pattern is an InvalidArgument error, which the wire layer
// reports as InvalidParameterException.
func Parse(raw string) (*Pattern, error) {
	if len(raw) > maxPatternLength {
		return nil, errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value at 'filterPattern' failed to satisfy constraint: "+
				"Member must have length less than or equal to %d", maxPatternLength)
	}

	if !utf8.ValidString(raw) {
		return nil, invalid("Invalid filter pattern: not valid UTF-8")
	}

	trimmed := strings.TrimSpace(raw)

	switch {
	case trimmed == "":
		return &Pattern{m: matchAll{}}, nil
	case strings.HasPrefix(trimmed, "{"):
		jp, err := parseJSONPattern(trimmed)
		if err != nil {
			return nil, err
		}

		return &Pattern{m: jp, hasRegex: jp.regexCount > 0, json: jp}, nil
	case strings.HasPrefix(trimmed, "["):
		dp, err := parseDelimitedPattern(trimmed)
		if err != nil {
			return nil, err
		}

		return &Pattern{m: dp, hasRegex: dp.regexCount > 0, delim: dp}, nil
	default:
		tp, err := parseTerms(trimmed)
		if err != nil {
			return nil, err
		}

		return &Pattern{m: tp, hasRegex: tp.hasRegex}, nil
	}
}

// Match reports whether a log event message satisfies the pattern.
func (p *Pattern) Match(message string) bool {
	return p.m.match(message)
}

// HasRegex reports whether the pattern contains a %regex% term. CloudWatch
// Logs limits how many such patterns a log group may hold.
func (p *Pattern) HasRegex() bool {
	return p.hasRegex
}

// ExtractedValues returns the values a structured pattern pulls out of a
// message it matched: JSON properties keyed by selector (for example
// "$.latency"), and space-delimited fields keyed "$name" or, for unnamed
// fields, "$N" by position. Unstructured patterns extract nothing.
func (p *Pattern) ExtractedValues(message string) map[string]string {
	switch {
	case p.json != nil:
		return p.json.extract(message)
	case p.delim != nil:
		return p.delim.extract(message)
	}

	return map[string]string{}
}

// HasFields reports whether the pattern selects fields (a JSON or
// space-delimited pattern), which metric filter dimensions require.
func (p *Pattern) HasFields() bool {
	return p.json != nil || p.delim != nil
}

// ValidRef reports whether ref is a field reference the pattern can resolve:
// a "$.path" selector without wildcards for a JSON pattern, or "$name" (a
// named field) or "$N" (a 1-based position) for a space-delimited one.
func (p *Pattern) ValidRef(ref string) bool {
	switch {
	case p.json != nil:
		sel, end, err := lexSelector(ref, 0)

		return err == nil && end == len(ref) && strings.HasPrefix(ref, "$.") && !hasWildcard(sel.steps)
	case p.delim != nil:
		return p.delim.validRef(ref)
	}

	return false
}

// Value resolves a metric filter field reference ("$.latency" for a JSON
// pattern, "$size" or "$3" for a space-delimited one) in a matched message.
func (p *Pattern) Value(message, ref string) (string, bool) {
	if p.json != nil {
		return jsonValue(message, ref)
	}

	v, ok := p.ExtractedValues(message)[ref]

	return v, ok
}

type matchAll struct{}

func (matchAll) match(string) bool { return true }

func invalid(msg string) error {
	return errors.New(errors.InvalidArgument, msg)
}
