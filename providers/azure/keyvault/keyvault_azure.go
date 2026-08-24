package keyvault

import (
	"context"
	"encoding/json"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

func (v *secretVersion) toKV(name string) driver.KVSecret {
	return driver.KVSecret{
		Name:        name,
		Version:     v.versionID,
		Value:       copyBytes(v.value),
		ContentType: v.contentType,
		Tags:        copyTags(v.tags),
		Enabled:     v.enabled,
		Expires:     v.expires,
		NotBefore:   v.notBefore,
		Created:     v.created.Unix(),
		Updated:     v.updated.Unix(),
		Current:     v.current,
	}
}

// SetKeyVaultSecret creates the secret on first call and appends a new version
// on subsequent calls, carrying content type, tags and attributes.
func (m *Mock) SetKeyVaultSecret(_ context.Context, name string, params driver.KVSetParams) (*driver.KVSecret, error) {
	if name == "" {
		return nil, errors.New(errors.InvalidArgument, "secret name is required")
	}

	if sd, ok := m.secrets.Get(name); ok {
		sd.mu.Lock()
		defer sd.mu.Unlock()

		if !sd.deletedAt.IsZero() {
			return nil, errors.Newf(errors.AlreadyExists, "secret %q is in a deleted but recoverable state", name)
		}

		v := m.appendVersionLocked(sd, params)
		kv := v.toKV(name)

		return &kv, nil
	}

	now := m.opts.Clock.Now().UTC()
	resourceID := idgen.AzureID(m.opts.AccountID, "rg-default", "Microsoft.KeyVault", "vaults/default/secrets", name)

	v := secretVersion{
		versionID:   idgen.GenerateID("ver-"),
		value:       copyBytes(params.Value),
		contentType: params.ContentType,
		tags:        copyTags(params.Tags),
		enabled:     params.Attributes.Enabled,
		expires:     params.Attributes.Expires,
		notBefore:   params.Attributes.NotBefore,
		created:     now,
		updated:     now,
		current:     true,
	}

	sd := &secretData{
		info: driver.SecretInfo{
			ID:         idgen.GenerateID("secret-"),
			Name:       name,
			ResourceID: resourceID,
			CreatedAt:  now.Format(time.RFC3339),
			UpdatedAt:  now.Format(time.RFC3339),
			Tags:       copyTags(params.Tags),
		},
		versions: []secretVersion{v},
	}

	m.secrets.Set(name, sd)

	kv := v.toKV(name)

	return &kv, nil
}

// GetKeyVaultSecret returns one secret version. Empty version returns current.
func (m *Mock) GetKeyVaultSecret(_ context.Context, name, version string) (*driver.KVSecret, error) {
	sd := m.liveSecret(name)
	if sd == nil {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	v := findVersion(sd, version)
	if v == nil {
		return nil, errors.Newf(errors.NotFound, "version %q not found for secret %q", version, name)
	}

	kv := v.toKV(name)

	return &kv, nil
}

// ListKeyVaultSecrets returns the current version of each live secret.
func (m *Mock) ListKeyVaultSecrets(_ context.Context) ([]driver.KVSecret, error) {
	all := m.secrets.All()

	out := make([]driver.KVSecret, 0, len(all))

	for _, sd := range all {
		sd.mu.RLock()
		if sd.deletedAt.IsZero() {
			if v := findVersion(sd, ""); v != nil {
				kv := v.toKV(sd.info.Name)
				kv.Value = nil
				out = append(out, kv)
			}
		}
		sd.mu.RUnlock()
	}

	return out, nil
}

// ListKeyVaultSecretVersions returns every version of a secret (no values).
func (m *Mock) ListKeyVaultSecretVersions(_ context.Context, name string) ([]driver.KVSecret, error) {
	sd := m.liveSecret(name)
	if sd == nil {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	out := make([]driver.KVSecret, len(sd.versions))

	for i := range sd.versions {
		kv := sd.versions[i].toKV(name)
		kv.Value = nil
		out[i] = kv
	}

	return out, nil
}

// UpdateKeyVaultSecret patches a version's attributes, tags and content type.
func (m *Mock) UpdateKeyVaultSecret(_ context.Context, name, version string, patch driver.KVPatch) (*driver.KVSecret, error) {
	sd := m.liveSecret(name)
	if sd == nil {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	v := findVersion(sd, version)
	if v == nil {
		return nil, errors.Newf(errors.NotFound, "version %q not found for secret %q", version, name)
	}

	applyPatch(v, patch)
	v.updated = m.opts.Clock.Now().UTC()

	if v.current {
		sd.info.UpdatedAt = v.updated.Format(time.RFC3339)
		sd.info.Tags = copyTags(v.tags)
	}

	kv := v.toKV(name)

	return &kv, nil
}

func applyPatch(v *secretVersion, patch driver.KVPatch) {
	if patch.ContentType != nil {
		v.contentType = *patch.ContentType
	}

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
}

// DeleteKeyVaultSecret soft-deletes a secret and returns its deleted view.
func (m *Mock) DeleteKeyVaultSecret(_ context.Context, name string) (*driver.KVDeletedSecret, error) {
	sd := m.liveSecret(name)
	if sd == nil {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	m.markDeletedLocked(sd)

	return deletedView(sd), nil
}

func deletedView(sd *secretData) *driver.KVDeletedSecret {
	v := findVersion(sd, "")
	if v == nil && len(sd.versions) > 0 {
		v = &sd.versions[len(sd.versions)-1]
	}

	var kv driver.KVSecret
	if v != nil {
		kv = v.toKV(sd.info.Name)
	}

	return &driver.KVDeletedSecret{
		KVSecret:           kv,
		DeletedDate:        sd.deletedAt.Unix(),
		ScheduledPurgeDate: sd.scheduledPurge.Unix(),
	}
}

// GetDeletedKeyVaultSecret returns a soft-deleted secret by name.
func (m *Mock) GetDeletedKeyVaultSecret(_ context.Context, name string) (*driver.KVDeletedSecret, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "deleted secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if sd.deletedAt.IsZero() {
		return nil, errors.Newf(errors.NotFound, "deleted secret %q not found", name)
	}

	return deletedView(sd), nil
}

// ListDeletedKeyVaultSecrets returns all soft-deleted secrets.
func (m *Mock) ListDeletedKeyVaultSecrets(_ context.Context) ([]driver.KVDeletedSecret, error) {
	all := m.secrets.All()

	out := make([]driver.KVDeletedSecret, 0, len(all))

	for _, sd := range all {
		sd.mu.RLock()
		if !sd.deletedAt.IsZero() {
			out = append(out, *deletedView(sd))
		}
		sd.mu.RUnlock()
	}

	return out, nil
}

// RecoverDeletedKeyVaultSecret clears the soft-delete state of a secret.
func (m *Mock) RecoverDeletedKeyVaultSecret(_ context.Context, name string) (*driver.KVSecret, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "deleted secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.deletedAt.IsZero() {
		return nil, errors.Newf(errors.NotFound, "deleted secret %q not found", name)
	}

	sd.deletedAt = time.Time{}
	sd.scheduledPurge = time.Time{}

	v := findVersion(sd, "")

	var kv driver.KVSecret
	if v != nil {
		kv = v.toKV(name)
	}

	return &kv, nil
}

// PurgeDeletedKeyVaultSecret permanently removes a soft-deleted secret.
func (m *Mock) PurgeDeletedKeyVaultSecret(_ context.Context, name string) error {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "deleted secret %q not found", name)
	}

	sd.mu.RLock()
	deleted := !sd.deletedAt.IsZero()
	sd.mu.RUnlock()

	if !deleted {
		return errors.Newf(errors.NotFound, "deleted secret %q not found", name)
	}

	m.secrets.Delete(name)

	return nil
}

type backupVersion struct {
	VersionID   string            `json:"versionId"`
	Value       []byte            `json:"value"`
	ContentType string            `json:"contentType"`
	Tags        map[string]string `json:"tags"`
	Enabled     bool              `json:"enabled"`
	Expires     int64             `json:"expires"`
	NotBefore   int64             `json:"notBefore"`
	Created     time.Time         `json:"created"`
	Updated     time.Time         `json:"updated"`
	Current     bool              `json:"current"`
}

type backupSnapshot struct {
	Name     string            `json:"name"`
	Tags     map[string]string `json:"tags"`
	Versions []backupVersion   `json:"versions"`
}

// BackupKeyVaultSecret returns an opaque blob capturing every version.
func (m *Mock) BackupKeyVaultSecret(_ context.Context, name string) ([]byte, error) {
	sd := m.liveSecret(name)
	if sd == nil {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	snap := backupSnapshot{Name: name, Tags: copyTags(sd.info.Tags)}

	for i := range sd.versions {
		v := &sd.versions[i]
		snap.Versions = append(snap.Versions, backupVersion{
			VersionID: v.versionID, Value: copyBytes(v.value), ContentType: v.contentType,
			Tags: copyTags(v.tags), Enabled: v.enabled, Expires: v.expires, NotBefore: v.notBefore,
			Created: v.created, Updated: v.updated, Current: v.current,
		})
	}

	return json.Marshal(snap)
}

// RestoreKeyVaultSecret recreates a secret from a backup blob.
func (m *Mock) RestoreKeyVaultSecret(_ context.Context, backup []byte) (*driver.KVSecret, error) {
	var snap backupSnapshot
	if err := json.Unmarshal(backup, &snap); err != nil {
		return nil, errors.New(errors.InvalidArgument, "invalid secret backup blob")
	}

	if snap.Name == "" || len(snap.Versions) == 0 {
		return nil, errors.New(errors.InvalidArgument, "invalid secret backup blob")
	}

	if _, ok := m.secrets.Get(snap.Name); ok {
		return nil, errors.Newf(errors.AlreadyExists, "secret %q already exists", snap.Name)
	}

	now := m.opts.Clock.Now().UTC()
	sd := &secretData{
		info: driver.SecretInfo{
			ID:         idgen.GenerateID("secret-"),
			Name:       snap.Name,
			ResourceID: idgen.AzureID(m.opts.AccountID, "rg-default", "Microsoft.KeyVault", "vaults/default/secrets", snap.Name),
			CreatedAt:  now.Format(time.RFC3339),
			UpdatedAt:  now.Format(time.RFC3339),
			Tags:       copyTags(snap.Tags),
		},
	}

	for i := range snap.Versions {
		bv := &snap.Versions[i]
		sd.versions = append(sd.versions, secretVersion{
			versionID: bv.VersionID, value: copyBytes(bv.Value), contentType: bv.ContentType,
			tags: copyTags(bv.Tags), enabled: bv.Enabled, expires: bv.Expires, notBefore: bv.NotBefore,
			created: bv.Created, updated: bv.Updated, current: bv.Current,
		})
	}

	m.secrets.Set(snap.Name, sd)

	v := findVersion(sd, "")

	var kv driver.KVSecret
	if v != nil {
		kv = v.toKV(snap.Name)
	}

	return &kv, nil
}
