package kms

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// kmsSnapshot is the serialized Cloud KMS state. Versions carry their key
// material (AES/HMAC secrets and PKCS#8 DER private keys), so ciphertexts and
// signatures made before a snapshot still verify after a restore. The clock
// and the mutex are not serialized.
type kmsSnapshot struct {
	KeyRings json.RawMessage `json:"keyRings,omitempty"`
	Keys     json.RawMessage `json:"cryptoKeys,omitempty"`
	Versions json.RawMessage `json:"cryptoKeyVersions,omitempty"`
}

// Snapshot captures every key ring, crypto key and version. includeAssets is
// unused: key material is small and always needed to decrypt.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var snap kmsSnapshot

	for _, d := range []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.KeyRings, m.keyRings.Snapshot},
		{&snap.Keys, m.keys.Snapshot},
		{&snap.Versions, m.versions.Snapshot},
	} {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("kms: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return json.Marshal(snap)
}

// Restore rebuilds the key rings, crypto keys and versions under their
// original names. A section missing from data is left empty.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap kmsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("kms: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, l := range []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.KeyRings, m.keyRings.LoadSnapshot},
		{snap.Keys, m.keys.LoadSnapshot},
		{snap.Versions, m.versions.LoadSnapshot},
	} {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("kms: restore store: %w", err)
		}
	}

	return nil
}
