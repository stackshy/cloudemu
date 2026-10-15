package cognito

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/jwtsign"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// cognitoSnapshot is the full serialized state of the Cognito mock. The stores
// hold plain driver values keyed by their resource id (clients by the composite
// poolID/clientID key), so each map lifts straight out. Tags live only in the
// ARN-keyed side map. Challenge sessions last minutes and are not kept.
type cognitoSnapshot struct {
	UserPools map[string]driver.UserPool       `json:"userPools,omitempty"`
	Clients   map[string]driver.UserPoolClient `json:"clients,omitempty"`
	Domains   map[string]driver.UserPoolDomain `json:"domains,omitempty"`
	Users     map[string]userRecord            `json:"users,omitempty"`
	Groups    map[string]driver.Group          `json:"groups,omitempty"`
	Logins    map[string]loginRecord           `json:"logins,omitempty"`
	Keys      map[string]keySnapshot           `json:"keys,omitempty"`
	Tags      map[string]map[string]string     `json:"tags,omitempty"`
}

// keySnapshot holds a pool's two signing keys as PKCS#8 DER, so tokens issued
// before a restart still verify after it.
type keySnapshot struct {
	ID     []byte `json:"id"`
	Access []byte `json:"access"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused. Cognito holds no
// bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := cognitoSnapshot{
		UserPools: deepCopyMap(m.userPools.All(), copyUserPool),
		Clients:   deepCopyMap(m.clients.All(), copyUserPoolClient),
		Domains:   deepCopyMap(m.domains.All(), copyUserPoolDomain),
		Users:     deepCopyMap(m.users.All(), copyUserRecord),
		Groups:    deepCopyMap(m.groups.All(), copyGroup),
		Logins:    deepCopyMap(m.logins.All(), copyLoginRecord),
	}

	keys, err := m.snapshotKeys()
	if err != nil {
		return nil, err
	}

	snap.Keys = keys

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

	keys, err := parseKeys(snap.Keys)
	if err != nil {
		return err
	}

	m.userPools.Clear()
	m.clients.Clear()
	m.domains.Clear()
	m.users.Clear()
	m.groups.Clear()
	m.logins.Clear()

	for k := range snap.UserPools {
		m.userPools.Set(k, copyUserPool(snap.UserPools[k]))
	}

	for k := range snap.Clients {
		m.clients.Set(k, copyUserPoolClient(snap.Clients[k]))
	}

	for k := range snap.Domains {
		m.domains.Set(k, copyUserPoolDomain(snap.Domains[k]))
	}

	for k := range snap.Users {
		m.users.Set(k, copyUserRecord(snap.Users[k]))
	}

	for k := range snap.Groups {
		m.groups.Set(k, copyGroup(snap.Groups[k]))
	}

	for k := range snap.Logins {
		m.logins.Set(k, snap.Logins[k])
	}

	m.keysMu.Lock()
	m.keys = keys
	m.keysMu.Unlock()

	m.sessionsMu.Lock()
	m.sessions = map[string]challengeSession{}
	m.sessionsMu.Unlock()

	m.tagsMu.Lock()
	m.tags = deepCopyTags(snap.Tags)

	if m.tags == nil {
		m.tags = map[string]map[string]string{}
	}
	m.tagsMu.Unlock()

	return nil
}

// snapshotKeys encodes every pool's signing keys as PKCS#8.
func (m *Mock) snapshotKeys() (map[string]keySnapshot, error) {
	m.keysMu.Lock()
	defer m.keysMu.Unlock()

	if len(m.keys) == 0 {
		return nil, nil
	}

	out := make(map[string]keySnapshot, len(m.keys))

	for poolID, k := range m.keys {
		id, err := jwtsign.MarshalPKCS8(k.id)
		if err != nil {
			return nil, err
		}

		access, err := jwtsign.MarshalPKCS8(k.access)
		if err != nil {
			return nil, err
		}

		out[poolID] = keySnapshot{ID: id, Access: access}
	}

	return out, nil
}

// parseKeys decodes snapshotted signing keys. The kids are recomputed from the
// keys, so they match the ones published before the snapshot.
func parseKeys(in map[string]keySnapshot) (map[string]*poolKeys, error) {
	keys := make(map[string]*poolKeys, len(in))

	for poolID, ks := range in {
		id, err := jwtsign.ParsePKCS8(ks.ID)
		if err != nil {
			return nil, fmt.Errorf("cognito: restore keys of %s: %w", poolID, err)
		}

		access, err := jwtsign.ParsePKCS8(ks.Access)
		if err != nil {
			return nil, fmt.Errorf("cognito: restore keys of %s: %w", poolID, err)
		}

		keys[poolID] = &poolKeys{id: id, access: access}
	}

	return keys, nil
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
