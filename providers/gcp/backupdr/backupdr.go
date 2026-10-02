// Package backupdr provides an in-memory mock of the Google Cloud Backup and DR
// control plane (backupdr.googleapis.com/v1). It models backup vaults and the
// long-running operations their mutating RPCs return. It is control-plane only:
// there are no data sources, backups, backup plans or restores, so every vault
// is empty (backupCount 0) unless its state is restored from a snapshot that
// says otherwise.
package backupdr

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	bdrdriver "github.com/stackshy/cloudemu/v2/services/backupdr/driver"
)

var _ bdrdriver.BackupDR = (*Mock)(nil)

const (
	vaultsColl = "backupVaults"

	// anyLocation is the list wildcard real Backup and DR accepts in place of a
	// location ("projects/p/locations/-") to list across every location.
	anyLocation = "-"

	opCreate = "create"
	opUpdate = "update"
	opDelete = "delete"
)

// Mock is the in-memory Backup and DR control-plane implementation. Each vault
// is keyed by its full GCP resource name.
type Mock struct {
	mu sync.RWMutex

	vaults     *memstore.Store[bdrdriver.BackupVault]
	operations *memstore.Store[bdrdriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Backup and DR mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		vaults:     memstore.New[bdrdriver.BackupVault](),
		operations: memstore.New[bdrdriver.Operation](),
		opts:       opts,
	}
}

// resourceName builds the full backup vault resource name.
func resourceName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + vaultsColl + "/" + id
}

// newOp records a completed operation scoped to the project+location it acted in
// and returns it. The caller holds the write lock.
//
// A validateOnly request performs no mutation, so it mints no operation id and
// records nothing: it returns a done operation with an empty name, which an LRO
// client resolves from the inline response without polling.
func (m *Mock) newOp(project, location, opType, target string, validateOnly bool) *bdrdriver.Operation {
	if validateOnly {
		return &bdrdriver.Operation{Done: true, TargetName: target, Type: opType}
	}

	scope := "projects/" + project + "/locations/" + location
	op := bdrdriver.Operation{
		Name:       fmt.Sprintf("%s/operations/operation-%d-%s", scope, m.opSeq.Add(1), idgen.UUID()),
		Done:       true,
		TargetName: target,
		Type:       opType,
	}
	m.operations.Set(op.Name, op)

	return &op
}

// CreateBackupVault validates the request, mints the output-only fields (state
// ACTIVE, deterministic serviceAccount, uid, etag, zero usage, timestamps) and
// returns the completed LRO. With ValidateOnly nothing is stored.
func (m *Mock) CreateBackupVault(_ context.Context, cfg *bdrdriver.BackupVaultConfig) (
	*bdrdriver.BackupVault, *bdrdriver.Operation, error,
) {
	if err := validateCreate(cfg); err != nil {
		return nil, nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(cfg.Project, cfg.Location, cfg.ID)
	if m.vaults.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "backup vault %q already exists", key)
	}

	now := m.opts.Clock.Now().UTC()
	v := bdrdriver.BackupVault{
		Project:                                cfg.Project,
		Location:                               cfg.Location,
		ID:                                     cfg.ID,
		Description:                            cfg.Description,
		Labels:                                 cloneStrMap(cfg.Labels),
		Annotations:                            cloneStrMap(cfg.Annotations),
		BackupMinimumEnforcedRetentionDuration: cfg.BackupMinimumEnforcedRetentionDuration,
		BackupRetentionInheritance:             cfg.BackupRetentionInheritance,
		EffectiveTime:                          cfg.EffectiveTime,
		AccessRestriction:                      defaultAccessRestriction(cfg.AccessRestriction),
		EncryptionConfig:                       cloneEncryption(cfg.EncryptionConfig),
		State:                                  stateActive,
		ServiceAccount:                         serviceAccount(cfg.Project),
		UID:                                    idgen.UUID(),
		Revision:                               1,
		CreateTime:                             now,
		UpdateTime:                             now,
	}
	v.Etag = etagFor(&v)

	if !cfg.ValidateOnly {
		m.vaults.Set(key, v)
	}

	op := m.newOp(cfg.Project, cfg.Location, opCreate, key, cfg.ValidateOnly)
	out := cloneVault(&v)

	return &out, op, nil
}

// GetBackupVault returns a vault by identity, cloned.
func (m *Mock) GetBackupVault(_ context.Context, project, location, id string) (*bdrdriver.BackupVault, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	v, ok := m.vaults.Get(resourceName(project, location, id))
	if !ok {
		return nil, notFoundErr(project, location, id)
	}

	out := cloneVault(&v)

	return &out, nil
}

// ListBackupVaults returns every vault in a project+location (or every location
// for "-"), ordered by resource name.
func (m *Mock) ListBackupVaults(_ context.Context, project, location string) ([]bdrdriver.BackupVault, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.vaults.SortedValues()
	out := make([]bdrdriver.BackupVault, 0, len(all))

	for i := range all {
		if all[i].Project != project || (location != anyLocation && all[i].Location != location) {
			continue
		}

		out = append(out, cloneVault(&all[i]))
	}

	return out, nil
}

// UpdateBackupVault applies a field-masked update. The mask is required and may
// only name mutable fields; a supplied etag must match. Every successful update
// bumps the revision, rotates the etag and advances updateTime. With
// ValidateOnly nothing is stored.
func (m *Mock) UpdateBackupVault(_ context.Context, cfg *bdrdriver.BackupVaultConfig, mask []string) (
	*bdrdriver.BackupVault, *bdrdriver.Operation, error,
) {
	fields, err := normalizeMask(mask)
	if err != nil {
		return nil, nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(cfg.Project, cfg.Location, cfg.ID)

	v, ok := m.vaults.Get(key)
	if !ok {
		return nil, nil, notFoundErr(cfg.Project, cfg.Location, cfg.ID)
	}

	if cfg.Etag != "" && cfg.Etag != v.Etag {
		return nil, nil, fmt.Errorf("backup vault %q: %w", key, bdrdriver.ErrEtagMismatch)
	}

	now := m.opts.Clock.Now().UTC()

	if err := applyMask(&v, cfg, fields, now); err != nil {
		return nil, nil, err
	}

	v.Revision++
	v.UpdateTime = now
	v.Etag = etagFor(&v)

	if !cfg.ValidateOnly {
		m.vaults.Set(key, v)
	}

	op := m.newOp(cfg.Project, cfg.Location, opUpdate, key, cfg.ValidateOnly)
	out := cloneVault(&v)

	return &out, op, nil
}

// DeleteBackupVault removes a vault and returns the completed LRO. An absent
// vault is NOT_FOUND unless AllowMissing; a stale etag is rejected; a vault that
// still holds backups is FAILED_PRECONDITION unless Force.
func (m *Mock) DeleteBackupVault(_ context.Context, req *bdrdriver.DeleteBackupVaultRequest) (
	*bdrdriver.Operation, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(req.Project, req.Location, req.ID)

	v, ok := m.vaults.Get(key)
	if !ok {
		if req.AllowMissing {
			return m.newOp(req.Project, req.Location, opDelete, key, req.ValidateOnly), nil
		}

		return nil, notFoundErr(req.Project, req.Location, req.ID)
	}

	if req.Etag != "" && req.Etag != v.Etag {
		return nil, fmt.Errorf("backup vault %q: %w", key, bdrdriver.ErrEtagMismatch)
	}

	if !v.Deletable() && !req.Force {
		return nil, cerrors.Newf(cerrors.FailedPrecondition,
			"backup vault %q contains %d backups; set force=true to delete it with its data sources", key, v.BackupCount)
	}

	if !req.ValidateOnly {
		m.vaults.Delete(key)
	}

	return m.newOp(req.Project, req.Location, opDelete, key, req.ValidateOnly), nil
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op
// id an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*bdrdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &bdrdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// notFoundErr builds the NOT_FOUND error carrying the full resource name.
func notFoundErr(project, location, id string) error {
	return cerrors.Newf(cerrors.NotFound, "backup vault %q not found", resourceName(project, location, id))
}
