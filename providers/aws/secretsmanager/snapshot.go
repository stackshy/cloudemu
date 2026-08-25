package secretsmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// secretsSnapshot is the full serialized state of the Secrets Manager mock:
// every secret keyed by name, each with its metadata, version history, and
// soft-delete state, so identifiers (ARN, version ids) restore unchanged.
type secretsSnapshot struct {
	Secrets map[string]*secretSnapshot `json:"secrets,omitempty"`
}

type secretSnapshot struct {
	Info           driver.SecretInfo      `json:"info"`
	Versions       []driver.SecretVersion `json:"versions,omitempty"`
	DeletedAt      time.Time              `json:"deletedAt,omitempty"`
	RecoveryWindow int                    `json:"recoveryWindow,omitempty"`
}

// Snapshot captures every secret's full state as JSON. includeAssets is unused —
// a secret without its value cannot be restored usefully, so values are always
// captured.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := secretsSnapshot{Secrets: make(map[string]*secretSnapshot)}

	for name, sd := range m.secrets.All() {
		sd.mu.RLock()
		snap.Secrets[name] = &secretSnapshot{
			Info:           sd.info,
			Versions:       sd.versions,
			DeletedAt:      sd.deletedAt,
			RecoveryWindow: sd.recoveryWindow,
		}
		sd.mu.RUnlock()
	}

	return json.Marshal(snap)
}

// Restore rebuilds every secret under its original name with its metadata and
// version history intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap secretsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("secretsmanager: parse snapshot: %w", err)
	}

	for name, ss := range snap.Secrets {
		m.secrets.Set(name, &secretData{
			info:           ss.Info,
			versions:       ss.Versions,
			deletedAt:      ss.DeletedAt,
			recoveryWindow: ss.RecoveryWindow,
		})
	}

	return nil
}
