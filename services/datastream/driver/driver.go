// Package driver defines the portable interface for the Google Datastream
// control plane (datastream.googleapis.com/v1). It is control-plane only — the
// two location-scoped resource collections a Terraform google provider or a
// real google.golang.org/api/datastream/v1 client CRUDs are modeled:
//
//	projects/{p}/locations/{loc}/connectionProfiles/{id}
//	projects/{p}/locations/{loc}/streams/{id}
//
// and the long-running operations their mutating RPCs return, which are
// location-scoped and share the operations space the shared GCP LRO poller owns:
//
//	projects/{p}/locations/{loc}/operations/{op}
//
// privateConnections, routes, IAM policy verbs, object discovery, and any real
// CDC replication (the data plane) are out of scope (see BUILDOUT_BACKLOG.md).
//
// Neither resource carries a uid or etag in the real API. A connection profile
// has no state; a stream has a state enum minted at create (default NOT_STARTED)
// and mutated via a state-masked patch. The identity + computed fields (name,
// createTime, updateTime, and — for a stream — state) are derived at create and
// stay stable across reads so a Terraform refresh does not drift. Every other
// caller-supplied body key (the rich oracle/mysql/postgresql/gcs/bigquery
// profile and source/destination config blocks, backfill oneof, labels, …) is
// carried as Fields verbatim, matching the clouddeploy raw-passthrough model, so
// deep loosely-typed sub-blocks need not be enumerated and cannot drift.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Resource is one Datastream control-plane resource (a connection profile or a
// stream). Name components are stored separately so the full resource name and
// location scoping can be rebuilt without re-parsing. CreateTime/UpdateTime are
// derived deterministically and stay stable across reads. Fields holds every
// caller-supplied, non-computed body key verbatim — including a stream's state,
// which round-trips as a passthrough value seeded at create.
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
// the wire layer; a stream's default state already seeded).
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

// Datastream is the control-plane interface a provider implements. The two
// resource collections have identical verb/LRO shapes.
type Datastream interface {
	CreateConnectionProfile(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetConnectionProfile(ctx context.Context, project, location, id string) (*Resource, error)
	ListConnectionProfiles(ctx context.Context, project, location string) ([]Resource, error)
	PatchConnectionProfile(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteConnectionProfile(ctx context.Context, project, location, id string) (*Operation, error)

	CreateStream(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetStream(ctx context.Context, project, location, id string) (*Resource, error)
	ListStreams(ctx context.Context, project, location string) ([]Resource, error)
	PatchStream(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteStream(ctx context.Context, project, location, id string) (*Operation, error)

	// GetOperation resolves a (done) long-running operation by name, for a
	// standalone package server's own operations poll.
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
