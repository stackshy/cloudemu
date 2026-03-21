// Package secretmanager provides an in-memory mock implementation of GCP Secret Manager.
package secretmanager

import (
	"context"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	"github.com/stackshy/cloudemu/secrets/driver"
)

// Compile-time check that Mock implements driver.Secrets.
var _ driver.Secrets = (*Mock)(nil)

type secretData struct {
	info      driver.SecretInfo
	versions  []driver.SecretVersion
	deletedAt time.Time
	mu        sync.RWMutex
}

// Mock is an in-memory mock implementation of GCP Secret Manager.
type Mock struct {
	secrets *memstore.Store[*secretData]
	opts    *config.Options
}

// New creates a new Secret Manager mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		secrets: memstore.New[*secretData](),
		opts:    opts,
	}
}

// CreateSecret creates a new secret with an initial value.
func (m *Mock) CreateSecret(_ context.Context, cfg driver.SecretConfig, value []byte) (*driver.SecretInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "secret name is required")
	}

	if m.secrets.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "secret %q already exists", cfg.Name)
	}

	now := m.opts.Clock.Now().UTC().Format(time.RFC3339)
	selfLink := idgen.GCPID(m.opts.ProjectID, "secrets", cfg.Name)

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	info := driver.SecretInfo{
		ID:          idgen.GenerateID("secret-"),
		Name:        cfg.Name,
		ResourceID:  selfLink,
		Description: cfg.Description,
		CreatedAt:   now,
		UpdatedAt:   now,
		Tags:        tags,
	}

	data := make([]byte, len(value))
	copy(data, value)

	versionID := idgen.GenerateID("ver-")
	version := driver.SecretVersion{
		VersionID: versionID,
		Value:     data,
		CreatedAt: now,
		Current:   true,
	}

	sd := &secretData{
		info:     info,
		versions: []driver.SecretVersion{version},
	}

	m.secrets.Set(cfg.Name, sd)

	result := info

	return &result, nil
}

// DeleteSecret soft-deletes a secret by name, scheduling it for deletion after a recovery window.
func (m *Mock) DeleteSecret(_ context.Context, name string) error {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if !sd.deletedAt.IsZero() {
		return errors.Newf(errors.NotFound, "secret %q is scheduled for deletion", name)
	}

	sd.deletedAt = m.opts.Clock.Now().UTC()

	return nil
}

// GetSecret retrieves secret metadata by name.
func (m *Mock) GetSecret(_ context.Context, name string) (*driver.SecretInfo, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if !sd.deletedAt.IsZero() {
		return nil, errors.Newf(errors.NotFound, "secret %q is scheduled for deletion", name)
	}

	result := sd.info

	return &result, nil
}

// ListSecrets lists all secrets, excluding soft-deleted ones.
func (m *Mock) ListSecrets(_ context.Context) ([]driver.SecretInfo, error) {
	all := m.secrets.All()

	secrets := make([]driver.SecretInfo, 0, len(all))

	for _, sd := range all {
		sd.mu.RLock()
		if sd.deletedAt.IsZero() {
			secrets = append(secrets, sd.info)
		}
		sd.mu.RUnlock()
	}

	return secrets, nil
}

// PutSecretValue stores a new version of a secret value.
func (m *Mock) PutSecretValue(_ context.Context, name string, value []byte) (*driver.SecretVersion, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if !sd.deletedAt.IsZero() {
		return nil, errors.Newf(errors.NotFound, "secret %q is scheduled for deletion", name)
	}

	now := m.opts.Clock.Now().UTC().Format(time.RFC3339)

	for i := range sd.versions {
		sd.versions[i].Current = false
	}

	data := make([]byte, len(value))
	copy(data, value)

	versionID := idgen.GenerateID("ver-")
	version := driver.SecretVersion{
		VersionID: versionID,
		Value:     data,
		CreatedAt: now,
		Current:   true,
	}

	sd.versions = append(sd.versions, version)
	sd.info.UpdatedAt = now

	result := version

	return &result, nil
}

// GetSecretValue retrieves a secret value. Empty versionID returns the current version.
func (m *Mock) GetSecretValue(_ context.Context, name, versionID string) (*driver.SecretVersion, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if !sd.deletedAt.IsZero() {
		return nil, errors.Newf(errors.NotFound, "secret %q is scheduled for deletion", name)
	}

	for _, v := range sd.versions {
		if versionID == "" && v.Current {
			result := v

			data := make([]byte, len(v.Value))
			copy(data, v.Value)
			result.Value = data

			return &result, nil
		}

		if v.VersionID == versionID {
			result := v

			data := make([]byte, len(v.Value))
			copy(data, v.Value)
			result.Value = data

			return &result, nil
		}
	}

	return nil, errors.Newf(errors.NotFound, "version %q not found for secret %q", versionID, name)
}

// ListSecretVersions lists all versions of a secret.
func (m *Mock) ListSecretVersions(_ context.Context, name string) ([]driver.SecretVersion, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if !sd.deletedAt.IsZero() {
		return nil, errors.Newf(errors.NotFound, "secret %q is scheduled for deletion", name)
	}

	versions := make([]driver.SecretVersion, len(sd.versions))
	for i, v := range sd.versions {
		versions[i] = driver.SecretVersion{
			VersionID: v.VersionID,
			CreatedAt: v.CreatedAt,
			Current:   v.Current,
		}
	}

	return versions, nil
}
