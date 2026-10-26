package cloudwatchlogs

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs/filterpattern"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

const (
	// maxRegexFiltersPerGroup is the number of metric and subscription
	// filters, together, a log group may have whose pattern contains a regex.
	maxRegexFiltersPerGroup = 5
	// maxTestMetricFilterMessages bounds TestMetricFilter logEventMessages.
	maxTestMetricFilterMessages = 50
)

func patternHasRegex(pattern string) bool {
	p, err := filterpattern.Parse(pattern)

	return err == nil && p.HasRegex()
}

// filterKind names the filter store a filter lives in, so replacing a filter
// does not count against the regex quota twice.
type filterKind int

const (
	metricFilterKind filterKind = iota
	subscriptionFilterKind
)

// validateFilterPattern parses a metric or subscription filter pattern and
// enforces the per-group quota on patterns that contain a regex. name and
// kind identify the filter being put; an existing filter with the same name
// and kind is being replaced and is left out of the count.
func validateFilterPattern(g *logGroup, kind filterKind, name, pattern string) error {
	p, err := filterpattern.Parse(pattern)
	if err != nil {
		return err
	}

	if !p.HasRegex() {
		return nil
	}

	if countRegexFilters(g, kind, name) >= maxRegexFiltersPerGroup {
		return errors.Newf(errors.ResourceExhausted,
			"log group %q already has the maximum of %d metric and subscription filters whose pattern contains a regex",
			g.info.Name, maxRegexFiltersPerGroup)
	}

	return nil
}

// TestMetricFilter runs a filter pattern over sample messages and returns the
// ones it matches, numbered from 0 in input order, with the values a JSON
// pattern extracts.
func (*Mock) TestMetricFilter(
	_ context.Context, pattern string, messages []string,
) ([]driver.MetricFilterMatch, error) {
	if len(messages) == 0 || len(messages) > maxTestMetricFilterMessages {
		return nil, errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value at 'logEventMessages' failed to satisfy constraint: "+
				"Member must have length between 1 and %d", maxTestMetricFilterMessages)
	}

	p, err := filterpattern.Parse(pattern)
	if err != nil {
		return nil, err
	}

	out := make([]driver.MetricFilterMatch, 0, len(messages))

	for i, msg := range messages {
		if msg == "" {
			return nil, errors.Newf(errors.InvalidArgument,
				"1 validation error detected: Value '' at 'logEventMessages.%d.member' failed to satisfy constraint: "+
					"Member must have length greater than or equal to 1", i+1)
		}

		if p.Match(msg) {
			out = append(out, driver.MetricFilterMatch{
				EventNumber: int64(i), EventMessage: msg, ExtractedValues: p.ExtractedValues(msg),
			})
		}
	}

	return out, nil
}

// countRegexFilters counts the group's metric and subscription filters whose
// pattern contains a regex, leaving out the filter named name of kind kind.
func countRegexFilters(g *logGroup, kind filterKind, name string) int {
	n := 0

	for fname, mf := range g.metricFilters.All() {
		if (kind != metricFilterKind || fname != name) && patternHasRegex(mf.FilterPattern) {
			n++
		}
	}

	for fname, sf := range g.subFilters.All() {
		if (kind != subscriptionFilterKind || fname != name) && patternHasRegex(sf.FilterPattern) {
			n++
		}
	}

	return n
}
