// Package driver defines the portable interface for the Google Network
// Connectivity Center control plane (networkconnectivity.googleapis.com/v1). It
// is control-plane only — the two resource collections a Terraform google
// provider or a real google.golang.org/api/networkconnectivity/v1 client CRUDs
// are modeled:
//
//	projects/{p}/locations/global/hubs/{id}    (hubs are global)
//	projects/{p}/locations/{loc}/spokes/{id}   (spokes are location-scoped)
//
// and the long-running operations their mutating RPCs return, which are
// location-scoped and share the operations space the shared GCP LRO poller owns:
//
//	projects/{p}/locations/{loc}/operations/{op}
//
// Groups, route tables, routes, policy-based routes, IAM policy verbs, and any
// real data-plane connectivity are out of scope (see BUILDOUT_BACKLOG.md).
//
// The identity + computed output fields (name, uniqueId, state, createTime,
// updateTime) are minted once at create, stored, and stay stable across reads so
// a Terraform refresh does not drift: a hub and a linked spoke are both minted
// ACTIVE, and uniqueId is a Google-style UUID. Every other caller-supplied body
// key (description, labels, hub, and the rich linked_vpc_network / linked_*
// oneof blocks) is carried as Fields verbatim, matching the clouddeploy
// raw-passthrough model, so deep loosely-typed sub-blocks need not be enumerated
// and cannot drift.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Resource is one Network Connectivity Center control-plane resource (a hub or a
// spoke). Name components are stored separately so the full resource name and
// location scoping can be rebuilt without re-parsing. UniqueID, State,
// CreateTime, and UpdateTime are minted deterministically at create and stay
// stable across reads. Fields holds every caller-supplied, non-computed body key
// verbatim (description, labels, hub, linked_* blocks).
type Resource struct {
	Project    string
	Location   string
	ID         string
	UniqueID   string
	State      string
	CreateTime time.Time
	UpdateTime time.Time
	Fields     map[string]json.RawMessage
}

// Config is the input to a create or patch. Fields carries the caller-supplied
// body keys (the computed name/uniqueId/state/createTime/updateTime keys already
// stripped by the wire layer).
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

// NetworkConnectivity is the control-plane interface a provider implements. The
// two resource collections have identical verb/LRO shapes; only their scoping
// differs (hubs are global, spokes are regional).
type NetworkConnectivity interface {
	CreateHub(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetHub(ctx context.Context, project, location, id string) (*Resource, error)
	ListHubs(ctx context.Context, project, location string) ([]Resource, error)
	PatchHub(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteHub(ctx context.Context, project, location, id string) (*Operation, error)

	CreateSpoke(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetSpoke(ctx context.Context, project, location, id string) (*Resource, error)
	ListSpokes(ctx context.Context, project, location string) ([]Resource, error)
	PatchSpoke(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteSpoke(ctx context.Context, project, location, id string) (*Operation, error)

	// GetOperation resolves a (done) long-running operation by name, for a
	// standalone package server's own operations poll.
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
