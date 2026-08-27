package iam

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// iamSnapshot is the full serialized state of the AWS IAM mock. Stores whose
// value type has unexported fields (users, roles, policies) are promoted to an
// exported snapshot form; the fully-exported stores round-trip through the
// generic memstore helper. Every attachment/membership map and the account
// password policy are captured too, so a snapshot/restore round-trip preserves
// all identities (user/role/policy ARNs, group memberships, access-key ids) and
// the EC2->instance-profile cross-reference (keyed by profile name) survives.
type iamSnapshot struct {
	Users    map[string]*userSnapshot   `json:"users,omitempty"`
	Roles    map[string]*roleSnapshot   `json:"roles,omitempty"`
	Policies map[string]*policySnapshot `json:"policies,omitempty"`

	Groups           json.RawMessage `json:"groups,omitempty"`
	AccessKeys       json.RawMessage `json:"accessKeys,omitempty"`
	InstanceProfiles json.RawMessage `json:"instanceProfiles,omitempty"`
	MFADevices       json.RawMessage `json:"mfaDevices,omitempty"`

	UserPolicies  map[string]map[string]bool `json:"userPolicies,omitempty"`
	RolePolicies  map[string]map[string]bool `json:"rolePolicies,omitempty"`
	GroupPolicies map[string]map[string]bool `json:"groupPolicies,omitempty"`
	GroupUsers    map[string]map[string]bool `json:"groupUsers,omitempty"`

	UserInlinePolicies  map[string]map[string]string `json:"userInlinePolicies,omitempty"`
	GroupInlinePolicies map[string]map[string]string `json:"groupInlinePolicies,omitempty"`

	PasswordPolicy *driver.PasswordPolicy `json:"passwordPolicy,omitempty"`
}

// userSnapshot mirrors userData, promoting its unexported permissionsBoundary so
// it survives JSON.
type userSnapshot struct {
	Name                string            `json:"name"`
	ID                  string            `json:"id,omitempty"`
	ARN                 string            `json:"arn,omitempty"`
	Path                string            `json:"path,omitempty"`
	Tags                map[string]string `json:"tags,omitempty"`
	CreatedAt           string            `json:"createdAt,omitempty"`
	PermissionsBoundary string            `json:"permissionsBoundary,omitempty"`
}

// roleSnapshot mirrors roleData, promoting its unexported inline policies and
// permissions boundary.
type roleSnapshot struct {
	Name                string            `json:"name"`
	ID                  string            `json:"id,omitempty"`
	ARN                 string            `json:"arn,omitempty"`
	Path                string            `json:"path,omitempty"`
	Description         string            `json:"description,omitempty"`
	AssumeRolePolicyDoc string            `json:"assumeRolePolicyDoc,omitempty"`
	MaxSessionDuration  int               `json:"maxSessionDuration,omitempty"`
	CreatedAt           string            `json:"createdAt,omitempty"`
	Tags                map[string]string `json:"tags,omitempty"`
	InlinePolicies      map[string]string `json:"inlinePolicies,omitempty"`
	PermissionsBoundary string            `json:"permissionsBoundary,omitempty"`
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
		Users:               m.snapshotUsers(),
		Roles:               m.snapshotRoles(),
		Policies:            m.snapshotPolicies(),
		UserPolicies:        m.userPolicies,
		RolePolicies:        m.rolePolicies,
		GroupPolicies:       m.groupPolicies,
		GroupUsers:          m.groupUsers,
		UserInlinePolicies:  m.userInlinePolicies,
		GroupInlinePolicies: m.groupInlinePolicies,
		PasswordPolicy:      m.passwordPolicy,
	}

	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	//nolint:gosec // G117: persist intentionally serializes IAM access-key material so a snapshot/restore round-trip is transparent.
	return json.Marshal(snap)
}

func (m *Mock) snapshotUsers() map[string]*userSnapshot {
	out := make(map[string]*userSnapshot, m.users.Len())

	for name, u := range m.users.All() {
		out[name] = &userSnapshot{
			Name: u.Name, ID: u.ID, ARN: u.ARN, Path: u.Path, Tags: u.Tags,
			CreatedAt: u.CreatedAt, PermissionsBoundary: u.permissionsBoundary,
		}
	}

	return out
}

func (m *Mock) snapshotRoles() map[string]*roleSnapshot {
	out := make(map[string]*roleSnapshot, m.roles.Len())

	for name, r := range m.roles.All() {
		out[name] = &roleSnapshot{
			Name: r.Name, ID: r.ID, ARN: r.ARN, Path: r.Path, Description: r.Description,
			AssumeRolePolicyDoc: r.AssumeRolePolicyDoc, MaxSessionDuration: r.MaxSessionDuration,
			CreatedAt: r.CreatedAt, Tags: r.Tags, InlinePolicies: r.inlinePolicies,
			PermissionsBoundary: r.permissionsBoundary,
		}
	}

	return out
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

// snapshotStores dumps the fully-exported stores through the generic memstore
// helper, preserving their keys (group name, access-key id, profile name, MFA
// serial).
func (m *Mock) snapshotStores(snap *iamSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Groups, m.groups.Snapshot},
		{&snap.AccessKeys, m.accessKeys.Snapshot},
		{&snap.InstanceProfiles, m.instanceProfiles.Snapshot},
		{&snap.MFADevices, m.mfaDevices.Snapshot},
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

	m.restoreUsers(snap.Users)
	m.restoreRoles(snap.Roles)
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

	if snap.GroupPolicies != nil {
		m.groupPolicies = snap.GroupPolicies
	}

	if snap.GroupUsers != nil {
		m.groupUsers = snap.GroupUsers
	}

	if snap.UserInlinePolicies != nil {
		m.userInlinePolicies = snap.UserInlinePolicies
	}

	if snap.GroupInlinePolicies != nil {
		m.groupInlinePolicies = snap.GroupInlinePolicies
	}

	m.passwordPolicy = snap.PasswordPolicy

	return nil
}

func (m *Mock) restoreUsers(users map[string]*userSnapshot) {
	for name, u := range users {
		m.users.Set(name, &userData{
			Name: u.Name, ID: u.ID, ARN: u.ARN, Path: u.Path, Tags: u.Tags,
			CreatedAt: u.CreatedAt, permissionsBoundary: u.PermissionsBoundary,
		})
	}
}

func (m *Mock) restoreRoles(roles map[string]*roleSnapshot) {
	for name, r := range roles {
		m.roles.Set(name, &roleData{
			Name: r.Name, ID: r.ID, ARN: r.ARN, Path: r.Path, Description: r.Description,
			AssumeRolePolicyDoc: r.AssumeRolePolicyDoc, MaxSessionDuration: r.MaxSessionDuration,
			CreatedAt: r.CreatedAt, Tags: r.Tags, inlinePolicies: r.InlinePolicies,
			permissionsBoundary: r.PermissionsBoundary,
		})
	}
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
		{snap.Groups, m.groups.LoadSnapshot},
		{snap.AccessKeys, m.accessKeys.LoadSnapshot},
		{snap.InstanceProfiles, m.instanceProfiles.LoadSnapshot},
		{snap.MFADevices, m.mfaDevices.LoadSnapshot},
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
