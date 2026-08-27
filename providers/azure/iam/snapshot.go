package iam

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// iamSnapshot is the full serialized state of the Azure IAM mock. The policies
// store is promoted to an exported snapshot form (policyData carries an
// unexported version list); the fully-exported stores round-trip through the
// generic memstore helper. The attachment/membership maps are captured too, so
// a snapshot/restore round-trip preserves all identities (user/role/policy
// ARNs, group memberships, access-key ids, instance-profile names).
type iamSnapshot struct {
	Policies map[string]*policySnapshot `json:"policies,omitempty"`

	Users            json.RawMessage `json:"users,omitempty"`
	Roles            json.RawMessage `json:"roles,omitempty"`
	Groups           json.RawMessage `json:"groups,omitempty"`
	AccessKeys       json.RawMessage `json:"accessKeys,omitempty"`
	InstanceProfiles json.RawMessage `json:"instanceProfiles,omitempty"`

	UserPolicies map[string]map[string]bool `json:"userPolicies,omitempty"`
	RolePolicies map[string]map[string]bool `json:"rolePolicies,omitempty"`
	GroupUsers   map[string]map[string]bool `json:"groupUsers,omitempty"`
}

// policySnapshot mirrors policyData, promoting its unexported version list and
// counter so the version history (and default-version pointer) round-trips.
type policySnapshot struct {
	Name           string               `json:"name"`
	ID             string               `json:"id,omitempty"`
	ARN            string               `json:"arn,omitempty"`
	Path           string               `json:"path,omitempty"`
	PolicyDocument string               `json:"policyDocument,omitempty"`
	Description    string               `json:"description,omitempty"`
	Versions       []*policyVersionData `json:"versions,omitempty"`
	VersionCounter int                  `json:"versionCounter,omitempty"`
}

// Snapshot captures the entire IAM state as JSON. includeAssets is unused — IAM
// holds no bulk object bodies, so everything is always captured.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snap := iamSnapshot{
		Policies:     m.snapshotPolicies(),
		UserPolicies: m.userPolicies,
		RolePolicies: m.rolePolicies,
		GroupUsers:   m.groupUsers,
	}

	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	//nolint:gosec // G117: persist intentionally serializes IAM access-key material so a snapshot/restore round-trip is transparent.
	return json.Marshal(snap)
}

func (m *Mock) snapshotPolicies() map[string]*policySnapshot {
	out := make(map[string]*policySnapshot, m.policies.Len())

	for arn, p := range m.policies.All() {
		out[arn] = &policySnapshot{
			Name: p.Name, ID: p.ID, ARN: p.ARN, Path: p.Path, PolicyDocument: p.PolicyDocument,
			Description: p.Description, Versions: p.versions, VersionCounter: p.versionCounter,
		}
	}

	return out
}

func (m *Mock) snapshotStores(snap *iamSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Users, m.users.Snapshot},
		{&snap.Roles, m.roles.Snapshot},
		{&snap.Groups, m.groups.Snapshot},
		{&snap.AccessKeys, m.accessKeys.Snapshot},
		{&snap.InstanceProfiles, m.instanceProfiles.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("iam: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds every store and attachment map under the original
// identities. It is called on a freshly built (empty) mock.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap iamSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("iam: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.restorePolicies(snap.Policies)

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	if snap.UserPolicies != nil {
		m.userPolicies = snap.UserPolicies
	}

	if snap.RolePolicies != nil {
		m.rolePolicies = snap.RolePolicies
	}

	if snap.GroupUsers != nil {
		m.groupUsers = snap.GroupUsers
	}

	return nil
}

func (m *Mock) restorePolicies(policies map[string]*policySnapshot) {
	for arn, p := range policies {
		m.policies.Set(arn, &policyData{
			Name: p.Name, ID: p.ID, ARN: p.ARN, Path: p.Path, PolicyDocument: p.PolicyDocument,
			Description: p.Description, versions: p.Versions, versionCounter: p.VersionCounter,
		})
	}
}

func (m *Mock) restoreStores(snap *iamSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Users, m.users.LoadSnapshot},
		{snap.Roles, m.roles.LoadSnapshot},
		{snap.Groups, m.groups.LoadSnapshot},
		{snap.AccessKeys, m.accessKeys.LoadSnapshot},
		{snap.InstanceProfiles, m.instanceProfiles.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("iam: restore store: %w", err)
		}
	}

	return nil
}
