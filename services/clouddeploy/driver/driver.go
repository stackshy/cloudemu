// Package driver defines the portable interface for the Google Cloud Deploy
// control plane (clouddeploy.googleapis.com/v1). It is control-plane only — the
// two location-scoped resource collections a Terraform google provider or a
// real google.golang.org/api/clouddeploy/v1 client CRUDs are modeled:
//
//	projects/{p}/locations/{loc}/deliveryPipelines/{id}
//	projects/{p}/locations/{loc}/targets/{id}
//
// and the long-running operations their mutating RPCs return, which are
// location-scoped and share the operations space the shared GCP LRO poller owns:
//
//	projects/{p}/locations/{loc}/operations/{op}
//
// Releases, rollouts, rollbacks, automations, deployPolicies, customTargetTypes,
// jobRuns, IAM policy verbs, and any real render/deploy execution are out of
// scope (see BUILDOUT_BACKLOG.md).
//
// Both resources carry a small set of identity + computed fields (id, uid,
// createTime, updateTime, etag, and — for a pipeline — a computed condition)
// plus a body of caller-supplied config sub-blocks (serialPipeline, the target
// deployment-target oneof, executionConfigs, …) that round-trips verbatim. The
// verbatim body is carried as Fields so deep, loosely-typed sub-blocks need not
// be enumerated, matching the composer raw-passthrough model.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Resource is one Cloud Deploy control-plane resource (a delivery pipeline or a
// target). Name components are stored separately so the full resource name and
// location scoping can be rebuilt without re-parsing. The computed fields are
// derived deterministically at create and stay stable across reads so a
// Terraform refresh does not drift. Fields holds every caller-supplied,
// non-computed body key verbatim.
type Resource struct {
	Project    string
	Location   string
	ID         string
	UID        string // generated at create; stable thereafter
	CreateTime time.Time
	UpdateTime time.Time
	Etag       string // recomputed on every mutation, stable across reads
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

// CloudDeploy is the control-plane interface a provider implements. The two
// resource collections have identical verb/LRO shapes.
type CloudDeploy interface {
	CreatePipeline(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetPipeline(ctx context.Context, project, location, id string) (*Resource, error)
	ListPipelines(ctx context.Context, project, location string) ([]Resource, error)
	PatchPipeline(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeletePipeline(ctx context.Context, project, location, id string) (*Operation, error)

	CreateTarget(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetTarget(ctx context.Context, project, location, id string) (*Resource, error)
	ListTargets(ctx context.Context, project, location string) ([]Resource, error)
	PatchTarget(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteTarget(ctx context.Context, project, location, id string) (*Operation, error)

	// GetOperation resolves a (done) long-running operation by name, for a
	// standalone package server's own operations poll.
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
