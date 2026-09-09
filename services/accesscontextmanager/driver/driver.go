// Package driver defines the portable interface for the Google Access Context
// Manager control plane (accesscontextmanager.googleapis.com/v1) — the VPC
// Service Controls surface a Terraform google provider or a real
// google.golang.org/api/accesscontextmanager client CRUDs. It is control-plane
// only, and its resource grammar is organization-scoped, not the usual
// projects/locations grammar:
//
//	accessPolicies/{policyNumber}                                   (org-scoped)
//	accessPolicies/{policyNumber}/accessLevels/{accessLevel}        (child)
//	accessPolicies/{policyNumber}/servicePerimeters/{perimeter}     (child)
//
// An access policy is created under POST /v1/accessPolicies with a body
// {parent: "organizations/{org}", title, scopes[]}; its numeric name is minted
// once, deterministically from parent+title, and stays byte-stable across
// reads (so a Terraform refresh does not drift). Access levels and service
// perimeters are children of a policy and carry the caller-supplied
// basic/custom (a level's conditions) or status/spec (a perimeter's config)
// blocks verbatim as opaque passthrough, matching the clouddeploy/datastream
// raw-passthrough model, so deep loosely-typed sub-blocks cannot drift.
//
// The long-running operations these mutating RPCs return are
// organization-scoped and live at the service root — /v1/operations/{id}, NOT
// under projects/locations — so they do not share the space the shared GCP LRO
// poller owns; a policy's own handler resolves them.
//
// IAM policy verbs (get/set/testIamPermissions), the accessLevels:replaceAll
// and servicePerimeters:replaceAll / :commit batch verbs, gcpUserAccessBindings
// and authorizedOrgsDescs are out of scope (see BUILDOUT_BACKLOG.md).
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Policy is one Access Context Manager access policy. Number is the computed
// numeric component of its resource name (accessPolicies/{Number}), minted once
// at create and stable thereafter. Etag/CreateTime/UpdateTime are computed and
// stay stable across reads. Fields holds every caller-supplied, non-computed
// body key verbatim (parent, title, scopes).
type Policy struct {
	Number     string
	CreateTime time.Time
	UpdateTime time.Time
	Etag       string
	Fields     map[string]json.RawMessage
}

// Child is one access level or service perimeter, nested under a policy.
// PolicyNumber+ID identify it (accessPolicies/{PolicyNumber}/{coll}/{ID}).
// Etag is computed and stable across reads; CreateTime/UpdateTime are populated
// but only surfaced on the wire for service perimeters (access levels carry no
// timestamps in the real API). Fields holds the caller-supplied body verbatim
// (title, description, basic/custom for a level; status/spec for a perimeter).
type Child struct {
	PolicyNumber string
	ID           string
	CreateTime   time.Time
	UpdateTime   time.Time
	Etag         string
	Fields       map[string]json.RawMessage
}

// PolicyConfig is the input to an access-policy create or patch. Fields carries
// the caller-supplied body keys (the computed name/etag/createTime/updateTime
// keys already stripped by the wire layer).
type PolicyConfig struct {
	Fields map[string]json.RawMessage
}

// ChildConfig is the input to an access-level or service-perimeter create or
// patch. PolicyNumber is the parent policy's number and ID is the short name.
type ChildConfig struct {
	PolicyNumber string
	ID           string
	Fields       map[string]json.RawMessage
}

// Operation is a completed long-running operation. Every CloudEmu mutation
// finishes synchronously, so Done is always true. TargetName is the full
// resource name the operation acted on and Kind classifies it so a poll can
// re-render the typed response.
type Operation struct {
	Name       string // operations/{id}
	Done       bool
	TargetName string
	Kind       string // policy | accessLevel | servicePerimeter
	Type       string // create | update | delete
}

// AccessContextManager is the control-plane interface a provider implements.
type AccessContextManager interface {
	CreatePolicy(ctx context.Context, cfg *PolicyConfig) (*Policy, *Operation, error)
	GetPolicy(ctx context.Context, number string) (*Policy, error)
	ListPolicies(ctx context.Context, parent string) ([]Policy, error)
	PatchPolicy(ctx context.Context, number string, cfg *PolicyConfig, mask []string) (*Policy, *Operation, error)
	DeletePolicy(ctx context.Context, number string) (*Operation, error)

	CreateAccessLevel(ctx context.Context, cfg *ChildConfig) (*Child, *Operation, error)
	GetAccessLevel(ctx context.Context, policyNumber, id string) (*Child, error)
	ListAccessLevels(ctx context.Context, policyNumber string) ([]Child, error)
	PatchAccessLevel(ctx context.Context, cfg *ChildConfig, mask []string) (*Child, *Operation, error)
	DeleteAccessLevel(ctx context.Context, policyNumber, id string) (*Operation, error)

	CreateServicePerimeter(ctx context.Context, cfg *ChildConfig) (*Child, *Operation, error)
	GetServicePerimeter(ctx context.Context, policyNumber, id string) (*Child, error)
	ListServicePerimeters(ctx context.Context, policyNumber string) ([]Child, error)
	PatchServicePerimeter(ctx context.Context, cfg *ChildConfig, mask []string) (*Child, *Operation, error)
	DeleteServicePerimeter(ctx context.Context, policyNumber, id string) (*Operation, error)

	// GetOperation resolves a (done) long-running operation by name, for the
	// service-root operations poll.
	GetOperation(ctx context.Context, name string) (*Operation, error)
	// HasOperation reports whether name was minted by this backend, so the wire
	// handler claims only its own root /v1/operations/{id} polls and yields the
	// shared root-operations space to sibling handlers (Cloud Functions gen1).
	HasOperation(ctx context.Context, name string) bool
}
