package bigtable

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// btSnapshot is the full serialized state of the Bigtable Admin mock. Every store
// holds a fully-exported btdriver value type keyed by its full GCP resource name
// (projects/{p}/instances/{i}/...), so each round-trips through the generic
// memstore helper — no promotion is needed. policies is a plain mu-guarded map of
// resource -> IAM Policy (a fully-exported type), and opSeq is the operation-name
// counter, both captured beside the stores so restored operation ids do not
// collide with fresh ones. The wired deps (m.opts) and the RWMutex are
// intentionally not serialized.
type btSnapshot struct {
	Instances   json.RawMessage            `json:"instances,omitempty"`
	Clusters    json.RawMessage            `json:"clusters,omitempty"`
	Tables      json.RawMessage            `json:"tables,omitempty"`
	AppProfiles json.RawMessage            `json:"appProfiles,omitempty"`
	Backups     json.RawMessage            `json:"backups,omitempty"`
	Operations  json.RawMessage            `json:"operations,omitempty"`
	Policies    map[string]btdriver.Policy `json:"policies,omitempty"`
	OpSeq       uint64                     `json:"opSeq,omitempty"`
}

// Snapshot captures every instance and its children as JSON. includeAssets is
// unused — Bigtable Admin is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var snap btSnapshot

	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Instances, m.instances.Snapshot},
		{&snap.Clusters, m.clusters.Snapshot},
		{&snap.Tables, m.tables.Snapshot},
		{&snap.AppProfiles, m.appProfiles.Snapshot},
		{&snap.Backups, m.backups.Snapshot},
		{&snap.Operations, m.operations.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("bigtable: snapshot store: %w", err)
		}

		*d.dst = b
	}

	if len(m.policies) > 0 {
		snap.Policies = make(map[string]btdriver.Policy, len(m.policies))
		for k, v := range m.policies {
			snap.Policies[k] = clonePolicy(&v)
		}
	}

	snap.OpSeq = m.opSeq.Load()

	return json.Marshal(snap)
}

// Restore rebuilds every instance and child under its original resource name, so
// cross-references (cluster->instance, table->instance) and IAM policies survive.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap btSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("bigtable: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Instances, m.instances.LoadSnapshot},
		{snap.Clusters, m.clusters.LoadSnapshot},
		{snap.Tables, m.tables.LoadSnapshot},
		{snap.AppProfiles, m.appProfiles.LoadSnapshot},
		{snap.Backups, m.backups.LoadSnapshot},
		{snap.Operations, m.operations.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("bigtable: restore store: %w", err)
		}
	}

	for k, v := range snap.Policies {
		m.policies[k] = clonePolicy(&v)
	}

	m.opSeq.Store(snap.OpSeq)

	return nil
}
