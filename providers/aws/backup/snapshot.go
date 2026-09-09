package backup

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/backup/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// backupSnapshot is the full serialized state of the AWS Backup mock. The stores
// hold exported driver types, so they serialize directly, keyed by vault name,
// plan id and selection id. The wired opts are not serialized.
type backupSnapshot struct {
	Vaults     map[string]driver.Vault     `json:"vaults,omitempty"`
	Plans      map[string]driver.Plan      `json:"plans,omitempty"`
	Selections map[string]driver.Selection `json:"selections,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// AWS Backup is control-plane only and holds no bulk assets.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := backupSnapshot{}

	if m.vaults.Len() > 0 {
		snap.Vaults = m.vaults.All()
	}

	if m.plans.Len() > 0 {
		snap.Plans = m.plans.All()
	}

	if m.selections.Len() > 0 {
		snap.Selections = m.selections.All()
	}

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("backup: snapshot: %w", err)
	}

	return b, nil
}

// Restore rebuilds the mock's state under the original identities: every vault
// name, plan id, VersionId and selection id (and the ARNs and computed fields
// derived from them) is preserved, so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap backupSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("backup: parse snapshot: %w", err)
	}

	for name := range snap.Vaults {
		m.vaults.Set(name, snap.Vaults[name])
	}

	for id := range snap.Plans {
		m.plans.Set(id, snap.Plans[id])
	}

	for id := range snap.Selections {
		m.selections.Set(id, snap.Selections[id])
	}

	return nil
}
