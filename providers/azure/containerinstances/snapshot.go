package containerinstances

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/containerinstances/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// ciSnapshot is the full serialized state of the Azure Container Instances mock.
// The groups store holds an unexported *groupData whose fields (the container
// group plus its engine handle/engine-backed flag) are unexported, so it is
// promoted to an exported form keyed by the composite ARM key
// (subscription/resourceGroup/name). The wired opts and the live container-engine
// workload are not serialized — a restored group reports its stored state.
type ciSnapshot struct {
	Groups map[string]*groupSnapshot `json:"groups,omitempty"`
}

// groupSnapshot mirrors groupData (all fields unexported).
type groupSnapshot struct {
	Group        driver.ContainerGroup `json:"group"`
	Handle       string                `json:"handle,omitempty"`
	EngineBacked bool                  `json:"engineBacked,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// ACI holds container-group metadata, not bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := ciSnapshot{}

	if m.groups.Len() > 0 {
		snap.Groups = make(map[string]*groupSnapshot, m.groups.Len())
		for key, gd := range m.groups.All() {
			snap.Groups[key] = &groupSnapshot{
				Group: gd.group, Handle: gd.handle, EngineBacked: gd.engineBacked,
			}
		}
	}

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: every
// container group's composite ARM key is preserved so a client's identifiers
// still resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap ciSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("containerinstances: parse snapshot: %w", err)
	}

	for key, gs := range snap.Groups {
		m.groups.Set(key, &groupData{group: gs.Group, handle: gs.Handle, engineBacked: gs.EngineBacked})
	}

	return nil
}
