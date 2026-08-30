package managedidentity

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// miSnapshot is the full serialized state of the managed-identity mock: every
// identity keyed by its (lowercased) resource id, plus the estate tenant id.
// Identity is fully exported and round-trips through the generic memstore
// helper; the tenant id is captured so newly created identities keep reporting
// the same tenant after a restore.
type miSnapshot struct {
	Identities json.RawMessage `json:"identities,omitempty"`
	TenantID   string          `json:"tenantId,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// managed identities hold no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids, err := m.store.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("managedidentity: snapshot store: %w", err)
	}

	return json.Marshal(miSnapshot{Identities: ids, TenantID: m.tenantID})
}

// Restore rebuilds every identity under its original id and restores the estate
// tenant id.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap miSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("managedidentity: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if snap.TenantID != "" {
		m.tenantID = snap.TenantID
	}

	if len(snap.Identities) == 0 {
		return nil
	}

	if err := m.store.LoadSnapshot(snap.Identities); err != nil {
		return fmt.Errorf("managedidentity: restore store: %w", err)
	}

	return nil
}
