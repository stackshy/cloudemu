package monitoring

import "strings"

// seriesFilter is a parsed Cloud Monitoring timeSeries.list filter. Only the
// clauses a real user reaches for are honored: metric.type equality /
// starts_with, and metric.labels.KEY / resource.labels.KEY equality.
type seriesFilter struct {
	metricType   string
	typeIsPrefix bool
	labels       map[string]string
}

// matchesType reports whether the metric type passes the metric.type clause.
func (f seriesFilter) matchesType(fullType string) bool {
	if f.metricType == "" {
		return true
	}

	if f.typeIsPrefix {
		return strings.HasPrefix(fullType, f.metricType)
	}

	return fullType == f.metricType
}

// matchesLabels reports whether a datum's labels satisfy every label clause.
func (f seriesFilter) matchesLabels(labels map[string]string) bool {
	for k, v := range f.labels {
		if labels[k] != v {
			return false
		}
	}

	return true
}

// parseSeriesFilter extracts the honored clauses from a raw filter string.
func parseSeriesFilter(raw string) seriesFilter {
	f := seriesFilter{labels: map[string]string{}}
	if raw == "" {
		return f
	}

	for _, clause := range splitFilterClauses(raw) {
		key, val, ok := splitClause(clause)
		if !ok {
			continue
		}

		switch {
		case key == "metric.type":
			f.metricType, f.typeIsPrefix = parseTypeValue(val)
		case strings.HasPrefix(key, "metric.labels."):
			f.labels[strings.TrimPrefix(key, "metric.labels.")] = unquote(val)
		case strings.HasPrefix(key, "resource.labels."):
			f.labels[strings.TrimPrefix(key, "resource.labels.")] = unquote(val)
		}
	}

	return f
}

// splitFilterClauses splits on AND (case-insensitive), the only combiner Cloud
// Monitoring list filters use.
func splitFilterClauses(raw string) []string {
	parts := []string{}
	rest := raw

	for {
		idx := indexAND(rest)
		if idx < 0 {
			parts = append(parts, rest)
			break
		}

		parts = append(parts, rest[:idx])
		rest = rest[idx+len(" AND "):]
	}

	return parts
}

func indexAND(s string) int {
	upper := strings.ToUpper(s)

	return strings.Index(upper, " AND ")
}

// splitClause splits "key = value" (or "key op value") into key and value.
func splitClause(clause string) (key, val string, ok bool) {
	i := strings.Index(clause, "=")
	if i < 0 {
		return "", "", false
	}

	return strings.TrimSpace(clause[:i]), strings.TrimSpace(clause[i+1:]), true
}

// parseTypeValue reads a metric.type value, handling starts_with("...").
func parseTypeValue(val string) (mtype string, isPrefix bool) {
	if strings.HasPrefix(val, "starts_with(") {
		inner := strings.TrimSuffix(strings.TrimPrefix(val, "starts_with("), ")")

		return unquote(strings.TrimSpace(inner)), true
	}

	return unquote(val), false
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "\"")
	s = strings.TrimSuffix(s, "\"")

	return s
}
