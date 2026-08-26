// Package secretmanager provides an in-memory mock implementation of GCP Secret Manager.
package secretmanager

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// Compile-time check that Mock implements driver.Secrets.
var _ driver.Secrets = (*Mock)(nil)

type secretData struct {
	info       driver.SecretInfo
	versions   []driver.SecretVersion
	verCounter int                  // monotonic version-number allocator (GCP numbers versions 1,2,3…)
	iam        *driver.GCPIAMPolicy // stored IAM policy; nil until first set
	mu         sync.RWMutex
}

// newEtag returns a fresh opaque optimistic-concurrency tag.
func newEtag() string {
	return idgen.GenerateID("etag-")
}

// resolveExpiry derives the RFC3339 expireTime from a ttl duration (e.g.
// "3600s") relative to now, or echoes an explicit expireTime. ttl takes
// precedence; an unparseable ttl is an invalid argument.
func resolveExpiry(now time.Time, ttl, expireTime string) (string, error) {
	if ttl != "" {
		d, err := time.ParseDuration(ttl)
		if err != nil {
			return "", errors.Newf(errors.InvalidArgument, "invalid ttl %q", ttl)
		}

		return now.Add(d).Format(time.RFC3339), nil
	}

	return expireTime, nil
}

func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}

	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}

	return out
}

func copySlice(s []string) []string {
	if s == nil {
		return nil
	}

	out := make([]string, len(s))
	copy(out, s)

	return out
}

func cloneReplication(rep *driver.GCPReplication) *driver.GCPReplication {
	if rep == nil {
		return nil
	}

	out := &driver.GCPReplication{Automatic: rep.Automatic, AutomaticKMSKeyName: rep.AutomaticKMSKeyName}
	if len(rep.UserManaged) > 0 {
		out.UserManaged = make([]driver.GCPReplica, len(rep.UserManaged))
		copy(out.UserManaged, rep.UserManaged)
	}

	return out
}

func cloneRotation(rot *driver.GCPRotation) *driver.GCPRotation {
	if rot == nil {
		return nil
	}

	clone := *rot

	return &clone
}

// resolveAlias maps a version alias to its target version id, leaving numeric
// ids and "" (current) untouched. Caller holds sd.mu.
func resolveAlias(sd *secretData, versionID string) string {
	if versionID == "" {
		return versionID
	}

	if target, ok := sd.info.VersionAliases[versionID]; ok {
		return target
	}

	return versionID
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

	expireTime, err := resolveExpiry(m.opts.Clock.Now().UTC(), cfg.TTL, cfg.ExpireTime)
	if err != nil {
		return nil, err
	}

	info := driver.SecretInfo{
		ID:             idgen.GenerateID("secret-"),
		Name:           cfg.Name,
		ResourceID:     selfLink,
		Description:    cfg.Description,
		CreatedAt:      now,
		UpdatedAt:      now,
		Tags:           tags,
		Etag:           newEtag(),
		Replication:    cloneReplication(cfg.Replication),
		Annotations:    copyMap(cfg.Annotations),
		ExpireTime:     expireTime,
		Rotation:       cloneRotation(cfg.Rotation),
		Topics:         copySlice(cfg.Topics),
		VersionAliases: copyMap(cfg.VersionAliases),
	}

	sd := &secretData{info: info}

	// GCP's secrets.create makes an empty container — the first version is added
	// separately via addVersion. Only seed a version when a value is actually
	// supplied (the AWS-style create-with-value path); otherwise the secret has
	// zero versions and access(latest) fails until one is added, matching GCP.
	if len(value) > 0 {
		data := make([]byte, len(value))
		copy(data, value)

		sd.verCounter++
		sd.versions = []driver.SecretVersion{{
			VersionID: strconv.Itoa(sd.verCounter),
			Value:     data,
			CreatedAt: now,
			Current:   true,
			State:     driver.VersionEnabled,
			Etag:      newEtag(),
		}}
	}

	m.secrets.Set(cfg.Name, sd)

	result := info

	return &result, nil
}

// DeleteSecret permanently removes a secret and all its versions. GCP Secret
// Manager's secrets.delete is a hard delete with no recovery window, so the same
// secretId is creatable again immediately.
func (m *Mock) DeleteSecret(_ context.Context, name string) error {
	if !m.secrets.Has(name) {
		return errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	m.secrets.Delete(name)

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

	result := sd.info

	return &result, nil
}

// ListSecrets lists all secrets.
func (m *Mock) ListSecrets(_ context.Context) ([]driver.SecretInfo, error) {
	all := m.secrets.All()

	secrets := make([]driver.SecretInfo, 0, len(all))

	for _, sd := range all {
		sd.mu.RLock()
		secrets = append(secrets, sd.info)
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

	now := m.opts.Clock.Now().UTC().Format(time.RFC3339)

	for i := range sd.versions {
		sd.versions[i].Current = false
	}

	data := make([]byte, len(value))
	copy(data, value)

	sd.verCounter++
	version := driver.SecretVersion{
		VersionID: strconv.Itoa(sd.verCounter),
		Value:     data,
		CreatedAt: now,
		Current:   true,
		State:     driver.VersionEnabled,
		Etag:      newEtag(),
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

	versionID = resolveAlias(sd, versionID)

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

	versions := make([]driver.SecretVersion, len(sd.versions))
	for i, v := range sd.versions {
		// Project metadata only — the payload is omitted from list results.
		versions[i] = driver.SecretVersion{
			VersionID:   v.VersionID,
			CreatedAt:   v.CreatedAt,
			Current:     v.Current,
			State:       v.State,
			DestroyTime: v.DestroyTime,
			Etag:        v.Etag,
		}
	}

	return versions, nil
}
