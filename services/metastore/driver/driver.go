// Package driver defines the portable interface for the Google Dataproc
// Metastore control plane (metastore.googleapis.com/v1). It is control-plane
// only — the single location-scoped resource collection a Terraform google
// provider or a real google.golang.org/api/metastore client CRUDs is modeled:
//
//	projects/{p}/locations/{region}/services/{id}
//
// and the long-running operations its mutating RPCs return, which are
// location-scoped and share the operations space the shared GCP LRO poller owns:
//
//	projects/{p}/locations/{region}/operations/{op}
//
// A metastore service carries a large set of output-only fields (endpointUri,
// state, stateMessage, artifactGcsUri, uid, createTime, updateTime) and a set of
// top-level server-defaulted fields (port 9083, databaseType MYSQL,
// releaseChannel STABLE, tier). The wire layer mints the computed values once at
// create and seeds the defaults so a client that omits them reads the same
// values the real API fills — the classic Dataproc Metastore refresh-drift
// point. The nested telemetryConfig/hiveMetastoreConfig blocks are Optional and
// NOT server-defaulted (the Terraform provider models them as non-Computed
// blocks), so they are carried through verbatim and never injected. Every
// caller-supplied body key (network, labels, encryptionConfig, networkConfig,
// scalingConfig, maintenanceWindow, hiveMetastoreConfig, …) is carried as Fields
// verbatim, matching the certificatemanager/vpcaccess raw-passthrough model, so
// deep sub-blocks need not be enumerated and cannot drift.
//
// metadataImports, backups, federations, and any real Hive metastore data plane
// are out of scope (see BUILDOUT_BACKLOG.md).
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Resource is one Dataproc Metastore service. Name components are stored
// separately so the full resource name and location scoping can be rebuilt
// without re-parsing. CreateTime/UpdateTime are derived deterministically and
// stay stable across reads. Fields holds every caller-supplied, non-computed
// body key verbatim — plus the computed body values seeded once at create
// (endpointUri, state, stateMessage, artifactGcsUri, uid, and the top-level
// server defaults port/databaseType/releaseChannel/tier), which then round-trip
// as stable passthrough values.
type Resource struct {
	Project    string
	Location   string
	ID         string
	CreateTime time.Time
	UpdateTime time.Time
	Fields     map[string]json.RawMessage
}

// Config is the input to a create or patch. Fields carries the caller-supplied
// body keys (the computed name/createTime/updateTime keys already stripped by
// the wire layer; the output-only and server-defaulted values already seeded).
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
	Name       string // projects/{p}/locations/{region}/operations/{op}
	Done       bool
	TargetName string // the service the operation acted on
	Type       string // create | update | delete
}

// Metastore is the control-plane interface a provider implements for the
// services collection.
type Metastore interface {
	CreateService(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetService(ctx context.Context, project, location, id string) (*Resource, error)
	ListServices(ctx context.Context, project, location string) ([]Resource, error)
	PatchService(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteService(ctx context.Context, project, location, id string) (*Operation, error)

	// GetOperation resolves a (done) long-running operation by name, for a
	// standalone package server's own operations poll.
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
