// Package driver defines the portable interface for the Google Cloud Backup and
// DR control plane (backupdr.googleapis.com/v1). It is control-plane only: the
// region-scoped backup vault collection a google.golang.org/api/backupdr/v1
// client or the Terraform google provider's google_backup_dr_backup_vault
// resource CRUDs is modeled:
//
//	projects/{p}/locations/{region}/backupVaults/{id}
//
// and the long-running operations its mutating RPCs return, which are
// location-scoped and share the operations space the shared GCP LRO poller owns:
//
//	projects/{p}/locations/{region}/operations/{op}
//
// There is no data plane: no data sources, backups, backup plans, management
// servers or restores. A vault therefore always reports backupCount and
// totalStoredBytes of 0 and deletable=true, unless restored from a snapshot
// that carries usage (the provider's tests seed it through a test-only hook to
// exercise the non-empty delete guard).

package driver

import (
	"context"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// ErrEtagMismatch is the error (wrapped) a patch or delete returns when the
// caller supplied an etag that no longer matches the stored vault. Real Backup
// and DR rejects that write so a concurrent update is never silently
// overwritten; wire layers map it to 409 ABORTED.
var ErrEtagMismatch = cerrors.New(cerrors.FailedPrecondition, "etag does not match the current backup vault etag")

// EncryptionConfig mirrors the vault's optional customer-managed encryption key.
type EncryptionConfig struct {
	KmsKeyName string
}

// BackupVault is one backup vault. Name components are stored separately so
// the full resource name and location scoping can be rebuilt without re-parsing.
// Every output-only field (State, ServiceAccount, UID, Etag, BackupCount,
// TotalStoredBytes, CreateTime, UpdateTime) is minted by the provider.
type BackupVault struct {
	Project  string
	Location string
	ID       string

	Description                            string
	Labels                                 map[string]string
	Annotations                            map[string]string
	BackupMinimumEnforcedRetentionDuration string
	BackupRetentionInheritance             string
	EffectiveTime                          string
	AccessRestriction                      string
	EncryptionConfig                       *EncryptionConfig

	State            string
	ServiceAccount   string
	UID              string
	Etag             string
	Revision         int64
	BackupCount      int64
	TotalStoredBytes int64
	CreateTime       time.Time
	UpdateTime       time.Time
}

// Deletable reports whether the vault holds no backups, the condition real
// Backup and DR exposes as the output-only `deletable` field.
func (v *BackupVault) Deletable() bool { return v.BackupCount == 0 }

// BackupVaultConfig is the input to a create or patch. Etag is only consulted
// by a patch: when non-empty it must match the stored vault's etag.
// ValidateOnly runs every check without mutating state.
type BackupVaultConfig struct {
	Project  string
	Location string
	ID       string

	Description                            string
	Labels                                 map[string]string
	Annotations                            map[string]string
	BackupMinimumEnforcedRetentionDuration string
	BackupRetentionInheritance             string
	EffectiveTime                          string
	AccessRestriction                      string
	EncryptionConfig                       *EncryptionConfig

	Etag         string
	ValidateOnly bool
}

// DeleteBackupVaultRequest carries the identity and the delete options real
// Backup and DR accepts. Force deletes a vault that still holds backups;
// AllowMissing turns a delete of an absent vault into a no-op success; Etag,
// when non-empty, must match the stored vault's etag.
type DeleteBackupVaultRequest struct {
	Project      string
	Location     string
	ID           string
	Etag         string
	Force        bool
	AllowMissing bool
	ValidateOnly bool
}

// Operation is a completed long-running operation. Every CloudEmu mutation
// finishes synchronously, so Done is always true; the shared poller replays it
// so an SDK or Terraform LRO wait terminates on the first poll.
type Operation struct {
	Name       string // projects/{p}/locations/{region}/operations/{op}
	Done       bool
	TargetName string // the backup vault the operation acted on
	Type       string // create | update | delete
}

// BackupDR is the control-plane interface a provider implements for the backup
// vaults collection.
type BackupDR interface {
	// CreateBackupVault creates a vault; the retention duration is required.
	CreateBackupVault(ctx context.Context, cfg *BackupVaultConfig) (*BackupVault, *Operation, error)
	// GetBackupVault returns one vault by identity.
	GetBackupVault(ctx context.Context, project, location, id string) (*BackupVault, error)
	// ListBackupVaults lists a project's vaults in a location ("-" for every location).
	ListBackupVaults(ctx context.Context, project, location string) ([]BackupVault, error)
	// UpdateBackupVault applies a field-masked update and rotates the etag.
	UpdateBackupVault(ctx context.Context, cfg *BackupVaultConfig, mask []string) (*BackupVault, *Operation, error)
	// DeleteBackupVault deletes a vault, honoring force, allowMissing and etag.
	DeleteBackupVault(ctx context.Context, req *DeleteBackupVaultRequest) (*Operation, error)

	// GetOperation resolves a (done) long-running operation by name, for a
	// standalone package server's own operations poll.
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
