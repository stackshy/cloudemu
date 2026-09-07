// Package driver defines the portable interface for the Google Certificate
// Manager control plane (certificatemanager.googleapis.com/v1). It is
// control-plane only — the three location-scoped resource collections a
// Terraform google provider or a real google.golang.org/api/certificatemanager
// client CRUDs are modeled:
//
//	projects/{p}/locations/{loc}/certificates/{id}
//	projects/{p}/locations/{loc}/certificateMaps/{id}
//	projects/{p}/locations/{loc}/dnsAuthorizations/{id}
//
// and the long-running operations their mutating RPCs return, which are
// location-scoped and share the operations space the shared GCP LRO poller owns:
//
//	projects/{p}/locations/{loc}/operations/{op}
//
// certificateMapEntries, certificateIssuanceConfigs, trustConfigs, IAM policy
// verbs, and any real certificate issuance (the data plane) are out of scope
// (see BUILDOUT_BACKLOG.md).
//
// No resource carries a uid or etag in the real API. The identity + computed
// fields (name, createTime, updateTime) are derived at create and stay stable
// across reads so a Terraform refresh does not drift. A dnsAuthorization also
// carries a computed dnsResourceRecord{name,type,data} minted once from its
// domain at create; it is stored and stable across reads (the classic drift
// point). Every other caller-supplied body key (managed/self_managed oneof,
// domains, dnsAuthorizations, labels, description, scope, …) is carried as
// Fields verbatim, matching the clouddeploy/datastream raw-passthrough model, so
// deep loosely-typed sub-blocks need not be enumerated and cannot drift.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Resource is one Certificate Manager control-plane resource (a certificate, a
// certificate map, or a DNS authorization). Name components are stored
// separately so the full resource name and location scoping can be rebuilt
// without re-parsing. CreateTime/UpdateTime are derived deterministically and
// stay stable across reads. Fields holds every caller-supplied, non-computed
// body key verbatim — plus any computed body value seeded once at create (a DNS
// authorization's dnsResourceRecord, a managed certificate's state), which then
// round-trips as a stable passthrough value.
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
// the wire layer; a DNS authorization's dnsResourceRecord already seeded).
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

// CertificateManager is the control-plane interface a provider implements. The
// three resource collections share identical verb/LRO shapes.
type CertificateManager interface {
	CreateCertificate(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetCertificate(ctx context.Context, project, location, id string) (*Resource, error)
	ListCertificates(ctx context.Context, project, location string) ([]Resource, error)
	PatchCertificate(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteCertificate(ctx context.Context, project, location, id string) (*Operation, error)

	CreateCertificateMap(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetCertificateMap(ctx context.Context, project, location, id string) (*Resource, error)
	ListCertificateMaps(ctx context.Context, project, location string) ([]Resource, error)
	PatchCertificateMap(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteCertificateMap(ctx context.Context, project, location, id string) (*Operation, error)

	CreateDNSAuthorization(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetDNSAuthorization(ctx context.Context, project, location, id string) (*Resource, error)
	ListDNSAuthorizations(ctx context.Context, project, location string) ([]Resource, error)
	PatchDNSAuthorization(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteDNSAuthorization(ctx context.Context, project, location, id string) (*Operation, error)

	// GetOperation resolves a (done) long-running operation by name, for a
	// standalone package server's own operations poll.
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
