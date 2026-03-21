// Package cloudlogging provides an in-memory mock implementation of GCP Cloud Logging.
package cloudlogging

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	"github.com/stackshy/cloudemu/logging/driver"
)

const (
	defaultRetentionDays = 30
	defaultLogLimit      = 100
)

// Compile-time check that Mock implements driver.Logging.
var _ driver.Logging = (*Mock)(nil)

type logStream struct {
	info   driver.LogStreamInfo
	events []driver.LogEvent
	mu     sync.RWMutex
}

type logGroup struct {
	info    driver.LogGroupInfo
	streams *memstore.Store[*logStream]
}

// Mock is an in-memory mock implementation of GCP Cloud Logging.
type Mock struct {
	groups *memstore.Store[*logGroup]
	opts   *config.Options
}

// New creates a new Cloud Logging mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		groups: memstore.New[*logGroup](),
		opts:   opts,
	}
}

// CreateLogGroup creates a new Cloud Logging log bucket.
func (m *Mock) CreateLogGroup(_ context.Context, cfg driver.LogGroupConfig) (*driver.LogGroupInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "log group name is required")
	}

	if m.groups.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "log group %q already exists", cfg.Name)
	}

	retentionDays := cfg.RetentionDays
	if retentionDays == 0 {
		retentionDays = defaultRetentionDays
	}

	selfLink := idgen.GCPID(m.opts.ProjectID, "logs", cfg.Name)

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	info := driver.LogGroupInfo{
		Name:          cfg.Name,
		ResourceID:    selfLink,
		RetentionDays: retentionDays,
		CreatedAt:     m.opts.Clock.Now().UTC().Format(time.RFC3339),
		StoredBytes:   0,
		Tags:          tags,
	}

	g := &logGroup{
		info:    info,
		streams: memstore.New[*logStream](),
	}

	m.groups.Set(cfg.Name, g)

	result := info

	return &result, nil
}

// DeleteLogGroup deletes a Cloud Logging log bucket by name.
func (m *Mock) DeleteLogGroup(_ context.Context, name string) error {
	if !m.groups.Delete(name) {
		return errors.Newf(errors.NotFound, "log group %q not found", name)
	}

	return nil
}

// GetLogGroup retrieves information about a Cloud Logging log bucket.
func (m *Mock) GetLogGroup(_ context.Context, name string) (*driver.LogGroupInfo, error) {
	g, ok := m.groups.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "log group %q not found", name)
	}

	result := g.info

	return &result, nil
}

// ListLogGroups lists all Cloud Logging log buckets.
func (m *Mock) ListLogGroups(_ context.Context) ([]driver.LogGroupInfo, error) {
	all := m.groups.All()

	groups := make([]driver.LogGroupInfo, 0, len(all))
	for _, g := range all {
		groups = append(groups, g.info)
	}

	return groups, nil
}

// CreateLogStream creates a new log stream in a log bucket.
func (m *Mock) CreateLogStream(_ context.Context, logGroup, streamName string) (*driver.LogStreamInfo, error) {
	g, ok := m.groups.Get(logGroup)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "log group %q not found", logGroup)
	}

	if streamName == "" {
		return nil, errors.New(errors.InvalidArgument, "stream name is required")
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

// DeleteLogStream deletes a log stream from a log bucket.
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

// ListLogStreams lists all log streams in a log bucket.
func (m *Mock) ListLogStreams(_ context.Context, logGroup string) ([]driver.LogStreamInfo, error) {
	g, ok := m.groups.Get(logGroup)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "log group %q not found", logGroup)
	}

	all := g.streams.All()

	streams := make([]driver.LogStreamInfo, 0, len(all))

	for _, s := range all {
		s.mu.RLock()
		streams = append(streams, s.info)
		s.mu.RUnlock()
	}

	return streams, nil
}

// PutLogEvents writes log events to a stream.
func (m *Mock) PutLogEvents(_ context.Context, groupName, streamName string, events []driver.LogEvent) error {
	g, ok := m.groups.Get(groupName)
	if !ok {
		return errors.Newf(errors.NotFound, "log group %q not found", groupName)
	}

	s, ok := g.streams.Get(streamName)
	if !ok {
		return errors.Newf(errors.NotFound, "log stream %q not found in group %q", streamName, groupName)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	copied := make([]driver.LogEvent, len(events))
	copy(copied, events)

	s.events = append(s.events, copied...)

	if len(events) > 0 {
		s.info.LastEvent = events[len(events)-1].Timestamp.UTC().Format(time.RFC3339)
	}

	var totalBytes int64
	for _, e := range events {
		totalBytes += int64(len(e.Message))
	}

	m.groups.Update(groupName, func(lg *logGroup) *logGroup {
		lg.info.StoredBytes += totalBytes
		return lg
	})

	return nil
}

// GetLogEvents retrieves log events matching the query.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) GetLogEvents(_ context.Context, input driver.LogQueryInput) ([]driver.LogEvent, error) {
	g, ok := m.groups.Get(input.LogGroup)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "log group %q not found", input.LogGroup)
	}

	limit := input.Limit
	if limit <= 0 {
		limit = defaultLogLimit
	}

	retentionCutoff := m.opts.Clock.Now().AddDate(0, 0, -g.info.RetentionDays)

	var results []driver.LogEvent

	if input.LogStream != "" {
		events := m.getStreamEvents(g, input.LogStream, &input, retentionCutoff)
		results = append(results, events...)
	} else {
		all := g.streams.All()
		for _, s := range all {
			events := m.filterEvents(s, &input, retentionCutoff)
			results = append(results, events...)
		}
	}

	if len(results) > limit {
		results = results[:limit]
	}

	if results == nil {
		results = []driver.LogEvent{}
	}

	return results, nil
}

func (m *Mock) getStreamEvents(g *logGroup, streamName string, input *driver.LogQueryInput, retentionCutoff time.Time) []driver.LogEvent {
	s, ok := g.streams.Get(streamName)
	if !ok {
		return nil
	}

	return m.filterEvents(s, input, retentionCutoff)
}

func (*Mock) filterEvents(s *logStream, input *driver.LogQueryInput, retentionCutoff time.Time) []driver.LogEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []driver.LogEvent

	for _, e := range s.events {
		if !retentionCutoff.IsZero() && e.Timestamp.Before(retentionCutoff) {
			continue
		}

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
