package logging

import (
	"context"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// The portable driver's projection onto OCI Logging: a log group is the log
// group, a log stream is a CUSTOM log inside it, and a log event is an
// ingested log entry.

// viaServiceConnector is what OCI does instead of a metric filter.
const viaServiceConnector = "a Service Connector routes matching log entries into Monitoring"

// CreateLogGroup creates a log group in the compartment the config's scope
// names, or the configured default compartment.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateLogGroup(_ context.Context, cfg driver.LogGroupConfig) (*driver.LogGroupInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, err := m.createGroup(LogGroupSpec{
		CompartmentID: cfg.Scope.Compartment,
		DisplayName:   cfg.Name,
		FreeformTags:  cfg.Tags,
		RetentionDays: cfg.RetentionDays,
	})
	if err != nil {
		return nil, err
	}

	info := m.toLogGroupInfo(g)

	return &info, nil
}

// UpdateLogGroup replaces the mutable fields of an existing log group.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) UpdateLogGroup(_ context.Context, cfg driver.LogGroupConfig) (*driver.LogGroupInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, ok := m.groupByName(cfg.Name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "log group %q not found", cfg.Name)
	}

	if cfg.RetentionDays != 0 {
		g.RetentionDays = cfg.RetentionDays
	}

	if cfg.Tags != nil {
		g.FreeformTags = copyTags(cfg.Tags)
	}

	if cfg.Scope.Compartment != "" {
		g.CompartmentID = cfg.Scope.Compartment
	}

	g.TimeLastModified = m.now()

	info := m.toLogGroupInfo(g)

	return &info, nil
}

// DeleteLogGroup deletes a log group by display name.
func (m *Mock) DeleteLogGroup(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, ok := m.groupByName(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "log group %q not found", name)
	}

	for _, rec := range m.logsIn(g.ID) {
		m.logs.Delete(rec.log.ID)
	}

	m.groups.Delete(g.ID)

	return nil
}

// GetLogGroup returns a log group by display name.
func (m *Mock) GetLogGroup(_ context.Context, name string) (*driver.LogGroupInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	g, ok := m.groupByName(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "log group %q not found", name)
	}

	info := m.toLogGroupInfo(g)

	return &info, nil
}

// ListLogGroups lists the log groups visible under a compartment filter.
func (m *Mock) ListLogGroups(_ context.Context, filter scope.Scope) ([]driver.LogGroupInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]driver.LogGroupInfo, 0, m.groups.Len())

	for _, g := range m.groups.SortedValues() {
		if !(scope.Scope{Compartment: g.CompartmentID}).Matches(filter) {
			continue
		}

		out = append(out, m.toLogGroupInfo(g))
	}

	return out, nil
}

// CreateLogStream creates an enabled CUSTOM log inside a log group.
func (m *Mock) CreateLogStream(_ context.Context, logGroup, streamName string) (*driver.LogStreamInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, ok := m.groupByName(logGroup)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "log group %q not found", logGroup)
	}

	l, err := m.createLog(g.ID, LogSpec{
		DisplayName: streamName,
		LogType:     LogTypeCustom,
		IsEnabled:   true,
	})
	if err != nil {
		return nil, err
	}

	return &driver.LogStreamInfo{Name: l.DisplayName, CreatedAt: l.TimeCreated}, nil
}

// DeleteLogStream deletes a log from a log group.
func (m *Mock) DeleteLogStream(_ context.Context, logGroup, streamName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, err := m.portableLog(logGroup, streamName)
	if err != nil {
		return err
	}

	m.logs.Delete(rec.log.ID)

	return nil
}

// ListLogStreams lists the logs in a log group.
func (m *Mock) ListLogStreams(_ context.Context, logGroup string) ([]driver.LogStreamInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	g, ok := m.groupByName(logGroup)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "log group %q not found", logGroup)
	}

	recs := m.logsIn(g.ID)
	out := make([]driver.LogStreamInfo, 0, len(recs))

	for _, rec := range recs {
		out = append(out, toStreamInfo(rec))
	}

	return out, nil
}

// PutLogEvents ingests log events into a log, the portable spelling of PutLogs.
func (m *Mock) PutLogEvents(ctx context.Context, logGroup, streamName string, events []driver.LogEvent) error {
	m.mu.Lock()

	rec, err := m.portableLog(logGroup, streamName)
	if err != nil {
		m.mu.Unlock()
		return err
	}

	batch := LogEntryBatch{Entries: make([]LogEntryItem, 0, len(events)), Type: "com.oraclecloud.logging.custom"}
	for _, e := range events {
		batch.Entries = append(batch.Entries, LogEntryItem{Data: e.Message, Time: e.Timestamp})
	}

	count, bytes := m.ingest(rec, []LogEntryBatch{batch})
	compartmentID, groupID, logID := rec.log.CompartmentID, rec.log.LogGroupID, rec.log.ID
	mon := m.monitoring
	m.mu.Unlock()

	dims := map[string]string{"logId": logID, "logGroupId": groupID, "compartmentId": compartmentID}
	m.emitMetric(ctx, mon, "IngestedLogEntries", float64(count), dims)
	m.emitMetric(ctx, mon, "IngestedLogBytes", float64(bytes), dims)

	return nil
}

// GetLogEvents reads log events out of a group, optionally from one log.
func (m *Mock) GetLogEvents(_ context.Context, input *driver.LogQueryInput) ([]driver.LogEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	recs, err := m.portableSelection(input.LogGroup, input.LogStream)
	if err != nil {
		return nil, err
	}

	limit := input.Limit
	if limit <= 0 {
		limit = defaultLogLimit
	}

	out := make([]driver.LogEvent, 0, limit)

	for _, rec := range recs {
		for i := range rec.entries {
			e := &rec.entries[i]
			if !inWindow(e, input.StartTime, input.EndTime) || !containsPattern(e.Data, input.Pattern) {
				continue
			}

			out = append(out, driver.LogEvent{Timestamp: e.Time, Message: e.Data})
		}
	}

	if len(out) > limit {
		out = out[:limit]
	}

	return out, nil
}

// FilterLogEvents reads log events across a group's logs, reporting which log
// each came from.
func (m *Mock) FilterLogEvents(
	_ context.Context, input *driver.FilterLogEventsInput,
) ([]driver.FilteredLogEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	recs, err := m.portableSelection(input.LogGroup, input.LogStream)
	if err != nil {
		return nil, err
	}

	limit := input.Limit
	if limit <= 0 {
		limit = defaultLogLimit
	}

	out := make([]driver.FilteredLogEvent, 0, limit)

	for _, rec := range recs {
		for i := range rec.entries {
			e := &rec.entries[i]
			if !inWindow(e, input.StartTime, input.EndTime) || !containsPattern(e.Data, input.FilterPattern) {
				continue
			}

			out = append(out, driver.FilteredLogEvent{
				LogStream: rec.log.DisplayName,
				Timestamp: e.Time,
				Message:   e.Data,
			})
		}
	}

	if len(out) > limit {
		out = out[:limit]
	}

	return out, nil
}

// PutMetricFilter is not an OCI Logging operation.
func (*Mock) PutMetricFilter(_ context.Context, _ *driver.MetricFilterConfig) error {
	return unsupported("PutMetricFilter")
}

// DeleteMetricFilter is not an OCI Logging operation.
func (*Mock) DeleteMetricFilter(_ context.Context, _, _ string) error {
	return unsupported("DeleteMetricFilter")
}

// DescribeMetricFilters is not an OCI Logging operation.
func (*Mock) DescribeMetricFilters(_ context.Context, _ string) ([]driver.MetricFilterInfo, error) {
	return nil, unsupported("DescribeMetricFilters")
}

// unsupported reports an operation OCI Logging has no equivalent for.
func unsupported(operation string) error {
	return cerrors.Newf(cerrors.Unimplemented, "%s is not an OCI Logging operation: %s",
		operation, viaServiceConnector)
}

// portableLog resolves a log by group and log display name. The caller holds mu.
func (m *Mock) portableLog(logGroup, streamName string) (*logRecord, error) {
	g, ok := m.groupByName(logGroup)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "log group %q not found", logGroup)
	}

	rec, ok := m.logByName(g.ID, streamName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "log %q not found in log group %q", streamName, logGroup)
	}

	return rec, nil
}

// portableSelection resolves the logs a read covers: one named log, or every
// log in the group. The caller holds mu.
func (m *Mock) portableSelection(logGroup, streamName string) ([]*logRecord, error) {
	if streamName != "" {
		rec, err := m.portableLog(logGroup, streamName)
		if err != nil {
			return nil, err
		}

		return []*logRecord{rec}, nil
	}

	g, ok := m.groupByName(logGroup)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "log group %q not found", logGroup)
	}

	return m.logsIn(g.ID), nil
}

// toLogGroupInfo projects a log group onto the portable shape. The caller
// holds mu.
func (m *Mock) toLogGroupInfo(g *LogGroup) driver.LogGroupInfo {
	return driver.LogGroupInfo{
		Name:          g.DisplayName,
		ResourceID:    g.ID,
		RetentionDays: g.RetentionDays,
		CreatedAt:     g.TimeCreated,
		StoredBytes:   m.storedBytes(g.ID),
		Tags:          copyTags(g.FreeformTags),
		Scope:         scope.Scope{Compartment: g.CompartmentID},
	}
}

// toStreamInfo projects a log onto the portable stream shape. The caller holds mu.
func toStreamInfo(rec *logRecord) driver.LogStreamInfo {
	info := driver.LogStreamInfo{Name: rec.log.DisplayName, CreatedAt: rec.log.TimeCreated}
	if n := len(rec.entries); n > 0 {
		info.LastEvent = rec.entries[n-1].Time.UTC().Format(timeFormat)
	}

	return info
}

// inWindow reports whether an entry falls in the caller's time range. A zero
// bound is open.
func inWindow(e *LogEntry, start, end time.Time) bool {
	if !start.IsZero() && e.Time.Before(start) {
		return false
	}

	if !end.IsZero() && e.Time.After(end) {
		return false
	}

	return true
}

// containsPattern reports whether a payload carries the caller's substring.
func containsPattern(data, pattern string) bool {
	return pattern == "" || strings.Contains(data, pattern)
}
