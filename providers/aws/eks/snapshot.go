package eks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// eksSnapshot is the full serialized state of the AWS EKS mock. Every store
// holds a fully-exported eksdriver type, so each round-trips through the generic
// memstore helper keyed by its resource id (cluster name, nodegroup/profile/addon
// keys). k8sUIDs preserves each cluster's registered Kubernetes UID so a restore
// keeps the cluster→UID mapping stable. The mutex and the wired deps (opts,
// monitoring, subnetResolver) are not serialized; the shared *kubernetes.APIServer
// data plane is external shared state and is intentionally not re-registered here
// — the stored records and the UID mapping are what a restore reinstates.
type eksSnapshot struct {
	Clusters        json.RawMessage   `json:"clusters,omitempty"`
	Nodegroups      json.RawMessage   `json:"nodegroups,omitempty"`
	FargateProfiles json.RawMessage   `json:"fargateProfiles,omitempty"`
	Addons          json.RawMessage   `json:"addons,omitempty"`
	Updates         json.RawMessage   `json:"updates,omitempty"`
	K8sUIDs         map[string]string `json:"k8sUids,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// EKS holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap eksSnapshot
	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	m.mu.RLock()
	if len(m.k8sUIDs) > 0 {
		uids := make(map[string]string, len(m.k8sUIDs))
		for k, v := range m.k8sUIDs {
			uids[k] = v
		}

		snap.K8sUIDs = uids
	}
	m.mu.RUnlock()

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *eksSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Clusters, m.clusters.Snapshot},
		{&snap.Nodegroups, m.nodegroups.Snapshot},
		{&snap.FargateProfiles, m.fargateProfiles.Snapshot},
		{&snap.Addons, m.addons.Snapshot},
		{&snap.Updates, m.updates.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("eks: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities: every cluster
// name and nodegroup/profile/addon key is preserved.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap eksSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("eks: parse snapshot: %w", err)
	}

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	if snap.K8sUIDs != nil {
		m.mu.Lock()
		m.k8sUIDs = snap.K8sUIDs
		m.mu.Unlock()
	}

	return nil
}

func (m *Mock) restoreStores(snap *eksSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Clusters, m.clusters.LoadSnapshot},
		{snap.Nodegroups, m.nodegroups.LoadSnapshot},
		{snap.FargateProfiles, m.fargateProfiles.LoadSnapshot},
		{snap.Addons, m.addons.LoadSnapshot},
		{snap.Updates, m.updates.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("eks: restore store: %w", err)
		}
	}

	return nil
}
