// Package driver defines the portable interface for the Google Cloud Dataplex
// control plane (dataplex.googleapis.com/v1). It is control-plane only — the
// three-level hierarchy a Terraform google provider or a real
// google.golang.org/api/dataplex client CRUDs is modeled:
//
//	projects/{p}/locations/{loc}/lakes/{lake}
//	projects/{p}/locations/{loc}/lakes/{lake}/zones/{zone}
//	projects/{p}/locations/{loc}/lakes/{lake}/zones/{zone}/assets/{asset}
//
// and the long-running operations their mutating RPCs return, which are
// location-scoped and share the operations space the shared GCP LRO poller owns:
//
//	projects/{p}/locations/{loc}/operations/{op}
//
// A zone create requires its parent lake to exist (404 otherwise); an asset
// create requires its parent lake and zone. Deleting a lake cascades to its
// zones and their assets; deleting a zone cascades to its assets.
//
// tasks, entryGroups, entities, dataScans, environments, and IAM policy verbs
// are out of scope (see BUILDOUT_BACKLOG.md).
//
// The identity + computed fields (name, createTime, updateTime) are derived at
// create and stay stable across reads so a Terraform refresh does not drift. The
// remaining output-only fields a resource carries — uid, state, service_account
// (lake), and the nested status blocks (assetStatus/metastoreStatus/
// resourceStatus/securityStatus/discoveryStatus) — are seeded once at create by
// the wire layer and carried in Fields verbatim, so they too stay stable. Every
// caller-supplied body key (description, displayName, labels, metastore,
// discoverySpec, resourceSpec, type, …) is carried as Fields verbatim, matching
// the certificatemanager/datastream raw-passthrough model, so deep loosely-typed
// sub-blocks need not be enumerated and cannot drift.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Resource is one Dataplex control-plane resource (a lake, a zone, or an asset).
// The name components are stored separately so the full resource name and parent
// scoping can be rebuilt without re-parsing. Lake is empty for a lake; Lake is
// set and Zone empty for a zone; both are set for an asset. CreateTime/UpdateTime
// are derived deterministically and stay stable across reads. Fields holds every
// caller-supplied, non-computed body key verbatim plus the computed values seeded
// once at create (uid, state, service_account, the status blocks), which then
// round-trip as stable passthrough values.
type Resource struct {
	Project    string
	Location   string
	Lake       string // parent lake id (empty for a lake)
	Zone       string // parent zone id (empty for a lake or zone)
	ID         string
	CreateTime time.Time
	UpdateTime time.Time
	Fields     map[string]json.RawMessage
}

// Config is the input to a create or patch. Lake/Zone identify the parent(s) for
// a nested resource. Fields carries the caller-supplied body keys (the computed
// name/createTime/updateTime keys already stripped by the wire layer; the uid/
// state/status output fields already seeded).
type Config struct {
	Project  string
	Location string
	Lake     string // parent lake id (for a zone or asset create/patch)
	Zone     string // parent zone id (for an asset create/patch)
	ID       string
	Fields   map[string]json.RawMessage
}

// Operation is a completed long-running operation. Every CloudEmu mutation
// finishes synchronously, so Done is always true; the shared poller replays it so
// an SDK or Terraform LRO wait terminates on the first poll.
type Operation struct {
	Name       string // projects/{p}/locations/{loc}/operations/{op}
	Done       bool
	TargetName string // the resource the operation acted on
	Type       string // create | update | delete
}

// Dataplex is the control-plane interface a provider implements. The three
// resource levels share identical verb/LRO shapes; the nested levels also
// validate their parent's existence and cascade on delete.
type Dataplex interface {
	CreateLake(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetLake(ctx context.Context, project, location, lake string) (*Resource, error)
	ListLakes(ctx context.Context, project, location string) ([]Resource, error)
	PatchLake(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteLake(ctx context.Context, project, location, lake string) (*Operation, error)

	CreateZone(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetZone(ctx context.Context, project, location, lake, zone string) (*Resource, error)
	ListZones(ctx context.Context, project, location, lake string) ([]Resource, error)
	PatchZone(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteZone(ctx context.Context, project, location, lake, zone string) (*Operation, error)

	CreateAsset(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetAsset(ctx context.Context, project, location, lake, zone, asset string) (*Resource, error)
	ListAssets(ctx context.Context, project, location, lake, zone string) ([]Resource, error)
	PatchAsset(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteAsset(ctx context.Context, project, location, lake, zone, asset string) (*Operation, error)

	// GetOperation resolves a (done) long-running operation by name, for a
	// standalone package server's own operations poll.
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
