package kms

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// kmsSnapshot is the full serialized state of the AWS KMS mock. keys and aliases
// hold unexported value types (with unexported fields, and — for a key — an
// asymmetric private key), so both are promoted to exported snapshot forms keyed
// by their id; grants holds a fully-exported *driver.Grant and round-trips
// through the generic memstore helper. The per-key mutex, and the in-flight
// import-session state (importWrappingKey/importToken, minted by
// GetParametersForImport and consumed by the immediately-following
// ImportKeyMaterial), are intentionally not serialized.
type kmsSnapshot struct {
	Keys    map[string]*keySnapshot   `json:"keys,omitempty"`
	Aliases map[string]*aliasSnapshot `json:"aliases,omitempty"`
	Grants  json.RawMessage           `json:"grants,omitempty"`
}

// keySnapshot mirrors keyData. The asymmetric private key is serialized as
// PKCS#8 DER so RSA/ECC keys survive a restore and can still sign/decrypt; the
// symmetric/HMAC material rounds through Materials.
type keySnapshot struct {
	Meta               driver.KeyMetadata     `json:"meta"`
	Tags               map[string]string      `json:"tags,omitempty"`
	Materials          [][]byte               `json:"materials,omitempty"`
	PrivKeyPKCS8       []byte                 `json:"privKeyPkcs8,omitempty"`
	Policies           map[string]string      `json:"policies,omitempty"`
	RotationEnabled    bool                   `json:"rotationEnabled,omitempty"`
	RotationPeriodDays int32                  `json:"rotationPeriodDays,omitempty"`
	Rotations          []driver.RotationEvent `json:"rotations,omitempty"`
	OnDemandCount      int                    `json:"onDemandCount,omitempty"`
}

// aliasSnapshot mirrors aliasData (all fields unexported).
type aliasSnapshot struct {
	Name        string    `json:"name"`
	ARN         string    `json:"arn,omitempty"`
	TargetKeyID string    `json:"targetKeyId,omitempty"`
	Created     time.Time `json:"created,omitempty"`
	Updated     time.Time `json:"updated,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// key material is always captured (a metadata-only key could not decrypt).
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	keys, err := m.snapshotKeys()
	if err != nil {
		return nil, err
	}

	snap := kmsSnapshot{Keys: keys, Aliases: m.snapshotAliases()}

	grants, err := m.grants.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("kms: snapshot grants: %w", err)
	}

	snap.Grants = grants

	return json.Marshal(snap)
}

func (m *Mock) snapshotKeys() (map[string]*keySnapshot, error) {
	if m.keys.Len() == 0 {
		return nil, nil
	}

	out := make(map[string]*keySnapshot, m.keys.Len())

	for id, kd := range m.keys.All() {
		kd.mu.RLock()
		ks := &keySnapshot{
			Meta: kd.meta, Tags: kd.tags, Materials: kd.materials, Policies: kd.policies,
			RotationEnabled: kd.rotationEnabled, RotationPeriodDays: kd.rotationPeriodDays,
			Rotations: kd.rotations, OnDemandCount: kd.onDemandCount,
		}

		if kd.privKey != nil {
			der, err := x509.MarshalPKCS8PrivateKey(kd.privKey)
			if err != nil {
				kd.mu.RUnlock()
				return nil, fmt.Errorf("kms: marshal private key %q: %w", id, err)
			}

			ks.PrivKeyPKCS8 = der
		}
		kd.mu.RUnlock()

		out[id] = ks
	}

	return out, nil
}

func (m *Mock) snapshotAliases() map[string]*aliasSnapshot {
	if m.aliases.Len() == 0 {
		return nil
	}

	out := make(map[string]*aliasSnapshot, m.aliases.Len())

	for id, a := range m.aliases.All() {
		out[id] = &aliasSnapshot{
			Name: a.name, ARN: a.arn, TargetKeyID: a.targetKeyID,
			Created: a.created, Updated: a.updated,
		}
	}

	return out
}

// Restore rebuilds the mock's state under the original identities: every key id,
// alias name, and grant id is preserved, and asymmetric keys are re-parsed so
// they still sign/decrypt.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap kmsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("kms: parse snapshot: %w", err)
	}

	if err := m.restoreKeys(snap.Keys); err != nil {
		return err
	}

	m.restoreAliases(snap.Aliases)

	if len(snap.Grants) > 0 {
		if err := m.grants.LoadSnapshot(snap.Grants); err != nil {
			return fmt.Errorf("kms: restore grants: %w", err)
		}
	}

	return nil
}

func (m *Mock) restoreKeys(keys map[string]*keySnapshot) error {
	for id, ks := range keys {
		kd := &keyData{
			meta: ks.Meta, tags: ks.Tags, materials: ks.Materials, policies: ks.Policies,
			rotationEnabled: ks.RotationEnabled, rotationPeriodDays: ks.RotationPeriodDays,
			rotations: ks.Rotations, onDemandCount: ks.OnDemandCount,
		}

		if len(ks.PrivKeyPKCS8) > 0 {
			pk, err := x509.ParsePKCS8PrivateKey(ks.PrivKeyPKCS8)
			if err != nil {
				return fmt.Errorf("kms: parse private key %q: %w", id, err)
			}

			kd.privKey = pk
		}

		m.keys.Set(id, kd)
	}

	return nil
}

func (m *Mock) restoreAliases(aliases map[string]*aliasSnapshot) {
	for id, as := range aliases {
		ad := &aliasData{
			name: as.Name, arn: as.ARN, targetKeyID: as.TargetKeyID,
			created: as.Created, updated: as.Updated,
		}

		m.aliases.Set(id, ad)
	}
}
