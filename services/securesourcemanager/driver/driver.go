// Package driver defines the portable interface for the Google Secure Source
// Manager control plane (securesourcemanager.googleapis.com/v1). It is
// control-plane only — the two location-scoped resource collections a Terraform
// google provider or a real Secure Source Manager client CRUDs are modeled:
//
//	projects/{p}/locations/{loc}/instances/{id}
//	projects/{p}/locations/{loc}/repositories/{id}
//
// and the long-running operations their mutating RPCs return, which are
// location-scoped and share the operations space the shared GCP LRO poller owns:
//
//	projects/{p}/locations/{loc}/operations/{op}
//
// Repositories are a top-level location-scoped collection that reference an
// instance by its full resource name (the `instance` body field), not a nested
// sub-collection of instances. IAM policy verbs, branch rules, hooks, and any
// real git hosting (the data plane) are out of scope (see BUILDOUT_BACKLOG.md).
//
// The identity + computed fields (name, createTime, updateTime) are derived at
// create and stay stable across reads so a Terraform refresh does not drift. An
// instance also carries a computed state and hostConfig{html,api,gitHttp,gitSsh}
// and a repository a computed uid and uris{html,gitHttps,api}, all minted once
// at create, stored, and stable across reads (the classic drift point — these
// output-only URL blocks must be byte-identical on the create response and every
// later GET). Every other caller-supplied body key (labels, description,
// kmsKey, privateConfig, initialConfig, instance, …) is carried as Fields
// verbatim, matching the certificatemanager/datastream raw-passthrough model, so
// deep loosely-typed sub-blocks need not be enumerated and cannot drift.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Resource is one Secure Source Manager control-plane resource (an instance or a
// repository). Name components are stored separately so the full resource name
// and location scoping can be rebuilt without re-parsing. CreateTime/UpdateTime
// are derived deterministically and stay stable across reads. Fields holds every
// caller-supplied, non-computed body key verbatim — plus any computed body value
// seeded once at create (an instance's state + hostConfig, a repository's uid +
// uris), which then round-trips as a stable passthrough value.
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
// the wire layer; the instance/repository computed blocks already seeded).
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

// SecureSourceManager is the control-plane interface a provider implements. The
// two resource collections share identical verb/LRO shapes.
type SecureSourceManager interface {
	// CreateInstance provisions a new instance and returns its completed LRO.
	// Instances have no update RPC in the real API (labels/kmsKey/privateConfig
	// are immutable; a change recreates), so there is deliberately no
	// PatchInstance — offering one would mask a real Terraform recreate.
	CreateInstance(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	// GetInstance returns an instance by identity.
	GetInstance(ctx context.Context, project, location, id string) (*Resource, error)
	// ListInstances returns every instance in a project+location.
	ListInstances(ctx context.Context, project, location string) ([]Resource, error)
	// DeleteInstance removes an instance and returns its completed LRO.
	DeleteInstance(ctx context.Context, project, location, id string) (*Operation, error)

	// CreateRepository provisions a new repository and returns its completed LRO.
	CreateRepository(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	// GetRepository returns a repository by identity.
	GetRepository(ctx context.Context, project, location, id string) (*Resource, error)
	// ListRepositories returns every repository in a project+location.
	ListRepositories(ctx context.Context, project, location string) ([]Resource, error)
	// PatchRepository applies a masked update to a repository (the one real
	// update RPC in this surface) and returns its completed LRO.
	PatchRepository(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	// DeleteRepository removes a repository and returns its completed LRO.
	DeleteRepository(ctx context.Context, project, location, id string) (*Operation, error)

	// GetOperation resolves a (done) long-running operation by name, for a
	// standalone package server's own operations poll.
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
