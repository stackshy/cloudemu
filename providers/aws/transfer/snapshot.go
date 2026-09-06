package transfer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/transfer/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// transferSnapshot is the full serialized state of the Transfer mock. The
// stores hold plain driver values keyed by their resource id (users by the
// composite serverId/userName key), so each map lifts straight out.
type transferSnapshot struct {
	Servers map[string]driver.Server     `json:"servers,omitempty"`
	Users   map[string]driver.User       `json:"users,omitempty"`
	Tags    map[string]map[string]string `json:"tags,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Transfer holds no bulk object bodies (this is a control-plane emulator).
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := transferSnapshot{
		Servers: deepCopyMap(m.servers.All(), copyServer),
		Users:   deepCopyMap(m.users.All(), copyUser),
	}

	m.tagsMu.RLock()
	snap.Tags = deepCopyTags(m.tags)
	m.tagsMu.RUnlock()

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap transferSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("transfer: parse snapshot: %w", err)
	}

	m.servers.Clear()
	m.users.Clear()

	for k := range snap.Servers {
		m.servers.Set(k, copyServer(snap.Servers[k]))
	}

	for k := range snap.Users {
		m.users.Set(k, copyUser(snap.Users[k]))
	}

	m.tagsMu.Lock()
	m.tags = deepCopyTags(snap.Tags)

	if m.tags == nil {
		m.tags = map[string]map[string]string{}
	}
	m.tagsMu.Unlock()

	return nil
}

// deepCopyTags copies a nested ARN->tags map.
func deepCopyTags(in map[string]map[string]string) map[string]map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]map[string]string, len(in))
	for k, v := range in {
		out[k] = copyStringMap(v)
	}

	return out
}

// deepCopyMap returns a copy of in with each value passed through cp.
func deepCopyMap[V any](in map[string]V, cp func(V) V) map[string]V {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]V, len(in))
	for k, v := range in {
		out[k] = cp(v)
	}

	return out
}
