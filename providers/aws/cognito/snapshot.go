package cognito

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// cognitoSnapshot is the full serialized state of the Cognito mock. The stores
// hold plain driver values keyed by their resource id (clients by the composite
// poolID/clientID key), so each map lifts straight out. Tags live only in the
// ARN-keyed side map.
type cognitoSnapshot struct {
	UserPools map[string]driver.UserPool       `json:"userPools,omitempty"`
	Clients   map[string]driver.UserPoolClient `json:"clients,omitempty"`
	Domains   map[string]driver.UserPoolDomain `json:"domains,omitempty"`
	Tags      map[string]map[string]string     `json:"tags,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Cognito holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := cognitoSnapshot{
		UserPools: deepCopyMap(m.userPools.All(), copyUserPool),
		Clients:   deepCopyMap(m.clients.All(), copyUserPoolClient),
		Domains:   deepCopyMap(m.domains.All(), copyUserPoolDomain),
	}

	m.tagsMu.RLock()
	snap.Tags = deepCopyTags(m.tags)
	m.tagsMu.RUnlock()

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap cognitoSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("cognito: parse snapshot: %w", err)
	}

	m.userPools.Clear()
	m.clients.Clear()
	m.domains.Clear()

	for k := range snap.UserPools {
		m.userPools.Set(k, copyUserPool(snap.UserPools[k]))
	}

	for k := range snap.Clients {
		m.clients.Set(k, copyUserPoolClient(snap.Clients[k]))
	}

	for k := range snap.Domains {
		m.domains.Set(k, copyUserPoolDomain(snap.Domains[k]))
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
