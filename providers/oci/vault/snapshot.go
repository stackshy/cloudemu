package vault

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// vaultSnapshot is the full serialized state of the OCI Vault mock. Every
// memstore store is dumped keyed by its resource OCID, so the cross-references
// that only exist as id strings — a key's VaultID, a secret's VaultID and
// KeyID, a key version's KeyID — still resolve after a restore. A secret's
// versions hang off the secret itself and travel with it, stages and all.
//
// The portable driver's default vault and key are captured alongside the
// stores: without them a restored mock would mint a second default vault on
// the next portable CreateSecret and orphan every secret it had restored.
//
// Every Vault value type has fully exported fields, so all four stores
// round-trip through the generic memstore helper; the mutex and *config.Options
// are not serialized.
type vaultSnapshot struct {
	Vaults      json.RawMessage `json:"vaults,omitempty"`
	Keys        json.RawMessage `json:"keys,omitempty"`
	KeyVersions json.RawMessage `json:"keyVersions,omitempty"`
	Secrets     json.RawMessage `json:"secrets,omitempty"`

	DefaultVaultID string `json:"defaultVaultId,omitempty"`
	DefaultKeyID   string `json:"defaultKeyId,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Vault holds no bulk object bodies; a secret's content is part of its version.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snap := vaultSnapshot{
		DefaultVaultID: m.defaultVaultID,
		DefaultKeyID:   m.defaultKeyID,
	}

	for _, d := range m.snapshotDumps(&snap) {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("vault: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: every OCID,
// the id-string cross-references between vaults, keys and secrets, each
// secret's version stages, and any scheduled deletion still pending.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap vaultSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("vault: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, d := range m.snapshotDumps(&snap) {
		if len(*d.dst) == 0 {
			continue
		}

		if err := d.load(*d.dst); err != nil {
			return fmt.Errorf("vault: restore store: %w", err)
		}
	}

	m.defaultVaultID = snap.DefaultVaultID
	m.defaultKeyID = snap.DefaultKeyID

	return nil
}

// storeDump pairs a snapshot field with its store's dump and load functions, so
// Snapshot and Restore share one table and cannot drift apart.
type storeDump struct {
	dst  *json.RawMessage
	fn   func() ([]byte, error)
	load func([]byte) error
}

// snapshotDumps lists every store alongside the snapshot field it maps to.
func (m *Mock) snapshotDumps(snap *vaultSnapshot) []storeDump {
	return []storeDump{
		{&snap.Vaults, m.vaults.Snapshot, m.vaults.LoadSnapshot},
		{&snap.Keys, m.keys.Snapshot, m.keys.LoadSnapshot},
		{&snap.KeyVersions, m.keyVersions.Snapshot, m.keyVersions.LoadSnapshot},
		{&snap.Secrets, m.secrets.Snapshot, m.secrets.LoadSnapshot},
	}
}
