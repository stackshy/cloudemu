// Package cloudwatchlogs provides an in-memory mock implementation of AWS CloudWatch Logs.
package cloudwatchlogs

import (
	"context"
	"maps"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const defaultLogLimit = 100

// Compile-time checks that Mock implements driver.Logging and the
// CloudWatch Logs-only driver.AWSLogging surface.
var (
	_ driver.Logging    = (*Mock)(nil)
	_ driver.AWSLogging = (*Mock)(nil)
)

type logStream struct {
	info   driver.LogStreamInfo
	events []driver.LogEvent
	// uploads counts accepted PutLogEvents calls; it seeds the (deprecated,
	// ignored-on-input) nextSequenceToken so the token changes per upload.
	uploads uint64
	mu      sync.RWMutex
}

type logGroup struct {
	info          driver.LogGroupInfo
	kmsKeyID      string
	class         string
	streams       *memstore.Store[*logStream]
	metricFilters *memstore.Store[*driver.MetricFilterInfo]
	subFilters    *memstore.Store[*driver.SubscriptionFilterInfo]
}

// LambdaInvoker asynchronously invokes a Lambda function by ARN with a
// CloudWatch Logs subscription-event payload. The Lambda mock satisfies this,
// enabling real subscription-filter delivery to Lambda destinations.
type LambdaInvoker interface {
	InvokeExternal(ctx context.Context, functionARN string, payload []byte) error
}

// Mock is an in-memory mock implementation of the AWS CloudWatch Logs service.
type Mock struct {
	groups     *memstore.Store[*logGroup]
	opts       *config.Options
	monitoring mondriver.Monitoring
	lambda     LambdaInvoker
	keys       KeyResolver

	// queryResultKmsKey is the account-wide key associated with Logs Insights
	// query results (AssociateKmsKey on the query-result resource).
	queryResultKmsKey string
	mu                sync.RWMutex
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// SetLambdaInvoker wires the Lambda backend so subscription filters with a
// Lambda destination invoke the mapped function on matching PutLogEvents.
func (m *Mock) SetLambdaInvoker(i LambdaInvoker) {
	m.lambda = i
}

func (m *Mock) emitMetric(metricName string, value float64, unit string, dims map[string]string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: "AWS/Logs", MetricName: metricName, Value: value, Unit: unit,
		Dimensions: dims, Timestamp: m.opts.Clock.Now(),
	}})
}

// New creates a new CloudWatch Logs mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		groups: memstore.New[*logGroup](),
		opts:   opts,
	}
}

// CreateLogGroup creates a new CloudWatch log group in the STANDARD class
// with no KMS key.
func (m *Mock) CreateLogGroup(ctx context.Context, cfg driver.LogGroupConfig) (*driver.LogGroupInfo, error) {
	g, err := m.CreateAWSLogGroup(ctx, &cfg, driver.AWSLogGroupOptions{})
	if err != nil {
		return nil, err
	}

	return &g.LogGroupInfo, nil
}

// DeleteLogGroup deletes a CloudWatch log group by name.
func (m *Mock) DeleteLogGroup(_ context.Context, name string) error {
	if !m.groups.Delete(name) {
		return errors.Newf(errors.NotFound, "log group %q not found", name)
	}

	return nil
}

// GetLogGroup retrieves information about a CloudWatch log group.
func (m *Mock) GetLogGroup(_ context.Context, name string) (*driver.LogGroupInfo, error) {
	g, ok := m.groups.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "log group %q not found", name)
	}

	result := g.info

	return &result, nil
}

// ListLogGroups lists all CloudWatch log groups.
func (m *Mock) ListLogGroups(_ context.Context, filter scope.Scope) ([]driver.LogGroupInfo, error) {
	all := m.groups.SortedValues()

	groups := make([]driver.LogGroupInfo, 0, len(all))
	for _, g := range all {
		if !g.info.Scope.Matches(filter) {
			continue
		}
		groups = append(groups, g.info)
	}

	return groups, nil
}

// CreateLogStream creates a new log stream in a log group.
func (m *Mock) CreateLogStream(_ context.Context, logGroup, streamName string) (*driver.LogStreamInfo, error) {
	g, ok := m.groups.Get(logGroup)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "log group %q not found", logGroup)
	}

	if err := validateLogStreamName(streamName); err != nil {
		return nil, err
	}

	if g.streams.Has(streamName) {
		return nil, errors.Newf(errors.AlreadyExists, "log stream %q already exists in group %q", streamName, logGroup)
	}

	info := driver.LogStreamInfo{
		Name:      streamName,
		CreatedAt: m.opts.Clock.Now().UTC().Format(time.RFC3339),
	}

	s := &logStream{
		info:   info,
		events: make([]driver.LogEvent, 0),
	}

	g.streams.Set(streamName, s)

	result := info

	return &result, nil
}

// DeleteLogStream deletes a log stream from a log group.
func (m *Mock) DeleteLogStream(_ context.Context, logGroup, streamName string) error {
	g, ok := m.groups.Get(logGroup)
	if !ok {
		return errors.Newf(errors.NotFound, "log group %q not found", logGroup)
	}

	if !g.streams.Delete(streamName) {
		return errors.Newf(errors.NotFound, "log stream %q not found in group %q", streamName, logGroup)
	}

	return nil
}

// ListLogStreams lists all log streams in a log group.
func (m *Mock) ListLogStreams(_ context.Context, logGroup string) ([]driver.LogStreamInfo, error) {
	g, ok := m.groups.Get(logGroup)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "log group %q not found", logGroup)
	}

	all := g.streams.SortedValues()

	streams := make([]driver.LogStreamInfo, 0, len(all))

	for _, s := range all {
		s.mu.RLock()
		streams = append(streams, s.info)
		s.mu.RUnlock()
	}

	return streams, nil
}

// PutLogEvents writes log events to a stream. Events outside the ingest
// window are dropped exactly as PutLogEventsWithResult drops them; the
// rejection details are discarded.
func (m *Mock) PutLogEvents(ctx context.Context, groupName, streamName string, events []driver.LogEvent) error {
	_, err := m.PutLogEventsWithResult(ctx, groupName, streamName, events)

	return err
}

// evaluateMetricFilters turns matching log events into CloudWatch metric data.
// For each metric filter on the group, every event whose message matches the
// filter pattern contributes the filter's metric value; the total is published
// to the filter's configured namespace/metric, mirroring how CloudWatch Logs
// extracts custom metrics from ingested events.
//
// Pattern matching only supports plain-substring "count occurrences of a
// term" matching (strings.Contains). Real CloudWatch Logs filter pattern
// syntax — quoted phrases, JSON-field patterns (e.g. { $.level = "ERROR" }),
// ?OR-style alternation, and -exclusion terms — is not parsed, so a more
// elaborate real filter pattern will not behave like real CloudWatch Logs
// here.
func (m *Mock) evaluateMetricFilters(g *logGroup, events []driver.LogEvent) {
	if m.monitoring == nil {
		return
	}

	for _, mf := range g.metricFilters.All() {
		var (
			matched int
			total   float64
		)

		for i := range events {
			if mf.FilterPattern == "" || strings.Contains(events[i].Message, mf.FilterPattern) {
				matched++
				total += metricFilterValue(mf.MetricValue)
			}
		}

		if matched == 0 {
			continue
		}

		namespace := mf.MetricNamespace
		if namespace == "" {
			namespace = "LogMetrics"
		}

		_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{{
			Namespace:  namespace,
			MetricName: mf.MetricName,
			Value:      total,
			Timestamp:  m.opts.Clock.Now(),
		}})
	}
}

// metricFilterValue resolves a metric filter's metricValue to the number each
// matching event contributes. A literal number is used directly; anything else
// (an empty value, or a "$field" token this emulator does not extract) counts
// as 1 per matching event, which is the common "count occurrences" filter.
func metricFilterValue(raw string) float64 {
	if raw == "" {
		return 1
	}

	if v, err := strconv.ParseFloat(raw, 64); err == nil {
		return v
	}

	return 1
}

// GetLogEvents retrieves log events matching the query.
func (m *Mock) GetLogEvents(_ context.Context, input *driver.LogQueryInput) ([]driver.LogEvent, error) {
	g, ok := m.groups.Get(input.LogGroup)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "log group %q not found", input.LogGroup)
	}

	m.purgeExpired(input.LogGroup, g)

	limit := input.Limit
	if limit == 0 {
		limit = defaultLogLimit
	}

	var results []driver.LogEvent

	if input.LogStream != "" {
		s, ok := g.streams.Get(input.LogStream)
		if !ok {
			return nil, errors.Newf(errors.NotFound, "log stream %q not found in group %q", input.LogStream, input.LogGroup)
		}

		results = append(results, m.filterEvents(s, input)...)
	} else {
		for _, s := range g.streams.SortedValues() {
			events := m.filterEvents(s, input)
			results = append(results, events...)
		}

		sortLogEventsAscending(results)
	}

	// A negative limit means "no cap" — the wire handler fetches the full
	// ordered set and paginates it itself.
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	if results == nil {
		results = []driver.LogEvent{}
	}

	return results, nil
}

// sortLogEventsAscending orders log events by timestamp ascending, keeping the
// relative order of equal-timestamp events (their ingestion order) stable —
// matching how CloudWatch Logs returns events in timestamp order.
func sortLogEventsAscending(events []driver.LogEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Timestamp.Before(events[j].Timestamp)
	})
}

func (*Mock) filterEvents(
	s *logStream,
	input *driver.LogQueryInput,
) []driver.LogEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []driver.LogEvent

	for _, e := range s.events {
		if !input.StartTime.IsZero() && e.Timestamp.Before(input.StartTime) {
			continue
		}

		if !input.EndTime.IsZero() && e.Timestamp.After(input.EndTime) {
			continue
		}

		if input.Pattern != "" && !strings.Contains(e.Message, input.Pattern) {
			continue
		}

		results = append(results, e)
	}

	return results
}

// FilterLogEvents filters log events across streams using a pattern.
func (m *Mock) FilterLogEvents(
	_ context.Context,
	input *driver.FilterLogEventsInput,
) ([]driver.FilteredLogEvent, error) {
	g, ok := m.groups.Get(input.LogGroup)
	if !ok {
		return nil, errors.Newf(
			errors.NotFound, "log group %q not found", input.LogGroup,
		)
	}

	m.purgeExpired(input.LogGroup, g)

	limit := input.Limit
	if limit == 0 {
		limit = defaultLogLimit
	}

	var results []driver.FilteredLogEvent

	if input.LogStream != "" {
		results = m.filterStreamEvents(g, input.LogStream, input)
	} else {
		for name, s := range g.streams.All() {
			results = append(results, m.matchEvents(s, name, input)...)
		}

		// CloudWatch Logs interleaves matches across streams in ascending
		// timestamp order; sort by (timestamp, stream) so the result is both
		// time-ordered and deterministic run-to-run regardless of the random
		// map-iteration order above. Ties break on stream name.
		sort.SliceStable(results, func(i, j int) bool {
			if !results[i].Timestamp.Equal(results[j].Timestamp) {
				return results[i].Timestamp.Before(results[j].Timestamp)
			}

			return results[i].LogStream < results[j].LogStream
		})
	}

	// A negative limit means "no cap" — the wire handler fetches the full
	// ordered set and paginates it itself.
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	if results == nil {
		results = []driver.FilteredLogEvent{}
	}

	return results, nil
}

func (m *Mock) filterStreamEvents(
	g *logGroup,
	streamName string,
	input *driver.FilterLogEventsInput,
) []driver.FilteredLogEvent {
	s, ok := g.streams.Get(streamName)
	if !ok {
		return nil
	}

	return m.matchEvents(s, streamName, input)
}

func (*Mock) matchEvents(
	s *logStream,
	streamName string,
	input *driver.FilterLogEventsInput,
) []driver.FilteredLogEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []driver.FilteredLogEvent

	for _, e := range s.events {
		if !input.StartTime.IsZero() && e.Timestamp.Before(input.StartTime) {
			continue
		}

		if !input.EndTime.IsZero() && e.Timestamp.After(input.EndTime) {
			continue
		}

		if input.FilterPattern != "" &&
			!strings.Contains(e.Message, input.FilterPattern) {
			continue
		}

		results = append(results, driver.FilteredLogEvent{
			LogStream:     streamName,
			Timestamp:     e.Timestamp,
			IngestionTime: e.IngestionTime,
			Message:       e.Message,
		})
	}

	return results
}

// PutMetricFilter creates or updates a metric filter for a log group.
func (m *Mock) PutMetricFilter(
	_ context.Context,
	cfg *driver.MetricFilterConfig,
) error {
	g, ok := m.groups.Get(cfg.LogGroup)
	if !ok {
		return errors.Newf(
			errors.NotFound, "log group %q not found", cfg.LogGroup,
		)
	}

	if cfg.Name == "" {
		return errors.New(
			errors.InvalidArgument, "metric filter name is required",
		)
	}

	info := &driver.MetricFilterInfo{
		Name:            cfg.Name,
		LogGroup:        cfg.LogGroup,
		FilterPattern:   cfg.FilterPattern,
		MetricName:      cfg.MetricName,
		MetricNamespace: cfg.MetricNamespace,
		MetricValue:     cfg.MetricValue,
		DefaultValue:    cfg.DefaultValue,
		Unit:            cfg.Unit,
		Dimensions:      maps.Clone(cfg.Dimensions),
		CreatedAt:       m.opts.Clock.Now().UTC(),
	}

	g.metricFilters.Set(cfg.Name, info)

	return nil
}

// DeleteMetricFilter deletes a metric filter from a log group.
func (m *Mock) DeleteMetricFilter(
	_ context.Context,
	logGroup, filterName string,
) error {
	g, ok := m.groups.Get(logGroup)
	if !ok {
		return errors.Newf(
			errors.NotFound, "log group %q not found", logGroup,
		)
	}

	if !g.metricFilters.Delete(filterName) {
		return errors.Newf(
			errors.NotFound,
			"metric filter %q not found in group %q",
			filterName, logGroup,
		)
	}

	return nil
}

// DescribeMetricFilters lists all metric filters for a log group.
func (m *Mock) DescribeMetricFilters(
	_ context.Context,
	logGroup string,
) ([]driver.MetricFilterInfo, error) {
	g, ok := m.groups.Get(logGroup)
	if !ok {
		return nil, errors.Newf(
			errors.NotFound, "log group %q not found", logGroup,
		)
	}

	all := g.metricFilters.All()
	results := make([]driver.MetricFilterInfo, 0, len(all))

	for _, mf := range all {
		results = append(results, *mf)
	}

	return results, nil
}

// UpdateLogGroup replaces the mutable fields of an existing log group —
// ARM CreateOrUpdate-on-existing semantics (retention and tags come from
// the request; identity and CreatedAt are preserved).
func (m *Mock) UpdateLogGroup(_ context.Context, cfg driver.LogGroupConfig) (*driver.LogGroupInfo, error) {
	g, ok := m.groups.Get(cfg.Name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "log group %q not found", cfg.Name)
	}

	if cfg.RetentionDays != 0 {
		g.info.RetentionDays = cfg.RetentionDays
	}
	if cfg.Tags != nil {
		g.info.Tags = maps.Clone(cfg.Tags)
	}
	if !cfg.Scope.IsZero() {
		g.info.Scope = cfg.Scope
	}

	m.groups.Set(cfg.Name, g)

	result := g.info

	return &result, nil
}
