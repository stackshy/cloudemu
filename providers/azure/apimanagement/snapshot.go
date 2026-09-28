package apimanagement

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// snapshotState is the on-disk shape: the live services and their child
// resources keyed by the (lowercased) service resource id, and the
// soft-deleted services keyed by subscription/location/name.
type snapshotState struct {
	Services json.RawMessage `json:"services,omitempty"`
	Children json.RawMessage `json:"children,omitempty"`
	Deleted  json.RawMessage `json:"deleted,omitempty"`
}

// Snapshot captures every API Management service, its child resources and the
// soft-deleted services. includeAssets is unused: these resources hold no bulk
// object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	services, err := m.services.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("apimanagement: snapshot services: %w", err)
	}

	children, err := m.children.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("apimanagement: snapshot children: %w", err)
	}

	deleted, err := m.deleted.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("apimanagement: snapshot deleted services: %w", err)
	}

	data, err := json.Marshal(snapshotState{Services: services, Children: children, Deleted: deleted})
	if err != nil {
		return nil, fmt.Errorf("apimanagement: marshal snapshot: %w", err)
	}

	return data, nil
}

// Restore rebuilds every service, child resource and soft-deleted service under
// its original key. Each restored service's properties block is re-materialized,
// so a snapshot written before the provider owned the computed fields comes
// back with them.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(data) == 0 {
		return nil
	}

	var state snapshotState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("apimanagement: unmarshal snapshot: %w", err)
	}

	loads := []struct {
		name string
		raw  json.RawMessage
		load func([]byte) error
	}{
		{"services", state.Services, m.services.LoadSnapshot},
		{"children", state.Children, m.children.LoadSnapshot},
		{"deleted services", state.Deleted, m.deleted.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.raw) == 0 {
			continue
		}

		if err := l.load(l.raw); err != nil {
			return fmt.Errorf("apimanagement: restore %s: %w", l.name, err)
		}
	}

	for _, s := range m.services.All() {
		s.materializeProperties()
	}

	return nil
}
