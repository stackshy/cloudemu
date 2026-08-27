package gke

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// gkeSnapshot is the full serialized state of the GKE mock. Every stored value
// type (Cluster/NodePool/Operation) is fully exported, so the stores round-trip
// through the generic memstore helper keyed by their ids (node pools keyed
// "cluster/nodePool"); k8sUIDs preserves the UID each cluster registered with the
// shared Kubernetes data plane. The wired *config.Options, monitoring backend,
// and *kubernetes.APIServer are intentionally not serialized.
type gkeSnapshot struct {
	Clusters   json.RawMessage   `json:"clusters,omitempty"`
	NodePools  json.RawMessage   `json:"nodePools,omitempty"`
	Operations json.RawMessage   `json:"operations,omitempty"`
	K8sUIDs    map[string]string `json:"k8sUids,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// GKE holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap gkeSnapshot

	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	m.mu.RLock()
	snap.K8sUIDs = m.k8sUIDs
	m.mu.RUnlock()

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *gkeSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Clusters, m.clusters.Snapshot},
		{&snap.NodePools, m.nodePools.Snapshot},
		{&snap.Operations, m.operations.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("gke: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities: cluster and
// node-pool ids (and the "cluster/nodePool" keys) are preserved so a restored
// node pool still resolves to its cluster.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap gkeSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("gke: parse snapshot: %w", err)
	}

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	m.mu.Lock()
	if snap.K8sUIDs != nil {
		m.k8sUIDs = snap.K8sUIDs
	}
	m.mu.Unlock()

	return nil
}

func (m *Mock) restoreStores(snap *gkeSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Clusters, m.clusters.LoadSnapshot},
		{snap.NodePools, m.nodePools.LoadSnapshot},
		{snap.Operations, m.operations.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("gke: restore store: %w", err)
		}
	}

	return nil
}
