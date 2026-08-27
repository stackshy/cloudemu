package cloudwatchlogs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// logsSnapshot is the full serialized state of the CloudWatch Logs mock. The
// groups store holds an unexported *logGroup whose nested stores (streams, and
// the metric/subscription filter stores) must be captured, so it is promoted to
// an exported snapshot form keyed by group name. The wired deps (opts,
// monitoring, lambda) are not serialized.
type logsSnapshot struct {
	Groups map[string]*logGroupSnapshot `json:"groups,omitempty"`
}

// logGroupSnapshot mirrors logGroup. Its metric- and subscription-filter stores
// hold fully-exported driver pointer types, so they round-trip through the
// generic memstore helper; streams carries an exported form because logStream
// has an unexported mutex.
type logGroupSnapshot struct {
	Info          driver.LogGroupInfo           `json:"info"`
	Streams       map[string]*logStreamSnapshot `json:"streams,omitempty"`
	MetricFilters json.RawMessage               `json:"metricFilters,omitempty"`
	SubFilters    json.RawMessage               `json:"subFilters,omitempty"`
}

// logStreamSnapshot mirrors logStream, promoting its fields past the unexported
// mutex.
type logStreamSnapshot struct {
	Info   driver.LogStreamInfo `json:"info"`
	Events []driver.LogEvent    `json:"events,omitempty"`
}

// Snapshot captures every log group's state as JSON. includeAssets is unused —
// log events are the payload and are always captured.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := logsSnapshot{}
	if m.groups.Len() == 0 {
		return json.Marshal(snap)
	}

	snap.Groups = make(map[string]*logGroupSnapshot, m.groups.Len())

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
	gs := &logGroupSnapshot{Info: g.info}

	mf, err := g.metricFilters.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("cloudwatchlogs: snapshot metric filters: %w", err)
	}

	sf, err := g.subFilters.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("cloudwatchlogs: snapshot subscription filters: %w", err)
	}

	gs.MetricFilters = mf
	gs.SubFilters = sf

	if g.streams.Len() > 0 {
		gs.Streams = make(map[string]*logStreamSnapshot, g.streams.Len())

		for sName, s := range g.streams.All() {
			s.mu.RLock()
			gs.Streams[sName] = &logStreamSnapshot{Info: s.info, Events: append([]driver.LogEvent(nil), s.events...)}
			s.mu.RUnlock()
		}
	}

	return gs, nil
}

// Restore rebuilds every log group under its original name with its streams,
// events, and metric/subscription filters intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap logsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("cloudwatchlogs: parse snapshot: %w", err)
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

	if len(gs.MetricFilters) > 0 {
		if err := g.metricFilters.LoadSnapshot(gs.MetricFilters); err != nil {
			return nil, fmt.Errorf("cloudwatchlogs: restore metric filters: %w", err)
		}
	}

	if len(gs.SubFilters) > 0 {
		if err := g.subFilters.LoadSnapshot(gs.SubFilters); err != nil {
			return nil, fmt.Errorf("cloudwatchlogs: restore subscription filters: %w", err)
		}
	}

	for sName, ss := range gs.Streams {
		g.streams.Set(sName, &logStream{info: ss.Info, events: ss.Events})
	}

	return g, nil
}
