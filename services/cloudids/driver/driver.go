// Package driver defines the portable interface for the Google Cloud IDS control
// plane (ids.googleapis.com/v1). It is control-plane only — the single
// region-scoped resource collection a Terraform google provider or a real
// google.golang.org/api/ids client CRUDs is modeled:
//
//	projects/{p}/locations/{region}/endpoints/{id}
//
// and the long-running operations its mutating RPCs return, which are
// location-scoped and share the operations space the shared GCP LRO poller owns:
//
//	projects/{p}/locations/{region}/operations/{op}
//
// There is no data plane: no packet mirroring, no real intrusion detection, no
// threat alerts (see BUILDOUT_BACKLOG.md). The identity + computed fields (name,
// createTime, updateTime) are derived at create and stay stable across reads so a
// Terraform refresh does not drift. The output-only endpoint attributes seeded
// once at create — state (READY), endpointForwardingRule, endpointIp — are the
// classic Cloud IDS drift point: endpointForwardingRule and endpointIp are
// derived deterministically from the endpoint identity so they are identical on
// every read. Every other caller-supplied body key (network, severity,
// description, threatExceptions, labels, …) is carried as Fields verbatim,
// matching the vpcaccess/certificatemanager raw-passthrough model, so deep
// sub-blocks need not be enumerated and cannot drift.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Resource is one Cloud IDS endpoint. Name components are stored separately so
// the full resource name and location scoping can be rebuilt without re-parsing.
// CreateTime/UpdateTime are derived deterministically and stay stable across
// reads. Fields holds every caller-supplied, non-computed body key verbatim —
// plus the computed body values seeded once at create (state,
// endpointForwardingRule, endpointIp), which then round-trip as stable
// passthrough values.
type Resource struct {
	Project    string
	Location   string
	ID         string
	CreateTime time.Time
	UpdateTime time.Time
	Fields     map[string]json.RawMessage
}

// Config is the input to a create or patch. Fields carries the caller-supplied
// body keys (the computed name/createTime/updateTime keys already stripped by the
// wire layer; the state/endpointForwardingRule/endpointIp values already seeded
// at create).
type Config struct {
	Project  string
	Location string
	ID       string
	Fields   map[string]json.RawMessage
}

// Operation is a completed long-running operation. Every CloudEmu mutation
// finishes synchronously, so Done is always true; the shared poller replays it so
// an SDK or Terraform LRO wait terminates on the first poll.
type Operation struct {
	Name       string // projects/{p}/locations/{region}/operations/{op}
	Done       bool
	TargetName string // the endpoint the operation acted on
	Type       string // create | update | delete
}

// CloudIDs is the control-plane interface a provider implements for the endpoints
// collection.
type CloudIDs interface {
	CreateEndpoint(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetEndpoint(ctx context.Context, project, location, id string) (*Resource, error)
	ListEndpoints(ctx context.Context, project, location string) ([]Resource, error)
	PatchEndpoint(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteEndpoint(ctx context.Context, project, location, id string) (*Operation, error)

	// GetOperation resolves a (done) long-running operation by name, for a
	// standalone package server's own operations poll.
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
