// Package driver defines the portable interface for the Google Serverless VPC
// Access control plane (vpcaccess.googleapis.com/v1). It is control-plane only —
// the single region-scoped resource collection a Terraform google provider or a
// real google.golang.org/api/vpcaccess client CRUDs is modeled:
//
//	projects/{p}/locations/{region}/connectors/{id}
//
// and the long-running operations its mutating RPCs return, which are
// location-scoped and share the operations space the shared GCP LRO poller owns:
//
//	projects/{p}/locations/{region}/operations/{op}
//
// The connector uses EITHER ipCidrRange+network OR a subnet{name,projectId} — the
// network/subnet oneof — and both are carried verbatim as Fields, so neither is
// forced. IAM policy verbs and any real data-plane traffic routing are out of
// scope (see BUILDOUT_BACKLOG.md).
//
// No connector carries a uid or etag in the real API. The identity + computed
// fields (name, createTime, updateTime) are derived at create and stay stable
// across reads so a Terraform refresh does not drift. The output-only state
// (seeded READY), connectedProjects, and the API-defaulted numeric fields
// (minInstances, maxInstances, minThroughput, maxThroughput, and the machineType
// default) are minted once at create and stored, so a client that omits them
// reads the same values the real API fills — the classic connector drift point.
// Every other caller-supplied body key (network, ipCidrRange, subnet, labels, …)
// is carried as Fields verbatim, matching the datastream/certificatemanager
// raw-passthrough model, so deep sub-blocks need not be enumerated and cannot
// drift.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Resource is one Serverless VPC Access connector. Name components are stored
// separately so the full resource name and location scoping can be rebuilt
// without re-parsing. CreateTime/UpdateTime are derived deterministically and
// stay stable across reads. Fields holds every caller-supplied, non-computed
// body key verbatim — plus the computed body values seeded once at create
// (state, connectedProjects, the defaulted min/max instances and throughput),
// which then round-trip as stable passthrough values.
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
// the wire layer; the state/connectedProjects/defaulted numerics already
// seeded).
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
	TargetName string // the connector the operation acted on
	Type       string // create | update | delete
}

// VPCAccess is the control-plane interface a provider implements for the
// connectors collection.
type VPCAccess interface {
	CreateConnector(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetConnector(ctx context.Context, project, location, id string) (*Resource, error)
	ListConnectors(ctx context.Context, project, location string) ([]Resource, error)
	PatchConnector(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteConnector(ctx context.Context, project, location, id string) (*Operation, error)

	// GetOperation resolves a (done) long-running operation by name, for a
	// standalone package server's own operations poll.
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
