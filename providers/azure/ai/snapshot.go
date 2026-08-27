package ai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// aiSnapshot is the full serialized state of the Azure AI mock. Every store's
// value type is fully exported (driver.*), so each round-trips through the
// generic memstore helper keyed by its resource id. The operation sequence
// counter is captured so ids minted after a restore do not collide. The wired
// *config.Options and monitoring backend are intentionally not serialized.
type aiSnapshot struct {
	Accounts         json.RawMessage `json:"accounts,omitempty"`
	AccountKeys      json.RawMessage `json:"accountKeys,omitempty"`
	Deployments      json.RawMessage `json:"deployments,omitempty"`
	Projects         json.RawMessage `json:"projects,omitempty"`
	RaiPolicies      json.RawMessage `json:"raiPolicies,omitempty"`
	CommitmentPlans  json.RawMessage `json:"commitmentPlans,omitempty"`
	PrivateEndpoints json.RawMessage `json:"privateEndpoints,omitempty"`
	Assistants       json.RawMessage `json:"assistants,omitempty"`
	Threads          json.RawMessage `json:"threads,omitempty"`
	Messages         json.RawMessage `json:"messages,omitempty"`
	Runs             json.RawMessage `json:"runs,omitempty"`
	MLWorkspaces     json.RawMessage `json:"mlWorkspaces,omitempty"`
	Computes         json.RawMessage `json:"computes,omitempty"`
	MLEndpoints      json.RawMessage `json:"mlEndpoints,omitempty"`
	MLDeploys        json.RawMessage `json:"mlDeploys,omitempty"`
	Jobs             json.RawMessage `json:"jobs,omitempty"`
	Assets           json.RawMessage `json:"assets,omitempty"`
	Datastores       json.RawMessage `json:"datastores,omitempty"`
	Connections      json.RawMessage `json:"connections,omitempty"`
	MLSchedules      json.RawMessage `json:"mlSchedules,omitempty"`
	Registries       json.RawMessage `json:"registries,omitempty"`
	Seq              int64           `json:"seq,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Azure AI holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap aiSnapshot
	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	snap.Seq = m.seq.Load()

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *aiSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Accounts, m.accounts.Snapshot},
		{&snap.AccountKeys, m.accountKeys.Snapshot},
		{&snap.Deployments, m.deployments.Snapshot},
		{&snap.Projects, m.projects.Snapshot},
		{&snap.RaiPolicies, m.raiPolicies.Snapshot},
		{&snap.CommitmentPlans, m.commitmentPlans.Snapshot},
		{&snap.PrivateEndpoints, m.privateEndpoints.Snapshot},
		{&snap.Assistants, m.assistants.Snapshot},
		{&snap.Threads, m.threads.Snapshot},
		{&snap.Messages, m.messages.Snapshot},
		{&snap.Runs, m.runs.Snapshot},
		{&snap.MLWorkspaces, m.mlWorkspaces.Snapshot},
		{&snap.Computes, m.computes.Snapshot},
		{&snap.MLEndpoints, m.mlEndpoints.Snapshot},
		{&snap.MLDeploys, m.mlDeploys.Snapshot},
		{&snap.Jobs, m.jobs.Snapshot},
		{&snap.Assets, m.assets.Snapshot},
		{&snap.Datastores, m.datastores.Snapshot},
		{&snap.Connections, m.connections.Snapshot},
		{&snap.MLSchedules, m.mlSchedules.Snapshot},
		{&snap.Registries, m.registries.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("ai: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities: every
// account/deployment/workspace id is preserved so cross-references resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap aiSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("ai: parse snapshot: %w", err)
	}

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	m.seq.Store(snap.Seq)

	return nil
}

func (m *Mock) restoreStores(snap *aiSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Accounts, m.accounts.LoadSnapshot},
		{snap.AccountKeys, m.accountKeys.LoadSnapshot},
		{snap.Deployments, m.deployments.LoadSnapshot},
		{snap.Projects, m.projects.LoadSnapshot},
		{snap.RaiPolicies, m.raiPolicies.LoadSnapshot},
		{snap.CommitmentPlans, m.commitmentPlans.LoadSnapshot},
		{snap.PrivateEndpoints, m.privateEndpoints.LoadSnapshot},
		{snap.Assistants, m.assistants.LoadSnapshot},
		{snap.Threads, m.threads.LoadSnapshot},
		{snap.Messages, m.messages.LoadSnapshot},
		{snap.Runs, m.runs.LoadSnapshot},
		{snap.MLWorkspaces, m.mlWorkspaces.LoadSnapshot},
		{snap.Computes, m.computes.LoadSnapshot},
		{snap.MLEndpoints, m.mlEndpoints.LoadSnapshot},
		{snap.MLDeploys, m.mlDeploys.LoadSnapshot},
		{snap.Jobs, m.jobs.LoadSnapshot},
		{snap.Assets, m.assets.LoadSnapshot},
		{snap.Datastores, m.datastores.LoadSnapshot},
		{snap.Connections, m.connections.LoadSnapshot},
		{snap.MLSchedules, m.mlSchedules.LoadSnapshot},
		{snap.Registries, m.registries.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("ai: restore store: %w", err)
		}
	}

	return nil
}
