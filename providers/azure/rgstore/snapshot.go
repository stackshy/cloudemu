package rgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var (
	_ snapshot.Snapshottable   = (*Mock)(nil)
	_ snapshot.MissingRestorer = (*Mock)(nil)
)

// defaultLocation is the location a rebuilt group gets when none of its
// resources reports one.
const defaultLocation = "eastus"

// GroupRef names one resource group a restored resource lives in, with that
// resource's location ("" when unknown).
type GroupRef struct {
	Subscription string
	Name         string
	Location     string
}

// SetRebuildSource sets where RestoreMissing reads the restored resources'
// groups from. The provider wires it to its cross-service inventory.
func (m *Mock) SetRebuildSource(fn func(ctx context.Context) ([]GroupRef, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.source = fn
}

// RestoreMissing rebuilds the resource groups for a snapshot written before
// groups were persisted: every group a restored resource names is created with
// that resource's location, or defaultLocation, so the resources inside stay
// reachable through the resource-group gate.
func (m *Mock) RestoreMissing(ctx context.Context) error {
	m.mu.RLock()
	source := m.source
	m.mu.RUnlock()

	if source == nil {
		return nil
	}

	refs, err := source(ctx)
	if err != nil {
		return fmt.Errorf("rgstore: rebuild groups: %w", err)
	}

	rebuilt := 0

	for _, ref := range refs {
		if ref.Subscription == "" || ref.Name == "" || m.Exists(ref.Subscription, ref.Name) {
			continue
		}

		loc := ref.Location
		if loc == "" {
			loc = defaultLocation
		}

		m.Put(ref.Subscription, ref.Name, map[string]any{
			"id":         "/subscriptions/" + ref.Subscription + "/resourceGroups/" + ref.Name,
			"name":       ref.Name,
			"type":       "Microsoft.Resources/resourceGroups",
			"location":   loc,
			"properties": map[string]any{"provisioningState": "Succeeded"},
		})

		rebuilt++
	}

	if rebuilt > 0 {
		log.Printf("rgstore: snapshot has no resource groups; rebuilt %d from restored resources", rebuilt)
	}

	return nil
}

// Snapshot captures every resource group keyed by lowercased subscription and
// name. includeAssets is unused: groups hold no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, err := m.store.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("rgstore: snapshot store: %w", err)
	}

	return data, nil
}

// Restore rebuilds every resource group under its original key.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(data) == 0 {
		return nil
	}

	if err := m.store.LoadSnapshot(data); err != nil {
		return fmt.Errorf("rgstore: restore store: %w", err)
	}

	return nil
}
