package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// monitoringSnapshot is the full serialized state of the OCI Monitoring mock.
// Series and alarms carry exported forms because metricSeries and alarmRecord
// have entirely unexported fields; notification channels are a fully-exported
// driver type and round-trip through the generic memstore helper. Each map is
// keyed by its store's key (series key / alarm OCID / channel OCID) so a restore
// reinstates every entry under the same identity. The mutex and *config.Options
// are intentionally not serialized.
type monitoringSnapshot struct {
	Series   map[string]*seriesSnapshot `json:"series,omitempty"`
	Alarms   map[string]*alarmSnapshot  `json:"alarms,omitempty"`
	Channels json.RawMessage            `json:"channels,omitempty"`
}

// seriesSnapshot mirrors metricSeries, promoting its unexported fields (and the
// unexported metricPoint samples) to exported ones so they survive JSON.
type seriesSnapshot struct {
	Place         scope.Scope       `json:"place,omitempty"`
	Namespace     string            `json:"namespace,omitempty"`
	ResourceGroup string            `json:"resourceGroup,omitempty"`
	Name          string            `json:"name,omitempty"`
	Dimensions    map[string]string `json:"dimensions,omitempty"`
	Points        []pointSnapshot   `json:"points,omitempty"`
}

// pointSnapshot is the exported form of metricPoint.
type pointSnapshot struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// alarmSnapshot mirrors alarmRecord, promoting its unexported fields to exported
// ones. OCIAlarmSpec and driver.AlarmHistoryEntry are fully exported, so the
// spec and the state-change history embed directly.
type alarmSnapshot struct {
	ID             string                     `json:"id"`
	Place          scope.Scope                `json:"place,omitempty"`
	Spec           OCIAlarmSpec               `json:"spec"`
	Status         string                     `json:"status,omitempty"`
	LifecycleState string                     `json:"lifecycleState,omitempty"`
	TimeCreated    time.Time                  `json:"timeCreated,omitempty"`
	TimeUpdated    time.Time                  `json:"timeUpdated,omitempty"`
	TimeTriggered  time.Time                  `json:"timeTriggered,omitempty"`
	BreachSince    time.Time                  `json:"breachSince,omitempty"`
	History        []driver.AlarmHistoryEntry `json:"history,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Monitoring holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	channels, err := m.channels.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("monitoring: snapshot channels: %w", err)
	}

	return json.Marshal(monitoringSnapshot{
		Series:   m.snapshotSeries(),
		Alarms:   m.snapshotAlarms(),
		Channels: channels,
	})
}

// snapshotSeries promotes each stored metric series to its exported form.
// Callers hold m.mu.
func (m *Mock) snapshotSeries() map[string]*seriesSnapshot {
	if m.series.Len() == 0 {
		return nil
	}

	out := make(map[string]*seriesSnapshot, m.series.Len())

	for key, s := range m.series.All() {
		points := make([]pointSnapshot, 0, len(s.points))
		for _, p := range s.points {
			points = append(points, pointSnapshot{Timestamp: p.timestamp, Value: p.value})
		}

		out[key] = &seriesSnapshot{
			Place:         s.place,
			Namespace:     s.namespace,
			ResourceGroup: s.resourceGroup,
			Name:          s.name,
			Dimensions:    copyTags(s.dimensions),
			Points:        points,
		}
	}

	return out
}

// snapshotAlarms promotes each stored alarm to its exported form. Callers hold
// m.mu.
func (m *Mock) snapshotAlarms() map[string]*alarmSnapshot {
	if m.alarms.Len() == 0 {
		return nil
	}

	out := make(map[string]*alarmSnapshot, m.alarms.Len())

	for id, a := range m.alarms.All() {
		out[id] = &alarmSnapshot{
			ID:             a.id,
			Place:          a.place,
			Spec:           a.spec,
			Status:         a.status,
			LifecycleState: a.lifecycleState,
			TimeCreated:    a.timeCreated,
			TimeUpdated:    a.timeUpdated,
			TimeTriggered:  a.timeTriggered,
			BreachSince:    a.breachSince,
			History:        append([]driver.AlarmHistoryEntry(nil), a.history...),
		}
	}

	return out
}

// Restore rebuilds the mock's state under the original identities: each series
// key, alarm OCID and channel OCID is preserved, so a restored alarm's history
// and a restored series' samples come back intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap monitoringSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("monitoring: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.restoreSeries(snap.Series)
	m.restoreAlarms(snap.Alarms)

	if len(snap.Channels) != 0 {
		if err := m.channels.LoadSnapshot(snap.Channels); err != nil {
			return fmt.Errorf("monitoring: restore channels: %w", err)
		}
	}

	return nil
}

// restoreSeries reinstates each metric series under its original key. Callers
// hold m.mu.
func (m *Mock) restoreSeries(series map[string]*seriesSnapshot) {
	for key, s := range series {
		points := make([]metricPoint, 0, len(s.Points))
		for _, p := range s.Points {
			points = append(points, metricPoint{timestamp: p.Timestamp, value: p.Value})
		}

		m.series.Set(key, &metricSeries{
			place:         s.Place,
			namespace:     s.Namespace,
			resourceGroup: s.ResourceGroup,
			name:          s.Name,
			dimensions:    copyTags(s.Dimensions),
			points:        points,
		})
	}
}

// restoreAlarms reinstates each alarm under its original OCID. Callers hold m.mu.
func (m *Mock) restoreAlarms(alarms map[string]*alarmSnapshot) {
	for id, a := range alarms {
		m.alarms.Set(id, &alarmRecord{
			id:             a.ID,
			place:          a.Place,
			spec:           a.Spec,
			status:         a.Status,
			lifecycleState: a.LifecycleState,
			timeCreated:    a.TimeCreated,
			timeUpdated:    a.TimeUpdated,
			timeTriggered:  a.TimeTriggered,
			breachSince:    a.BreachSince,
			history:        append([]driver.AlarmHistoryEntry(nil), a.History...),
		})
	}
}
