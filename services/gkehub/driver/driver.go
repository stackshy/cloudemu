// Package driver defines the portable interface for the GKE Hub / Fleet
// control plane (gkehub.googleapis.com/v1). It is control-plane only — the three
// location-scoped resource collections a Terraform google provider or a real
// google.golang.org/api/gkehub/v1 client CRUDs are modeled:
//
//	projects/{p}/locations/global/memberships/{id}   (memberships are global)
//	projects/{p}/locations/{loc}/features/{id}
//	projects/{p}/locations/{loc}/fleets/{id}          (the singleton "default")
//
// and the long-running operations their mutating RPCs return, which are
// location-scoped and share the operations space the shared GCP LRO poller owns:
//
//	projects/{p}/locations/{loc}/operations/{op}
//
// generateConnectManifest, IAM policy verbs, membership bindings, scopes,
// rbacrolebindings, and any real cluster registration / fleet data plane are out
// of scope (see BUILDOUT_BACKLOG.md).
//
// Each resource carries a small set of identity + computed fields (id, uniqueId/
// uid, createTime, updateTime, and a computed state) plus a body of
// caller-supplied config sub-blocks (a membership's endpoint/authority, a
// feature's spec/membershipSpecs, a fleet's displayName/defaultClusterConfig, …)
// that round-trips verbatim. The verbatim body is carried as Fields so deep,
// loosely-typed sub-blocks need not be enumerated, matching the raw-passthrough
// model used by composer, clouddeploy, and certificatemanager.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Resource is one GKE Hub control-plane resource (a membership, feature, or
// fleet). Name components are stored separately so the full resource name and
// location scoping can be rebuilt without re-parsing. The computed fields are
// derived deterministically at create and stay stable across reads so a
// Terraform refresh does not drift. Fields holds every caller-supplied,
// non-computed body key verbatim.
type Resource struct {
	Project    string
	Location   string
	ID         string
	UID        string // generated at create; stable thereafter (membership uniqueId, fleet uid)
	CreateTime time.Time
	UpdateTime time.Time
	Fields     map[string]json.RawMessage
}

// Config is the input to a create or patch. Fields carries the caller-supplied
// body keys (computed/output-only keys already stripped by the wire layer).
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

// GKEHub is the control-plane interface a provider implements. The three
// resource collections have identical verb/LRO shapes.
type GKEHub interface {
	CreateMembership(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetMembership(ctx context.Context, project, location, id string) (*Resource, error)
	ListMemberships(ctx context.Context, project, location string) ([]Resource, error)
	PatchMembership(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteMembership(ctx context.Context, project, location, id string) (*Operation, error)

	CreateFeature(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetFeature(ctx context.Context, project, location, id string) (*Resource, error)
	ListFeatures(ctx context.Context, project, location string) ([]Resource, error)
	PatchFeature(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteFeature(ctx context.Context, project, location, id string) (*Operation, error)

	CreateFleet(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetFleet(ctx context.Context, project, location, id string) (*Resource, error)
	ListFleets(ctx context.Context, project, location string) ([]Resource, error)
	PatchFleet(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteFleet(ctx context.Context, project, location, id string) (*Operation, error)

	// GetOperation resolves a (done) long-running operation by name, for a
	// standalone package server's own operations poll.
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
