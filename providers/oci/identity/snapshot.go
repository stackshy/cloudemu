package identity

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// identitySnapshot is the full serialized state of the OCI Identity mock. Every
// store is keyed by resource OCID so cross-references (a membership's UserID /
// GroupID, a policy's compartment scope, a compartment's parent) still resolve
// after a restore. The fully-exported value types round-trip through the generic
// memstore helper; policies carry an exported form because policy has unexported
// fields (parsed / versions / versionCounter). The mutex and *config.Options are
// intentionally not serialized.
type identitySnapshot struct {
	Users         json.RawMessage            `json:"users,omitempty"`
	Groups        json.RawMessage            `json:"groups,omitempty"`
	Memberships   json.RawMessage            `json:"memberships,omitempty"`
	Compartments  json.RawMessage            `json:"compartments,omitempty"`
	DynamicGroups json.RawMessage            `json:"dynamicGroups,omitempty"`
	AuthTokens    json.RawMessage            `json:"authTokens,omitempty"`
	Policies      map[string]*policySnapshot `json:"policies,omitempty"`
}

// policySnapshot mirrors policy, promoting its unexported fields (versions and
// versionCounter) to exported ones so they survive JSON. parsed is derived from
// Statements and rebuilt on restore rather than serialized. policyRevision is
// fully exported, so the version history embeds directly.
type policySnapshot struct {
	ID             string            `json:"id"`
	Name           string            `json:"name,omitempty"`
	Description    string            `json:"description,omitempty"`
	Statements     []string          `json:"statements,omitempty"`
	TimeCreated    string            `json:"timeCreated,omitempty"`
	VersionDate    string            `json:"versionDate,omitempty"`
	Scope          scope.Scope       `json:"scope,omitempty"`
	FreeformTags   map[string]string `json:"freeformTags,omitempty"`
	Versions       []*policyRevision `json:"versions,omitempty"`
	VersionCounter int               `json:"versionCounter,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Identity holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snap := identitySnapshot{Policies: m.snapshotPolicies()}

	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Users, m.users.Snapshot},
		{&snap.Groups, m.groups.Snapshot},
		{&snap.Memberships, m.memberships.Snapshot},
		{&snap.Compartments, m.compartments.Snapshot},
		{&snap.DynamicGroups, m.dynamicGroups.Snapshot},
		{&snap.AuthTokens, m.authTokens.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("identity: snapshot store: %w", err)
		}

		*d.dst = b
	}

	// G117 false positive: the authTokens key names a store of auth-token
	// metadata that is intentionally persisted, not a leaked credential.
	//nolint:gosec // authTokens is a resource store, not a hardcoded secret.
	return json.Marshal(snap)
}

// snapshotPolicies promotes each stored policy to its exported snapshot form.
// Callers hold m.mu.
func (m *Mock) snapshotPolicies() map[string]*policySnapshot {
	if m.policies.Len() == 0 {
		return nil
	}

	out := make(map[string]*policySnapshot, m.policies.Len())

	for id, p := range m.policies.All() {
		out[id] = &policySnapshot{
			ID:             p.ID,
			Name:           p.Name,
			Description:    p.Description,
			Statements:     copyStrings(p.Statements),
			TimeCreated:    p.TimeCreated,
			VersionDate:    p.VersionDate,
			Scope:          p.Scope,
			FreeformTags:   copyTags(p.FreeformTags),
			Versions:       p.versions,
			VersionCounter: p.versionCounter,
		}
	}

	return out
}

// Restore rebuilds the mock's state under the original identities, so restored
// users, groups, memberships, policies and compartments keep their OCIDs and
// every id-string cross-reference between them still resolves.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap identitySnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("identity: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.restorePolicies(snap.Policies)

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Users, m.users.LoadSnapshot},
		{snap.Groups, m.groups.LoadSnapshot},
		{snap.Memberships, m.memberships.LoadSnapshot},
		{snap.Compartments, m.compartments.LoadSnapshot},
		{snap.DynamicGroups, m.dynamicGroups.LoadSnapshot},
		{snap.AuthTokens, m.authTokens.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("identity: restore store: %w", err)
		}
	}

	return nil
}

// restorePolicies reinstates each policy under its original OCID, re-deriving the
// parsed statements from the stored text so Evaluate works after a restore.
// Callers hold m.mu.
func (m *Mock) restorePolicies(policies map[string]*policySnapshot) {
	for id, s := range policies {
		parsed, _ := parseStatements(s.Statements)
		m.policies.Set(id, &policy{
			ID:             s.ID,
			Name:           s.Name,
			Description:    s.Description,
			Statements:     copyStrings(s.Statements),
			TimeCreated:    s.TimeCreated,
			VersionDate:    s.VersionDate,
			Scope:          s.Scope,
			FreeformTags:   copyTags(s.FreeformTags),
			parsed:         parsed,
			versions:       s.Versions,
			versionCounter: s.VersionCounter,
		})
	}
}
