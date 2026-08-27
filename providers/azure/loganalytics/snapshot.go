package loganalytics

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// logAnalyticsSnapshot is the full serialized state of the Log Analytics mock:
// every workspace (log group) keyed by name, each carrying its info, its log
// streams (with their events), and its metric/subscription filters. logGroup and
// logStream are unexported, so they are promoted to exported forms; the filter
// stores hold exported driver values and round-trip through the generic memstore
// helper. Each logStream's mutex and the wired options/monitoring are
// intentionally not serialized.
type logAnalyticsSnapshot struct {
	Groups map[string]*logGroupSnapshot `json:"groups,omitempty"`
}

// logGroupSnapshot is the exported form of logGroup.
type logGroupSnapshot struct {
	Info          driver.LogGroupInfo           `json:"info"`
	Streams       map[string]*logStreamSnapshot `json:"streams,omitempty"`
	MetricFilters json.RawMessage               `json:"metricFilters,omitempty"`
	SubFilters    json.RawMessage               `json:"subFilters,omitempty"`
}

// logStreamSnapshot is the exported form of logStream; its mutex is excluded.
type logStreamSnapshot struct {
	Info   driver.LogStreamInfo `json:"info"`
	Events []driver.LogEvent    `json:"events,omitempty"`
}

// Snapshot captures every workspace's full state as JSON. includeAssets is unused
// — the log events are the resource, so they are always captured.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := logAnalyticsSnapshot{Groups: make(map[string]*logGroupSnapshot, m.groups.Len())}

	for name, g := range m.groups.All() {
		gs, err := snapshotGroup(g)
		if err != nil {
			return nil, err
		}

		snap.Groups[name] = gs
	}

	return json.Marshal(snap)
}

func snapshotGroup(g *logGroup) (*logGroupSnapshot, error) {
	gs := &logGroupSnapshot{
		Info:    g.info,
		Streams: make(map[string]*logStreamSnapshot, g.streams.Len()),
	}

	for name, s := range g.streams.All() {
		s.mu.RLock()
		gs.Streams[name] = &logStreamSnapshot{Info: s.info, Events: append([]driver.LogEvent(nil), s.events...)}
		s.mu.RUnlock()
	}

	mf, err := g.metricFilters.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("loganalytics: snapshot metric filters: %w", err)
	}

	sf, err := g.subFilters.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("loganalytics: snapshot subscription filters: %w", err)
	}

	gs.MetricFilters = mf
	gs.SubFilters = sf

	return gs, nil
}

// Restore rebuilds every workspace under its original name with its streams,
// events, and filters intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap logAnalyticsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("loganalytics: parse snapshot: %w", err)
	}

	for name, gs := range snap.Groups {
		g, err := restoreGroup(gs)
		if err != nil {
			return err
		}

		m.groups.Set(name, g)
	}

	return nil
}

func restoreGroup(gs *logGroupSnapshot) (*logGroup, error) {
	g := &logGroup{
		info:          gs.Info,
		streams:       memstore.New[*logStream](),
		metricFilters: memstore.New[*driver.MetricFilterInfo](),
		subFilters:    memstore.New[*driver.SubscriptionFilterInfo](),
	}

	for name, ss := range gs.Streams {
		g.streams.Set(name, &logStream{info: ss.Info, events: ss.Events})
	}

	if len(gs.MetricFilters) > 0 {
		if err := g.metricFilters.LoadSnapshot(gs.MetricFilters); err != nil {
			return nil, fmt.Errorf("loganalytics: restore metric filters: %w", err)
		}
	}

	if len(gs.SubFilters) > 0 {
		if err := g.subFilters.LoadSnapshot(gs.SubFilters); err != nil {
			return nil, fmt.Errorf("loganalytics: restore subscription filters: %w", err)
		}
	}

	return g, nil
}
