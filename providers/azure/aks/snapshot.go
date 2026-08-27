package aks

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// aksSnapshot is the full serialized state of the AKS mock. The three stores hold
// fully-exported values (ManagedCluster, AgentPool, MaintenanceConfig) and
// round-trip through the generic memstore helper keyed by their composite ids
// ("{rg}/{name}", "{rg}/{cluster}/{pool}", …), so cross-references survive.
// k8sUIDs (the "{rg}/{name}" → registered Kubernetes UID map) is captured so a
// restored cluster keeps its data-plane identity. The mutex and the wired
// options/monitoring/k8sAPI (a live data-plane server pointer) are intentionally
// not serialized.
type aksSnapshot struct {
	Clusters    json.RawMessage   `json:"clusters,omitempty"`
	Pools       json.RawMessage   `json:"pools,omitempty"`
	Maintenance json.RawMessage   `json:"maintenance,omitempty"`
	K8sUIDs     map[string]string `json:"k8sUids,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// AKS holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap aksSnapshot

	clusters, err := m.clusters.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("aks: snapshot clusters: %w", err)
	}

	pools, err := m.pools.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("aks: snapshot pools: %w", err)
	}

	maint, err := m.maintenance.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("aks: snapshot maintenance: %w", err)
	}

	snap.Clusters = clusters
	snap.Pools = pools
	snap.Maintenance = maint

	m.mu.RLock()
	if len(m.k8sUIDs) > 0 {
		snap.K8sUIDs = maps.Clone(m.k8sUIDs)
	}
	m.mu.RUnlock()

	return json.Marshal(snap)
}

// Restore rebuilds every store under its original keys and reinstates the
// data-plane UID map.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap aksSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("aks: parse snapshot: %w", err)
	}

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Clusters, m.clusters.LoadSnapshot},
		{snap.Pools, m.pools.LoadSnapshot},
		{snap.Maintenance, m.maintenance.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("aks: restore store: %w", err)
		}
	}

	if len(snap.K8sUIDs) > 0 {
		m.mu.Lock()
		maps.Copy(m.k8sUIDs, snap.K8sUIDs)
		m.mu.Unlock()
	}

	return nil
}
