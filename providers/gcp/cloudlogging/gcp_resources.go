package cloudlogging

import (
	"context"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// Compile-time check that Mock implements the optional GCP-only surface.
var _ driver.GCPLogging = (*Mock)(nil)

// defaultSinkWriterIdentity is the service account Cloud Logging reports as the
// writer for a sink whose caller did not request a unique one.
const defaultSinkWriterIdentity = "serviceAccount:cloud-logs@system.gserviceaccount.com"

func resourceKey(project, name string) string {
	return project + "/" + name
}

// CreateSink stores a new export sink under project.
func (m *Mock) CreateSink(_ context.Context, project string, sink *driver.LogSink) (*driver.LogSink, error) {
	if sink.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "sink name is required")
	}

	if sink.Destination == "" {
		return nil, errors.New(errors.InvalidArgument, "sink destination is required")
	}

	key := resourceKey(project, sink.Name)
	if m.sinks.Has(key) {
		return nil, errors.Newf(errors.AlreadyExists, "sink %q already exists", sink.Name)
	}

	now := m.opts.Clock.Now().UTC()

	stored := *sink
	stored.CreatedAt = now
	stored.UpdatedAt = now

	if stored.WriterIdentity == "" {
		stored.WriterIdentity = defaultSinkWriterIdentity
	}

	m.sinks.Set(key, &stored)

	result := stored

	return &result, nil
}

// GetSink returns the sink named name under project.
func (m *Mock) GetSink(_ context.Context, project, name string) (*driver.LogSink, error) {
	s, ok := m.sinks.Get(resourceKey(project, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "sink %q not found", name)
	}

	result := *s

	return &result, nil
}

// ListSinks lists all sinks under project in name order.
func (m *Mock) ListSinks(_ context.Context, project string) ([]driver.LogSink, error) {
	prefix := project + "/"

	sinks := make([]driver.LogSink, 0)

	for key, s := range m.sinks.All() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		sinks = append(sinks, *s)
	}

	sort.Slice(sinks, func(i, j int) bool { return sinks[i].Name < sinks[j].Name })

	return sinks, nil
}

// UpdateSink replaces the mutable fields of an existing sink (identity and
// createTime are preserved).
func (m *Mock) UpdateSink(_ context.Context, project string, sink *driver.LogSink) (*driver.LogSink, error) {
	key := resourceKey(project, sink.Name)

	existing, ok := m.sinks.Get(key)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "sink %q not found", sink.Name)
	}

	updated := *existing
	updated.Destination = sink.Destination
	updated.Filter = sink.Filter
	updated.Description = sink.Description
	updated.Disabled = sink.Disabled
	updated.IncludeChildren = sink.IncludeChildren
	updated.UpdatedAt = m.opts.Clock.Now().UTC()

	m.sinks.Set(key, &updated)

	result := updated

	return &result, nil
}

// DeleteSink removes the sink named name under project.
func (m *Mock) DeleteSink(_ context.Context, project, name string) error {
	if !m.sinks.Delete(resourceKey(project, name)) {
		return errors.Newf(errors.NotFound, "sink %q not found", name)
	}

	return nil
}

// CreateLogMetric stores a new log-based metric under project.
func (m *Mock) CreateLogMetric(_ context.Context, project string, metric *driver.LogBasedMetric) (*driver.LogBasedMetric, error) {
	if metric.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "metric name is required")
	}

	if metric.Filter == "" {
		return nil, errors.New(errors.InvalidArgument, "metric filter is required")
	}

	key := resourceKey(project, metric.Name)
	if m.metrics.Has(key) {
		return nil, errors.Newf(errors.AlreadyExists, "metric %q already exists", metric.Name)
	}

	now := m.opts.Clock.Now().UTC()

	stored := *metric
	stored.CreatedAt = now
	stored.UpdatedAt = now

	m.metrics.Set(key, &stored)

	result := stored

	return &result, nil
}

// GetLogMetric returns the metric named name under project.
func (m *Mock) GetLogMetric(_ context.Context, project, name string) (*driver.LogBasedMetric, error) {
	mm, ok := m.metrics.Get(resourceKey(project, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "metric %q not found", name)
	}

	result := *mm

	return &result, nil
}

// ListLogMetrics lists all log-based metrics under project in name order.
func (m *Mock) ListLogMetrics(_ context.Context, project string) ([]driver.LogBasedMetric, error) {
	prefix := project + "/"

	metrics := make([]driver.LogBasedMetric, 0)

	for key, mm := range m.metrics.All() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		metrics = append(metrics, *mm)
	}

	sort.Slice(metrics, func(i, j int) bool { return metrics[i].Name < metrics[j].Name })

	return metrics, nil
}

// UpdateLogMetric replaces the mutable fields of an existing metric (createTime
// is preserved).
func (m *Mock) UpdateLogMetric(_ context.Context, project string, metric *driver.LogBasedMetric) (*driver.LogBasedMetric, error) {
	key := resourceKey(project, metric.Name)

	existing, ok := m.metrics.Get(key)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "metric %q not found", metric.Name)
	}

	updated := *existing
	updated.Description = metric.Description
	updated.Filter = metric.Filter
	updated.ValueExtractor = metric.ValueExtractor
	updated.MetricKind = metric.MetricKind
	updated.ValueType = metric.ValueType
	updated.Unit = metric.Unit
	updated.UpdatedAt = m.opts.Clock.Now().UTC()

	m.metrics.Set(key, &updated)

	result := updated

	return &result, nil
}

// DeleteLogMetric removes the metric named name under project.
func (m *Mock) DeleteLogMetric(_ context.Context, project, name string) error {
	if !m.metrics.Delete(resourceKey(project, name)) {
		return errors.Newf(errors.NotFound, "metric %q not found", name)
	}

	return nil
}
