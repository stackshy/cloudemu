package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// monitorSnapshot is the full serialized state of the Azure Monitor mock. The
// alarm, channel, and action-group stores hold value types whose fields are all
// exported, so they round-trip through the generic memstore helper. The metric
// buffer is keyed by a struct (metricKey) — which json cannot serialize as a map
// key — so it is promoted to a deterministically-ordered slice. The alarm-history
// and action-group-delivery logs are captured in order. The mutex, the wired
// webhook deliverer, and *config.Options are intentionally not captured.
type monitorSnapshot struct {
	Metrics      []metricEntrySnapshot      `json:"metrics,omitempty"`
	Alarms       json.RawMessage            `json:"alarms,omitempty"`
	Channels     json.RawMessage            `json:"channels,omitempty"`
	ActionGroups json.RawMessage            `json:"actionGroups,omitempty"`
	Deliveries   []ActionGroupDelivery      `json:"deliveries,omitempty"`
	History      []driver.AlarmHistoryEntry `json:"history,omitempty"`
}

// metricEntrySnapshot promotes one (metricKey -> datapoints) entry to an
// exported form. metricKey's fields are already exported, so it serializes as a
// value here even though it cannot serve as a JSON map key.
type metricEntrySnapshot struct {
	Key  metricKey            `json:"key"`
	Data []driver.MetricDatum `json:"data,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Azure Monitor holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap monitorSnapshot

	m.mu.RLock()
	snap.Metrics = snapshotMetrics(m.metrics)

	if len(m.history) > 0 {
		snap.History = make([]driver.AlarmHistoryEntry, len(m.history))
		copy(snap.History, m.history)
	}

	if len(m.deliveries) > 0 {
		snap.Deliveries = make([]ActionGroupDelivery, len(m.deliveries))
		copy(snap.Deliveries, m.deliveries)
	}
	m.mu.RUnlock()

	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	return json.Marshal(snap)
}

// snapshotMetrics promotes the struct-keyed metric buffer to a slice sorted by
// (namespace, metricName) so a snapshot->restore->snapshot round-trip is
// byte-stable despite Go's random map iteration order.
func snapshotMetrics(metrics map[metricKey][]driver.MetricDatum) []metricEntrySnapshot {
	if len(metrics) == 0 {
		return nil
	}

	out := make([]metricEntrySnapshot, 0, len(metrics))

	for k, data := range metrics {
		cp := make([]driver.MetricDatum, len(data))
		copy(cp, data)
		out = append(out, metricEntrySnapshot{Key: k, Data: cp})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Key.Namespace != out[j].Key.Namespace {
			return out[i].Key.Namespace < out[j].Key.Namespace
		}

		return out[i].Key.MetricName < out[j].Key.MetricName
	})

	return out
}

func (m *Mock) snapshotStores(snap *monitorSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Alarms, m.alarms.Snapshot},
		{&snap.Channels, m.channels.Snapshot},
		{&snap.ActionGroups, m.actionGroups.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("azure monitor: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities: alarms,
// channels, and action groups come back under their keys, and the metric buffer
// is reindexed under its original (namespace, metricName) keys.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap monitorSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("azure monitor: parse snapshot: %w", err)
	}

	m.mu.Lock()
	for _, e := range snap.Metrics {
		m.metrics[e.Key] = e.Data
	}

	if len(snap.History) > 0 {
		m.history = append(m.history, snap.History...)
	}

	if len(snap.Deliveries) > 0 {
		m.deliveries = append(m.deliveries, snap.Deliveries...)
	}
	m.mu.Unlock()

	return m.restoreStores(&snap)
}

func (m *Mock) restoreStores(snap *monitorSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Alarms, m.alarms.LoadSnapshot},
		{snap.Channels, m.channels.LoadSnapshot},
		{snap.ActionGroups, m.actionGroups.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("azure monitor: restore store: %w", err)
		}
	}

	return nil
}
