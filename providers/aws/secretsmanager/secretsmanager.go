// Package secretsmanager provides an in-memory mock implementation of AWS Secrets Manager.
package secretsmanager

import (
	"context"
	"sync"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	"github.com/stackshy/cloudemu/secretmanager/driver"
)

// Compile-time check that Mock implements driver.SecretManager.
var _ driver.SecretManager = (*Mock)(nil)

// versionEntry holds a single version of a secret value.
type versionEntry struct {
	Value     string
	Version   int
	CreatedAt string
}

// secretData holds the internal state of a single secret.
type secretData struct {
	name        string
	arn         string
	description string
	tags        map[string]string
	createdAt   string
	updatedAt   string
	deletedAt   string // non-empty means scheduled for deletion
	current     int    // current version number
	versions    []versionEntry
	mu          sync.RWMutex
}

// Mock is an in-memory mock implementation of the AWS Secrets Manager service.
type Mock struct {
	secrets *memstore.Store[*secretData]
	opts    *config.Options
}

// New creates a new Secrets Manager mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		secrets: memstore.New[*secretData](),
		opts:    opts,
	}
}

// CreateSecret creates a new secret with the given name and value.
func (m *Mock) CreateSecret(_ context.Context, cfg driver.SecretConfig) (*driver.SecretInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "secret name is required")
	}

	arn := idgen.AWSARN("secretsmanager", m.opts.Region, m.opts.AccountID, "secret:"+cfg.Name)
	now := m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z")

	tags := copyTags(cfg.Tags)

	sd := &secretData{
		name:        cfg.Name,
		arn:         arn,
		description: cfg.Description,
		tags:        tags,
		createdAt:   now,
		updatedAt:   now,
		current:     1,
		versions: []versionEntry{
			{Value: cfg.Value, Version: 1, CreatedAt: now},
		},
	}

	if !m.secrets.SetIfAbsent(cfg.Name, sd) {
		return nil, errors.Newf(errors.AlreadyExists, "secret %q already exists", cfg.Name)
	}

	return &driver.SecretInfo{
		Name:        cfg.Name,
		ARN:         arn,
		Description: cfg.Description,
		Version:     1,
		Tags:        copyTags(tags),
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// GetSecret retrieves the current version of a secret.
func (m *Mock) GetSecret(_ context.Context, name string) (*driver.SecretValue, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if sd.deletedAt != "" {
		return nil, errors.Newf(errors.FailedPrecondition, "secret %q is scheduled for deletion", name)
	}

	current := sd.versions[sd.current-1]

	return &driver.SecretValue{
		Name:    sd.name,
		ARN:     sd.arn,
		Value:   current.Value,
		Version: current.Version,
	}, nil
}

// UpdateSecret updates the value of an existing secret and auto-increments the version.
func (m *Mock) UpdateSecret(_ context.Context, name, value string) (*driver.SecretInfo, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.deletedAt != "" {
		return nil, errors.Newf(errors.FailedPrecondition, "secret %q is scheduled for deletion", name)
	}

	now := m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z")

	sd.current++
	sd.versions = append(sd.versions, versionEntry{Value: value, Version: sd.current, CreatedAt: now})
	sd.updatedAt = now

	return buildSecretInfo(sd), nil
}

// DeleteSecret schedules a secret for deletion. Use RestoreSecret to cancel.
func (m *Mock) DeleteSecret(_ context.Context, name string) error {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.deletedAt != "" {
		return errors.Newf(errors.FailedPrecondition, "secret %q is already scheduled for deletion", name)
	}

	sd.deletedAt = m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z")

	return nil
}

// RestoreSecret cancels a scheduled deletion and restores the secret.
func (m *Mock) RestoreSecret(_ context.Context, name string) (*driver.SecretInfo, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.deletedAt == "" {
		return nil, errors.Newf(errors.FailedPrecondition, "secret %q is not scheduled for deletion", name)
	}

	sd.deletedAt = ""

	return buildSecretInfo(sd), nil
}

// ListSecrets returns all non-deleted secrets (names and metadata only, no values).
func (m *Mock) ListSecrets(_ context.Context) ([]driver.SecretInfo, error) {
	all := m.secrets.All()

	results := make([]driver.SecretInfo, 0, len(all))

	for _, sd := range all {
		sd.mu.RLock()

		if sd.deletedAt == "" {
			results = append(results, driver.SecretInfo{
				Name:        sd.name,
				ARN:         sd.arn,
				Description: sd.description,
				Version:     sd.current,
				Tags:        copyTags(sd.tags),
				CreatedAt:   sd.createdAt,
				UpdatedAt:   sd.updatedAt,
			})
		}

		sd.mu.RUnlock()
	}

	return results, nil
}

// GetSecretVersion retrieves a specific version of a secret.
func (m *Mock) GetSecretVersion(_ context.Context, name string, version int) (*driver.SecretValue, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if sd.deletedAt != "" {
		return nil, errors.Newf(errors.FailedPrecondition, "secret %q is scheduled for deletion", name)
	}

	if version < 1 || version > len(sd.versions) {
		return nil, errors.Newf(errors.NotFound, "secret %q version %d not found", name, version)
	}

	entry := sd.versions[version-1]

	return &driver.SecretValue{
		Name:    sd.name,
		ARN:     sd.arn,
		Value:   entry.Value,
		Version: entry.Version,
	}, nil
}

// RotateSecret simulates secret rotation by invoking the callback with the current value
// and storing the returned value as a new version.
func (m *Mock) RotateSecret(_ context.Context, name string, callback driver.RotateCallback) (*driver.SecretInfo, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()

	if sd.deletedAt != "" {
		sd.mu.Unlock()
		return nil, errors.Newf(errors.FailedPrecondition, "secret %q is scheduled for deletion", name)
	}

	currentValue := sd.versions[sd.current-1].Value
	sd.mu.Unlock()

	newValue, err := callback(currentValue)
	if err != nil {
		return nil, errors.Newf(errors.Internal, "rotation callback failed: %v", err)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	now := m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z")

	sd.current++
	sd.versions = append(sd.versions, versionEntry{Value: newValue, Version: sd.current, CreatedAt: now})
	sd.updatedAt = now

	return buildSecretInfo(sd), nil
}

// DescribeSecret returns metadata about a secret without the value.
func (m *Mock) DescribeSecret(_ context.Context, name string) (*driver.SecretInfo, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	return buildSecretInfo(sd), nil
}

// TagResource adds or updates tags on an existing secret.
func (m *Mock) TagResource(_ context.Context, name string, tags map[string]string) error {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.deletedAt != "" {
		return errors.Newf(errors.FailedPrecondition, "secret %q is scheduled for deletion", name)
	}

	for k, v := range tags {
		sd.tags[k] = v
	}

	return nil
}

// UntagResource removes tags from an existing secret by key.
func (m *Mock) UntagResource(_ context.Context, name string, tagKeys []string) error {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.deletedAt != "" {
		return errors.Newf(errors.FailedPrecondition, "secret %q is scheduled for deletion", name)
	}

	for _, key := range tagKeys {
		delete(sd.tags, key)
	}

	return nil
}

// ListSecretVersions returns all version metadata for a secret.
func (m *Mock) ListSecretVersions(_ context.Context, name string) ([]driver.VersionInfo, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	results := make([]driver.VersionInfo, len(sd.versions))

	for i, v := range sd.versions {
		results[i] = driver.VersionInfo{
			Version:   v.Version,
			CreatedAt: v.CreatedAt,
		}
	}

	return results, nil
}

// buildSecretInfo creates a SecretInfo from internal secretData. Caller must hold at least a read lock.
func buildSecretInfo(sd *secretData) *driver.SecretInfo {
	return &driver.SecretInfo{
		Name:        sd.name,
		ARN:         sd.arn,
		Description: sd.description,
		Version:     sd.current,
		Tags:        copyTags(sd.tags),
		CreatedAt:   sd.createdAt,
		UpdatedAt:   sd.updatedAt,
		DeletedAt:   sd.deletedAt,
	}
}

// copyTags creates a shallow copy of a tag map.
func copyTags(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}

	return dst
}
