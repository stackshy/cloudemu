// Package driver defines the portable interface for the Google Cloud Workflows
// control plane (workflows.googleapis.com/v1). It is control-plane only — the
// single location-scoped resource collection a Terraform google provider or a
// real google.golang.org/api/workflows/v1 client CRUDs is modeled:
//
//	projects/{p}/locations/{loc}/workflows/{id}
//
// and the long-running operations its mutating RPCs return, which are
// location-scoped and share the operations space the shared GCP LRO poller owns:
//
//	projects/{p}/locations/{loc}/operations/{op}
//
// Workflow definition execution (executions, callbacks, step logs) is out of
// scope: CloudEmu emulates the control plane, not the workflow runtime.
//
// A workflow carries a small set of computed / output-only fields (state,
// revisionId, createTime, updateTime, revisionCreateTime) plus a body of
// caller-supplied config keys (description, sourceContents, serviceAccount,
// labels, userEnvVars, cryptoKeyName, callLogLevel, …) that round-trips
// verbatim. The verbatim body is carried as Fields so deep, loosely-typed
// sub-blocks need not be enumerated, matching the clouddeploy raw-passthrough
// model. revisionId is derived deterministically and bumps only when a
// revision-defining field (sourceContents or serviceAccount) changes, matching
// real GCP.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Resource is one Cloud Workflows control-plane resource. Name components are
// stored separately so the full resource name and location scoping can be
// rebuilt without re-parsing. The computed fields are derived deterministically
// at create and stay stable across reads so a Terraform refresh does not drift.
// Fields holds every caller-supplied, non-computed body key verbatim.
type Resource struct {
	Project    string
	Location   string
	ID         string
	Revision   uint64 // 1-based revision counter; bumps on a revision-defining change
	RevisionID string // "NNNNNN-XXX"; stable until a revision-defining field changes
	State      string // output-only lifecycle state (ACTIVE)

	CreateTime         time.Time // minted at create, never changes
	UpdateTime         time.Time // recomputed on every mutation
	RevisionCreateTime time.Time // recomputed only when the revision bumps

	Fields map[string]json.RawMessage
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

// Workflows is the control-plane interface a provider implements.
type Workflows interface {
	CreateWorkflow(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetWorkflow(ctx context.Context, project, location, id string) (*Resource, error)
	ListWorkflows(ctx context.Context, project, location string) ([]Resource, error)
	PatchWorkflow(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteWorkflow(ctx context.Context, project, location, id string) (*Operation, error)

	// GetOperation resolves a (done) long-running operation by name, for a
	// standalone package server's own operations poll.
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
