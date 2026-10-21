// Package filterpattern implements the CloudWatch Logs filter pattern
// language used by FilterLogEvents, metric filters, subscription filters and
// TestMetricFilter: unstructured terms, %regex% terms, and JSON property
// patterns. See
// https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/FilterAndPatternSyntax.html.
//
// Space-delimited patterns ([a, b, ...]) are not supported yet and are
// rejected rather than matched as plain text.
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
	// maxRegexPerStructuredPattern caps %regex% values in one JSON pattern.
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
		return nil, invalid("space-delimited filter patterns are not supported yet: " + trimmed)
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

// ExtractedValues returns the values of the JSON properties a JSON pattern
// names, keyed by selector (for example "$.latency"), for a message the
// pattern matched. Other pattern forms extract nothing.
func (p *Pattern) ExtractedValues(message string) map[string]string {
	if p.json == nil {
		return map[string]string{}
	}

	return p.json.extract(message)
}

type matchAll struct{}

func (matchAll) match(string) bool { return true }

func invalid(msg string) error {
	return errors.New(errors.InvalidArgument, msg)
}
