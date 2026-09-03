package ecs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// ecsSnapshot is the full serialized state of the ECS mock. Every store holds a
// fully-exported driver type (or []driver.Tag / string), so each round-trips
// through the generic memstore helper keyed by its resource id — ARNs and the
// "cluster/name", "family:revision" composite keys survive unchanged, so a
// restore is transparent to clients. The dynamic-host-port counter is preserved
// so newly placed bridge-mode ports do not collide with restored ones. The
// mutexes, the optional wired deps (launcher, logs, registrar), and the
// in-flight taskSettle transients are not serialized — a settle window is a
// sub-few-second overlay, so a restored task is simply observed in its final
// state, matching how EC2 excludes its own settle windows.
type ecsSnapshot struct {
	Clusters      json.RawMessage `json:"clusters,omitempty"`
	TaskDefs      json.RawMessage `json:"taskDefs,omitempty"`
	Tasks         json.RawMessage `json:"tasks,omitempty"`
	Services      json.RawMessage `json:"services,omitempty"`
	Instances     json.RawMessage `json:"instances,omitempty"`
	Tags          json.RawMessage `json:"tags,omitempty"`
	Settings      json.RawMessage `json:"settings,omitempty"`
	Attributes    json.RawMessage `json:"attributes,omitempty"`
	EngineHandles json.RawMessage `json:"engineHandles,omitempty"`
	PortCounter   uint32          `json:"portCounter,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// ECS holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap ecsSnapshot
	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	snap.PortCounter = m.portCounter.Load()

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *ecsSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Clusters, m.clusters.Snapshot},
		{&snap.TaskDefs, m.taskDefs.Snapshot},
		{&snap.Tasks, m.tasks.Snapshot},
		{&snap.Services, m.services.Snapshot},
		{&snap.Instances, m.instances.Snapshot},
		{&snap.Tags, m.tags.Snapshot},
		{&snap.Settings, m.settings.Snapshot},
		{&snap.Attributes, m.attributes.Snapshot},
		{&snap.EngineHandles, m.engineHandles.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("ecs: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities (ARNs and the
// composite keys), so a restored task/service/cluster resolves exactly as before.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap ecsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("ecs: parse snapshot: %w", err)
	}

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	m.portCounter.Store(snap.PortCounter)

	return nil
}

func (m *Mock) restoreStores(snap *ecsSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Clusters, m.clusters.LoadSnapshot},
		{snap.TaskDefs, m.taskDefs.LoadSnapshot},
		{snap.Tasks, m.tasks.LoadSnapshot},
		{snap.Services, m.services.LoadSnapshot},
		{snap.Instances, m.instances.LoadSnapshot},
		{snap.Tags, m.tags.LoadSnapshot},
		{snap.Settings, m.settings.LoadSnapshot},
		{snap.Attributes, m.attributes.LoadSnapshot},
		{snap.EngineHandles, m.engineHandles.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("ecs: restore store: %w", err)
		}
	}

	return nil
}
