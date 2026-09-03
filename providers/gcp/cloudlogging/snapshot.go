package cloudlogging

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// cloudloggingSnapshot is the full serialized state of the GCP Cloud Logging
// mock: every log group (with its streams, metric filters, and subscription
// filters), plus the GCP-native export sinks, log-based metrics, and buckets.
// The stored *logGroup/*logStream have unexported layouts, so they are
// promoted to exported snapshot forms; sinks, metrics, and buckets hold
// fully-exported driver values and round-trip through the generic memstore
// helper. The wired *config.Options and monitoring backend are intentionally
// not serialized.
type cloudloggingSnapshot struct {
	Groups  map[string]*logGroupSnapshot `json:"groups,omitempty"`
	Sinks   json.RawMessage              `json:"sinks,omitempty"`
	Metrics json.RawMessage              `json:"metrics,omitempty"`
	Buckets json.RawMessage              `json:"buckets,omitempty"`
}

// logGroupSnapshot mirrors logGroup, promoting its nested stream store and the
// (exported-value) filter stores to dumps.
type logGroupSnapshot struct {
	Info          driver.LogGroupInfo           `json:"info"`
	Streams       map[string]*logStreamSnapshot `json:"streams,omitempty"`
	MetricFilters json.RawMessage               `json:"metricFilters,omitempty"`
	SubFilters    json.RawMessage               `json:"subFilters,omitempty"`
}

// logStreamSnapshot mirrors logStream, promoting its unexported info/events to
// exported fields (the mutex is excluded).
type logStreamSnapshot struct {
	Info   driver.LogStreamInfo `json:"info"`
	Events []driver.LogEvent    `json:"events,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Cloud Logging holds no bulk object bodies beyond its log events, which are
// always captured.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := cloudloggingSnapshot{Groups: make(map[string]*logGroupSnapshot, m.groups.Len())}

	for name, g := range m.groups.All() {
		gs, err := snapshotGroup(g)
		if err != nil {
			return nil, err
		}

		snap.Groups[name] = gs
	}

	if err := dumpInto(&snap.Sinks, m.sinks.Snapshot); err != nil {
		return nil, err
	}

	if err := dumpInto(&snap.Metrics, m.metrics.Snapshot); err != nil {
		return nil, err
	}

	if err := dumpInto(&snap.Buckets, m.buckets.Snapshot); err != nil {
		return nil, err
	}

	return json.Marshal(snap)
}

func snapshotGroup(g *logGroup) (*logGroupSnapshot, error) {
	gs := &logGroupSnapshot{
		Info:    g.info,
		Streams: make(map[string]*logStreamSnapshot, g.streams.Len()),
	}

	for id, s := range g.streams.All() {
		s.mu.RLock()
		gs.Streams[id] = &logStreamSnapshot{Info: s.info, Events: append([]driver.LogEvent(nil), s.events...)}
		s.mu.RUnlock()
	}

	if err := dumpInto(&gs.MetricFilters, g.metricFilters.Snapshot); err != nil {
		return nil, err
	}

	if err := dumpInto(&gs.SubFilters, g.subFilters.Snapshot); err != nil {
		return nil, err
	}

	return gs, nil
}

func dumpInto(dst *json.RawMessage, fn func() ([]byte, error)) error {
	b, err := fn()
	if err != nil {
		return fmt.Errorf("cloudlogging: snapshot store: %w", err)
	}

	*dst = b

	return nil
}

// Restore rebuilds every log group (and its streams/filters) and the sinks and
// metrics under their original identities.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap cloudloggingSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("cloudlogging: parse snapshot: %w", err)
	}

	for name, gs := range snap.Groups {
		g, err := restoreGroup(gs)
		if err != nil {
			return err
		}

		m.groups.Set(name, g)
	}

	if err := loadInto(snap.Sinks, m.sinks.LoadSnapshot); err != nil {
		return err
	}

	if err := loadInto(snap.Metrics, m.metrics.LoadSnapshot); err != nil {
		return err
	}

	return loadInto(snap.Buckets, m.buckets.LoadSnapshot)
}

func restoreGroup(gs *logGroupSnapshot) (*logGroup, error) {
	g := &logGroup{
		info:          gs.Info,
		streams:       memstore.New[*logStream](),
		metricFilters: memstore.New[*driver.MetricFilterInfo](),
		subFilters:    memstore.New[*driver.SubscriptionFilterInfo](),
	}

	for id, ss := range gs.Streams {
		g.streams.Set(id, &logStream{info: ss.Info, events: ss.Events})
	}

	if err := loadInto(gs.MetricFilters, g.metricFilters.LoadSnapshot); err != nil {
		return nil, err
	}

	if err := loadInto(gs.SubFilters, g.subFilters.LoadSnapshot); err != nil {
		return nil, err
	}

	return g, nil
}

func loadInto(src json.RawMessage, fn func([]byte) error) error {
	if len(src) == 0 {
		return nil
	}

	if err := fn(src); err != nil {
		return fmt.Errorf("cloudlogging: restore store: %w", err)
	}

	return nil
}
