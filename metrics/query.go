package metrics

// Query provides filtering over collected metrics.
type Query struct {
	metrics []Metric
}

// NewQuery creates a Query from a collector.
func NewQuery(c *Collector) *Query {
	return &Query{metrics: c.All()}
}

// ByName filters metrics by name.
func (q *Query) ByName(name string) *Query {
	var filtered []Metric
	for _, m := range q.metrics {
		if m.Name == name {
			filtered = append(filtered, m)
		}
	}
	return &Query{metrics: filtered}
}

// ByType filters metrics by type.
func (q *Query) ByType(t MetricType) *Query {
	var filtered []Metric
	for _, m := range q.metrics {
		if m.Type == t {
			filtered = append(filtered, m)
		}
	}
	return &Query{metrics: filtered}
}

// ByLabel filters metrics that have the given label key=value.
func (q *Query) ByLabel(key, value string) *Query {
	var filtered []Metric
	for _, m := range q.metrics {
		if m.Labels[key] == value {
			filtered = append(filtered, m)
		}
	}
	return &Query{metrics: filtered}
}

// Count returns the number of matching metrics.
func (q *Query) Count() int {
	return len(q.metrics)
}

// Sum returns the sum of all matching metric values.
func (q *Query) Sum() float64 {
	var total float64
	for _, m := range q.metrics {
		total += m.Value
	}
	return total
}

// Results returns all matching metrics.
func (q *Query) Results() []Metric {
	result := make([]Metric, len(q.metrics))
	copy(result, q.metrics)
	return result
}
