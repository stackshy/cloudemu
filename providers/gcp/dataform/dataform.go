// Package dataform provides an in-memory mock of the Google Cloud Dataform
// control plane (dataform.googleapis.com/v1beta1). It models the region-scoped
// repository resource with synchronous REST CRUD (no long-running operations).
//
// Each repository's name (full resource path) and createTime are stable,
// server-assigned, output-only fields minted once at create and returned
// unchanged on every read so a Terraform refresh never drifts. The rich nested
// blocks gitRemoteSettings and workspaceCompilationOverrides are stored as raw
// JSON and echoed back verbatim.
package dataform

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	dfdriver "github.com/stackshy/cloudemu/v2/services/dataform/driver"
)

var _ dfdriver.Dataform = (*Mock)(nil)

const (
	repositoriesColl = "repositories"

	// createTimeFormat is the RFC 3339 layout Dataform stamps createTime with.
	createTimeFormat = time.RFC3339Nano

	// mask field paths a Patch honors (top-level replace).
	maskDisplayName    = "displayName"
	maskLabels         = "labels"
	maskServiceAccount = "serviceAccount"
	maskKmsKeyName     = "kmsKeyName"
	maskNpmrc          = "npmrcEnvironmentVariablesSecretVersion"
	maskGitRemote      = "gitRemoteSettings"
	maskWorkspaceOv    = "workspaceCompilationOverrides"
)

// Mock is the in-memory Dataform control-plane implementation. Repositories are
// keyed by their full GCP resource name.
type Mock struct {
	mu sync.RWMutex

	repositories *memstore.Store[dfdriver.Repository]

	opts *config.Options
}

// New creates a new Dataform mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		repositories: memstore.New[dfdriver.Repository](),
		opts:         opts,
	}
}

func repoName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + repositoriesColl + "/" + id
}

func notFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "repository %q not found", name)
}

// CreateRepository provisions a new repository with a stable name and createTime.
func (m *Mock) CreateRepository(_ context.Context, cfg *dfdriver.RepositoryConfig) (*dfdriver.Repository, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "repositoryId is required")
	}

	if cfg.Location == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "location is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := repoName(cfg.Project, cfg.Location, cfg.ID)
	if m.repositories.Has(key) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "repository %q already exists", key)
	}

	repo := repositoryFromConfig(cfg)
	repo.CreateTime = m.opts.Clock.Now().UTC().Format(createTimeFormat)
	m.repositories.Set(key, repo)

	out := cloneRepository(&repo)

	return &out, nil
}

// GetRepository returns a repository by identity, cloned.
func (m *Mock) GetRepository(_ context.Context, project, location, id string) (*dfdriver.Repository, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := repoName(project, location, id)

	repo, ok := m.repositories.Get(key)
	if !ok {
		return nil, notFound(key)
	}

	out := cloneRepository(&repo)

	return &out, nil
}

// ListRepositories returns every repository in a project+location, id-ordered.
func (m *Mock) ListRepositories(_ context.Context, project, location string) ([]dfdriver.Repository, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/locations/" + location + "/" + repositoriesColl + "/"
	all := m.repositories.SortedValues()
	out := make([]dfdriver.Repository, 0, len(all))

	for i := range all {
		if strings.HasPrefix(repoName(all[i].Project, all[i].Location, all[i].ID), prefix) {
			out = append(out, cloneRepository(&all[i]))
		}
	}

	return out, nil
}

// PatchRepository applies a masked update over the mutable fields. An empty mask
// replaces every mutable field from cfg (lenient full update). The name and
// createTime are immutable.
func (m *Mock) PatchRepository(_ context.Context, cfg *dfdriver.RepositoryConfig, mask []string) (*dfdriver.Repository, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := repoName(cfg.Project, cfg.Location, cfg.ID)

	repo, ok := m.repositories.Get(key)
	if !ok {
		return nil, notFound(key)
	}

	applyRepositoryMask(&repo, cfg, mask)
	m.repositories.Set(key, repo)

	out := cloneRepository(&repo)

	return &out, nil
}

// DeleteRepository removes a repository. The force flag (child cleanup) is a
// no-op here as the data plane is not emulated.
func (m *Mock) DeleteRepository(_ context.Context, project, location, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := repoName(project, location, id)
	if !m.repositories.Has(key) {
		return notFound(key)
	}

	m.repositories.Delete(key)

	return nil
}

// repositoryFromConfig builds a stored repository from a create config (without
// createTime, which the caller stamps).
func repositoryFromConfig(cfg *dfdriver.RepositoryConfig) dfdriver.Repository {
	return dfdriver.Repository{
		Project: cfg.Project, Location: cfg.Location, ID: cfg.ID,
		DisplayName:                            cfg.DisplayName,
		Labels:                                 cloneStrMap(cfg.Labels),
		ServiceAccount:                         cfg.ServiceAccount,
		KmsKeyName:                             cfg.KmsKeyName,
		NpmrcEnvironmentVariablesSecretVersion: cfg.NpmrcEnvironmentVariablesSecretVersion,
		GitRemoteSettings:                      cloneRaw(cfg.GitRemoteSettings),
		WorkspaceCompilationOverrides:          cloneRaw(cfg.WorkspaceCompilationOverrides),
	}
}

// applyRepositoryMask folds the masked mutable fields from cfg into repo. An
// empty mask replaces every mutable field (lenient full update).
func applyRepositoryMask(repo *dfdriver.Repository, cfg *dfdriver.RepositoryConfig, mask []string) {
	full := len(mask) == 0

	set := func(field string, apply func()) {
		if full || masked(mask, field) {
			apply()
		}
	}

	set(maskDisplayName, func() { repo.DisplayName = cfg.DisplayName })
	set(maskLabels, func() { repo.Labels = cloneStrMap(cfg.Labels) })
	set(maskServiceAccount, func() { repo.ServiceAccount = cfg.ServiceAccount })
	set(maskKmsKeyName, func() { repo.KmsKeyName = cfg.KmsKeyName })
	set(maskNpmrc, func() { repo.NpmrcEnvironmentVariablesSecretVersion = cfg.NpmrcEnvironmentVariablesSecretVersion })
	set(maskGitRemote, func() { repo.GitRemoteSettings = cloneRaw(cfg.GitRemoteSettings) })
	set(maskWorkspaceOv, func() { repo.WorkspaceCompilationOverrides = cloneRaw(cfg.WorkspaceCompilationOverrides) })
}

// masked reports whether field is targeted by the updateMask: an empty mask is a
// lenient full update (every field), otherwise the field must be listed (its
// leading path segment matching).
func masked(mask []string, field string) bool {
	if len(mask) == 0 {
		return true
	}

	for _, p := range mask {
		seg := p
		if i := strings.IndexByte(p, '.'); i >= 0 {
			seg = p[:i]
		}

		if seg == field {
			return true
		}
	}

	return false
}
