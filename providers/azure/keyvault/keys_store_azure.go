package keyvault

import (
	"context"
	"math/big"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// padLeft returns b left-padded with zeros to size bytes. Used for EC
// coordinates, which Key Vault returns at the curve's coordinate length.
func padLeft(b []byte, size int) []byte {
	if len(b) >= size {
		return b
	}

	out := make([]byte, size)
	copy(out[size-len(b):], b)

	return out
}

func encodeExponent(e int) []byte {
	return big.NewInt(int64(e)).Bytes()
}

// toKVKey projects a stored version onto the driver's public key view. Private
// components are never included.
func toKVKey(name string, v *keyVersion) driver.KVKey {
	kv := driver.KVKey{
		Name:      name,
		Version:   v.versionID,
		Kty:       v.kty,
		Curve:     v.curve,
		KeyOps:    append([]string(nil), v.keyOps...),
		Tags:      copyTags(v.tags),
		Enabled:   v.enabled,
		Expires:   v.expires,
		NotBefore: v.notBefore,
		Created:   v.created.Unix(),
		Updated:   v.updated.Unix(),
		Managed:   v.managed,
	}

	switch {
	case v.rsaKey != nil:
		kv.N = v.rsaKey.N.Bytes()
		kv.E = encodeExponent(v.rsaKey.E)
	case v.ecKey != nil:
		size := (v.ecKey.Curve.Params().BitSize + 7) / 8
		kv.X = padLeft(v.ecKey.X.Bytes(), size)
		kv.Y = padLeft(v.ecKey.Y.Bytes(), size)
	}

	return kv
}

// liveKey returns the stored key from store if it exists and is not
// soft-deleted.
func liveKey(store *memstore.Store[*keyData], name string) *keyData {
	kd, ok := store.Get(name)
	if !ok {
		return nil
	}

	kd.mu.RLock()
	deleted := !kd.deletedAt.IsZero()
	kd.mu.RUnlock()

	if deleted {
		return nil
	}

	return kd
}

func findKeyVersion(kd *keyData, version string) *keyVersion {
	for i := range kd.versions {
		v := &kd.versions[i]
		if version == "" && v.current {
			return v
		}

		if v.versionID == version {
			return v
		}
	}

	return nil
}

// GetKey returns one key version. Empty version returns the current version.
func (m *Mock) GetKey(_ context.Context, vault, name, version string) (*driver.KVKey, error) {
	kd := liveKey(m.vault(vault).keys, name)
	if kd == nil {
		return nil, errors.Newf(errors.NotFound, "key %q not found", name)
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	v := findKeyVersion(kd, version)
	if v == nil {
		return nil, errors.Newf(errors.NotFound, "version %q not found for key %q", version, name)
	}

	kv := toKVKey(name, v)

	return &kv, nil
}

// ListKeys returns the current version of each live key.
func (m *Mock) ListKeys(_ context.Context, vault string) ([]driver.KVKey, error) {
	all := m.vault(vault).keys.All()

	out := make([]driver.KVKey, 0, len(all))

	for _, kd := range all {
		kd.mu.RLock()
		if kd.deletedAt.IsZero() {
			if v := findKeyVersion(kd, ""); v != nil {
				out = append(out, toKVKey(kd.name, v))
			}
		}
		kd.mu.RUnlock()
	}

	return out, nil
}

// ListKeyVersions returns every version of a key.
func (m *Mock) ListKeyVersions(_ context.Context, vault, name string) ([]driver.KVKey, error) {
	kd := liveKey(m.vault(vault).keys, name)
	if kd == nil {
		return nil, errors.Newf(errors.NotFound, "key %q not found", name)
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	out := make([]driver.KVKey, len(kd.versions))
	for i := range kd.versions {
		out[i] = toKVKey(name, &kd.versions[i])
	}

	return out, nil
}

// UpdateKey patches a version's attributes, tags and key operations.
func (m *Mock) UpdateKey(_ context.Context, vault, name, version string, patch driver.KVKeyPatch) (*driver.KVKey, error) {
	kd := liveKey(m.vault(vault).keys, name)
	if kd == nil {
		return nil, errors.Newf(errors.NotFound, "key %q not found", name)
	}

	kd.mu.Lock()
	defer kd.mu.Unlock()

	v := findKeyVersion(kd, version)
	if v == nil {
		return nil, errors.Newf(errors.NotFound, "version %q not found for key %q", version, name)
	}

	applyKeyPatch(v, patch)
	v.updated = m.opts.Clock.Now().UTC()

	kv := toKVKey(name, v)

	return &kv, nil
}

func applyKeyPatch(v *keyVersion, patch driver.KVKeyPatch) {
	if patch.Enabled != nil {
		v.enabled = *patch.Enabled
	}

	if patch.Expires != nil {
		v.expires = *patch.Expires
	}

	if patch.NotBefore != nil {
		v.notBefore = *patch.NotBefore
	}

	if patch.SetTags {
		v.tags = copyTags(patch.Tags)
	}

	if patch.SetKeyOps {
		v.keyOps = append([]string(nil), patch.KeyOps...)
	}
}

// DeleteKey soft-deletes a key and returns its deleted view.
func (m *Mock) DeleteKey(_ context.Context, vault, name string) (*driver.KVDeletedKey, error) {
	kd := liveKey(m.vault(vault).keys, name)
	if kd == nil {
		return nil, errors.Newf(errors.NotFound, "key %q not found", name)
	}

	kd.mu.Lock()
	defer kd.mu.Unlock()

	now := m.opts.Clock.Now().UTC()
	kd.deletedAt = now
	kd.scheduledPurge = now.AddDate(0, 0, purgeWindowDays)

	return deletedKeyView(kd), nil
}

func deletedKeyView(kd *keyData) *driver.KVDeletedKey {
	v := findKeyVersion(kd, "")
	if v == nil && len(kd.versions) > 0 {
		v = &kd.versions[len(kd.versions)-1]
	}

	var kv driver.KVKey
	if v != nil {
		kv = toKVKey(kd.name, v)
	}

	return &driver.KVDeletedKey{
		KVKey:              kv,
		DeletedDate:        kd.deletedAt.Unix(),
		ScheduledPurgeDate: kd.scheduledPurge.Unix(),
	}
}

// GetDeletedKey returns a soft-deleted key by name.
func (m *Mock) GetDeletedKey(_ context.Context, vault, name string) (*driver.KVDeletedKey, error) {
	kd, ok := m.vault(vault).keys.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "deleted key %q not found", name)
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	if kd.deletedAt.IsZero() {
		return nil, errors.Newf(errors.NotFound, "deleted key %q not found", name)
	}

	return deletedKeyView(kd), nil
}

// ListDeletedKeys returns all soft-deleted keys.
func (m *Mock) ListDeletedKeys(_ context.Context, vault string) ([]driver.KVDeletedKey, error) {
	all := m.vault(vault).keys.All()

	out := make([]driver.KVDeletedKey, 0, len(all))

	for _, kd := range all {
		kd.mu.RLock()
		if !kd.deletedAt.IsZero() {
			out = append(out, *deletedKeyView(kd))
		}
		kd.mu.RUnlock()
	}

	return out, nil
}

// RecoverDeletedKey clears the soft-delete state of a key.
func (m *Mock) RecoverDeletedKey(_ context.Context, vault, name string) (*driver.KVKey, error) {
	kd, ok := m.vault(vault).keys.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "deleted key %q not found", name)
	}

	kd.mu.Lock()
	defer kd.mu.Unlock()

	if kd.deletedAt.IsZero() {
		return nil, errors.Newf(errors.NotFound, "deleted key %q not found", name)
	}

	kd.deletedAt = time.Time{}
	kd.scheduledPurge = time.Time{}

	v := findKeyVersion(kd, "")

	var kv driver.KVKey
	if v != nil {
		kv = toKVKey(name, v)
	}

	return &kv, nil
}

// PurgeDeletedKey permanently removes a soft-deleted key.
func (m *Mock) PurgeDeletedKey(_ context.Context, vault, name string) error {
	store := m.vault(vault).keys

	kd, ok := store.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "deleted key %q not found", name)
	}

	kd.mu.RLock()
	deleted := !kd.deletedAt.IsZero()
	kd.mu.RUnlock()

	if !deleted {
		return errors.Newf(errors.NotFound, "deleted key %q not found", name)
	}

	store.Delete(name)

	return nil
}

// GetKeyRotationPolicy returns name's rotation policy. A key that has never
// had a policy set returns Key Vault's empty default (no lifetime actions, no
// expiry) rather than an error.
func (m *Mock) GetKeyRotationPolicy(_ context.Context, vault, name string) (*driver.KVRotationPolicy, error) {
	kd := liveKey(m.vault(vault).keys, name)
	if kd == nil {
		return nil, errors.Newf(errors.NotFound, "key %q not found", name)
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	if kd.rotationPolicy == nil {
		return &driver.KVRotationPolicy{}, nil
	}

	policy := *kd.rotationPolicy

	return &policy, nil
}

// UpdateKeyRotationPolicy replaces name's rotation policy, preserving its
// original Created time (Key Vault semantics: Created is set once, Updated
// changes on every write).
func (m *Mock) UpdateKeyRotationPolicy(
	_ context.Context, vault, name string, policy driver.KVRotationPolicy,
) (*driver.KVRotationPolicy, error) {
	kd := liveKey(m.vault(vault).keys, name)
	if kd == nil {
		return nil, errors.Newf(errors.NotFound, "key %q not found", name)
	}

	kd.mu.Lock()
	defer kd.mu.Unlock()

	now := m.opts.Clock.Now().UTC().Unix()

	created := now
	if kd.rotationPolicy != nil {
		created = kd.rotationPolicy.Created
	}

	policy.Created = created
	policy.Updated = now
	kd.rotationPolicy = &policy

	stored := *kd.rotationPolicy

	return &stored, nil
}
