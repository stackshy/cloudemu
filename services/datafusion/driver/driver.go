// Package driver defines the portable interface for the Google Cloud Data Fusion
// control plane (datafusion.googleapis.com/v1). It is control-plane only — the
// single location-scoped resource collection a Terraform google provider
// (google_data_fusion_instance) or a real google.golang.org/api/datafusion/v1
// client CRUDs is modeled:
//
//	projects/{p}/locations/{loc}/instances/{id}
//
// and the long-running operations its mutating RPCs return, which are
// location-scoped and share the operations space the shared GCP LRO poller owns:
//
//	projects/{p}/locations/{loc}/operations/{op}
//
// The instance state machine is modeled: a create settles synchronously to
// ACTIVE (real create ~20min, but the emulator must land ACTIVE or a Terraform
// apply hangs / drifts), and the :restart custom verb runs an ACTIVE instance
// through RESTARTING back to ACTIVE. The v1 API's running state is ACTIVE (it
// corresponds to RUNNING in datafusion.v1beta1); the stable hashicorp/google
// provider targets the v1 base path, so ACTIVE is the drift-free value.
//
// A :restart of a non-ACTIVE instance is rejected with FAILED_PRECONDITION and a
// verb on a missing instance with NOT_FOUND, matching the real API.
//
// Every caller-supplied body key (type, version, displayName, description,
// labels, networkConfig, accelerators, dataprocServiceAccount, enableRbac,
// options, …) is carried as Fields verbatim, matching the clouddeploy/
// certificatemanager raw-passthrough model, so deep loosely-typed sub-blocks
// need not be enumerated and cannot drift. IAM policy verbs are out of scope
// (see BUILDOUT_BACKLOG.md).
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Instance-state constants shared by the driver and its provider implementation.
const (
	// StateActive is the running state a v1 Data Fusion instance reports. It
	// corresponds to RUNNING in datafusion.v1beta1; the stable provider targets
	// v1, so ACTIVE is the value that does not drift.
	StateActive = "ACTIVE"
	// StateRestarting is the transient state an instance passes through during a
	// :restart. The emulator settles synchronously, so it is observable only as
	// the modeled intermediate transition.
	StateRestarting = "RESTARTING"
	// StateCreating / StateDeleting are the transient lifecycle states a real
	// instance reports; the emulator settles past them synchronously, but they
	// are the states from which a :restart is illegal.
	StateCreating = "CREATING"
	StateDeleting = "DELETING"
)

// Resource is one Data Fusion control-plane instance. Name components are stored
// separately so the full resource name and location scoping can be rebuilt
// without re-parsing. State is the output-only lifecycle state minted at create
// and advanced by lifecycle verbs. CreateTime is minted once at create;
// UpdateTime advances on every mutation. Fields holds every caller-supplied,
// non-computed body key verbatim.
type Resource struct {
	Project    string
	Location   string
	ID         string
	State      string
	CreateTime time.Time
	UpdateTime time.Time
	Fields     map[string]json.RawMessage
}

// Config is the input to a create or patch. Fields carries the caller-supplied
// body keys (the computed name/state/createTime/updateTime and other output-only
// keys already stripped by the wire layer).
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
	Type       string // create | update | delete | restart
}

// DataFusion is the control-plane interface a provider implements. Owns and
// OwnsAnyIn back the wire handler's Matches, which must claim only genuinely-
// Data-Fusion traffic on the /instances path it shares with Memorystore and
// Filestore.
type DataFusion interface {
	CreateInstance(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetInstance(ctx context.Context, project, location, id string) (*Resource, error)
	ListInstances(ctx context.Context, project, location string) ([]Resource, error)
	PatchInstance(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteInstance(ctx context.Context, project, location, id string) (*Operation, error)

	// RestartInstance runs an ACTIVE instance through RESTARTING back to ACTIVE.
	// A restart of a non-ACTIVE instance is FAILED_PRECONDITION; of a missing
	// instance, NOT_FOUND.
	RestartInstance(ctx context.Context, project, location, id string) (*Resource, *Operation, error)

	// Owns reports whether this store holds the named instance, so the wire
	// handler claims an item request only for its own instances (Redis/Filestore
	// items sharing the path fall through).
	Owns(project, location, id string) bool
	// OwnsAnyIn reports whether this store holds any instance in the scope, so a
	// bare LIST is claimed only when it has something to return.
	OwnsAnyIn(project, location string) bool

	// GetOperation resolves a (done) long-running operation by name, for a
	// standalone package server's own operations poll.
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
