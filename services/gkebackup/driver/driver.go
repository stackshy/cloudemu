// Package driver defines the portable interface for the Google Backup for GKE
// control plane (gkebackup.googleapis.com/v1). It is control-plane only — the
// two location-scoped resource collections a Terraform google provider or a
// real google.golang.org/api/gkebackup client CRUDs are modeled:
//
//	projects/{p}/locations/{loc}/backupPlans/{id}
//	projects/{p}/locations/{loc}/restorePlans/{id}
//
// and the long-running operations their mutating RPCs return, which are
// location-scoped and share the operations space the shared GCP LRO poller owns:
//
//	projects/{p}/locations/{loc}/operations/{op}
//
// The Backups nested under a backupPlan and the Restores nested under a
// restorePlan are a data-plane-ish sub-resource and are out of scope, as are
// IAM policy verbs (see BUILDOUT_BACKLOG.md).
//
// Each resource carries a small set of output-only computed fields derived at
// create and stable across reads so a Terraform refresh does not drift: uid
// (deterministic from the resource name), etag (recomputed on every mutation),
// state (READY, or DEACTIVATED for a deactivated backupPlan), createTime, and
// updateTime. Every caller-supplied body key (cluster, backupConfig,
// backupSchedule, retentionPolicy, deactivated, restoreConfig, backupPlan,
// labels, description, …) is carried as Fields verbatim, matching the
// clouddeploy/certificatemanager raw-passthrough model, so deep loosely-typed
// sub-blocks need not be enumerated and cannot drift.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Resource is one Backup for GKE control-plane resource (a backup plan or a
// restore plan). Name components are stored separately so the full resource
// name and location scoping can be rebuilt without re-parsing. Uid, Etag, and
// State are output-only computed values minted at create and kept stable across
// reads (Etag is recomputed on every mutation). CreateTime/UpdateTime are
// derived deterministically. Fields holds every caller-supplied, non-computed
// body key verbatim.
type Resource struct {
	Project    string
	Location   string
	ID         string
	UID        string
	Etag       string
	State      string
	CreateTime time.Time
	UpdateTime time.Time
	Fields     map[string]json.RawMessage
}

// Config is the input to a create or patch. Fields carries the caller-supplied
// body keys (the computed name/uid/etag/state/createTime/updateTime keys already
// stripped by the wire layer). The output-only state is derived by the provider
// from the resulting resource, so it is correct after a masked patch that does
// or does not touch the deactivated flag.
type Config struct {
	Project  string
	Location string
	ID       string
	Fields   map[string]json.RawMessage
}

// Operation is a completed long-running operation. Every CloudEmu mutation
// finishes synchronously, so Done is always true; the shared poller replays it
// so an SDK or Terraform LRO wait terminates on the first poll.
type Operation struct {
	Name       string // projects/{p}/locations/{loc}/operations/{op}
	Done       bool
	TargetName string // the resource the operation acted on
	Type       string // create | update | delete
}

// GKEBackup is the control-plane interface a provider implements. The two
// resource collections share identical verb/LRO shapes.
type GKEBackup interface {
	CreateBackupPlan(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetBackupPlan(ctx context.Context, project, location, id string) (*Resource, error)
	ListBackupPlans(ctx context.Context, project, location string) ([]Resource, error)
	PatchBackupPlan(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteBackupPlan(ctx context.Context, project, location, id string) (*Operation, error)

	CreateRestorePlan(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetRestorePlan(ctx context.Context, project, location, id string) (*Resource, error)
	ListRestorePlans(ctx context.Context, project, location string) ([]Resource, error)
	PatchRestorePlan(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteRestorePlan(ctx context.Context, project, location, id string) (*Operation, error)

	// GetOperation resolves a (done) long-running operation by name, for a
	// standalone package server's own operations poll.
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
