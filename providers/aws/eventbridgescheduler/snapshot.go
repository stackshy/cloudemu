package eventbridgescheduler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/eventbridgescheduler/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// schedulerSnapshot is the full serialized state of the Scheduler mock. The
// stores hold exported driver types, so they serialize directly, keyed by
// schedule key ("<group>/<name>") and group name. The wired opts are not
// serialized.
type schedulerSnapshot struct {
	Schedules map[string]driver.Schedule      `json:"schedules,omitempty"`
	Groups    map[string]driver.ScheduleGroup `json:"groups,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Scheduler is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := schedulerSnapshot{}

	if m.schedules.Len() > 0 {
		snap.Schedules = m.schedules.All()
	}

	if m.groups.Len() > 0 {
		snap.Groups = m.groups.All()
	}

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("scheduler: snapshot: %w", err)
	}

	return b, nil
}

// Restore rebuilds the mock's state under the original identities: every
// schedule and group keeps its name, group and derived arn, so a restore is
// transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap schedulerSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("scheduler: parse snapshot: %w", err)
	}

	for key := range snap.Schedules {
		m.schedules.Set(key, snap.Schedules[key])
	}

	for name := range snap.Groups {
		m.groups.Set(name, snap.Groups[name])
	}

	return nil
}
