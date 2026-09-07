// Package driver defines the portable interface for the Google Certificate
// Authority Service control plane (privateca.googleapis.com/v1). It is
// control-plane only — the four resource collections a Terraform google provider
// or a real google.golang.org/api/privateca client CRUDs are modeled:
//
//	projects/{p}/locations/{loc}/caPools/{id}
//	projects/{p}/locations/{loc}/caPools/{pool}/certificateAuthorities/{id}
//	projects/{p}/locations/{loc}/caPools/{pool}/certificates/{id}
//	projects/{p}/locations/{loc}/certificateTemplates/{id}
//
// and the long-running operations their mutating RPCs return, which are
// location-scoped and share the operations space the shared GCP LRO poller owns:
//
//	projects/{p}/locations/{loc}/operations/{op}
//
// No real X.509 crypto or signing is performed: certificate authorities and
// certificates carry deterministic placeholder PEM material minted once at create
// and stored, so it is byte-stable across reads (a Terraform refresh never
// drifts). The unique certificateAuthorities verbs (:enable, :disable, :undelete,
// :fetch, :activate) drive the real CA State machine (ENABLED / DISABLED / STAGED
// / AWAITING_USER_ACTIVATION / DELETED); certificates carry a :revoke verb that
// records revocationDetails. IAM policy verbs, CRL/fetchCaCerts, and any real
// issuance internals are out of scope (see BUILDOUT_BACKLOG.md).
//
// Every caller-supplied, non-computed body key (config, subjectConfig, x509Config,
// keySpec, publishingOptions, issuancePolicy, labels, …) is carried as Fields
// verbatim, matching the certificatemanager/clouddeploy raw-passthrough model, so
// deep loosely-typed sub-blocks need not be enumerated and cannot drift. The CA
// lifecycle state and every computed PEM/description value are seeded into Fields
// once at create and thereafter round-trip as stable stored passthrough values.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Resource is one Certificate Authority Service control-plane resource (a CA
// pool, a certificate authority, a certificate template, or a certificate). Name
// components are stored separately so the full resource name and location scoping
// can be rebuilt without re-parsing; CaPool is set only for the two collections
// nested under a caPool (certificateAuthorities, certificates) and empty
// otherwise. CreateTime/UpdateTime are derived deterministically and stay stable
// across reads. Fields holds every caller-supplied body key verbatim plus any
// computed body value seeded once at create (a CA's state / pemCaCertificates, a
// certificate's pemCertificate), which then round-trips as a stable passthrough.
type Resource struct {
	Project    string
	Location   string
	CaPool     string
	ID         string
	CreateTime time.Time
	UpdateTime time.Time
	Fields     map[string]json.RawMessage
}

// Config is the input to a create or patch. Fields carries the caller-supplied
// body keys (the computed name/createTime/updateTime keys already stripped by the
// wire layer). CaPool is the parent pool id for a nested collection.
type Config struct {
	Project  string
	Location string
	CaPool   string
	ID       string
	Fields   map[string]json.RawMessage
}

// Operation is a completed long-running operation. Every CloudEmu mutation
// finishes synchronously, so Done is always true; the shared poller replays it so
// an SDK or Terraform LRO wait terminates on the first poll.
type Operation struct {
	Name       string // projects/{p}/locations/{loc}/operations/{op}
	Done       bool
	TargetName string // the resource the operation acted on
	Type       string // create | update | delete | enable | disable | undelete | activate
}

// PrivateCA is the control-plane interface a provider implements. The four
// resource collections share the same CRUD/LRO shapes; certificateAuthorities add
// the CA lifecycle verbs and certificates add revoke (and complete synchronously,
// returning the resource rather than an operation).
type PrivateCA interface {
	CreateCaPool(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetCaPool(ctx context.Context, project, location, id string) (*Resource, error)
	ListCaPools(ctx context.Context, project, location string) ([]Resource, error)
	PatchCaPool(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteCaPool(ctx context.Context, project, location, id string) (*Operation, error)

	CreateCertificateAuthority(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetCertificateAuthority(ctx context.Context, project, location, caPool, id string) (*Resource, error)
	ListCertificateAuthorities(ctx context.Context, project, location, caPool string) ([]Resource, error)
	PatchCertificateAuthority(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteCertificateAuthority(ctx context.Context, project, location, caPool, id string) (*Operation, error)
	EnableCertificateAuthority(ctx context.Context, project, location, caPool, id string) (*Resource, *Operation, error)
	DisableCertificateAuthority(ctx context.Context, project, location, caPool, id string) (*Resource, *Operation, error)
	UndeleteCertificateAuthority(ctx context.Context, project, location, caPool, id string) (*Resource, *Operation, error)
	ActivateCertificateAuthority(ctx context.Context, project, location, caPool, id, pemCaCertificate string) (
		*Resource, *Operation, error)
	FetchCertificateAuthorityCSR(ctx context.Context, project, location, caPool, id string) (string, error)

	CreateCertificateTemplate(ctx context.Context, cfg *Config) (*Resource, *Operation, error)
	GetCertificateTemplate(ctx context.Context, project, location, id string) (*Resource, error)
	ListCertificateTemplates(ctx context.Context, project, location string) ([]Resource, error)
	PatchCertificateTemplate(ctx context.Context, cfg *Config, mask []string) (*Resource, *Operation, error)
	DeleteCertificateTemplate(ctx context.Context, project, location, id string) (*Operation, error)

	CreateCertificate(ctx context.Context, cfg *Config) (*Resource, error)
	GetCertificate(ctx context.Context, project, location, caPool, id string) (*Resource, error)
	ListCertificates(ctx context.Context, project, location, caPool string) ([]Resource, error)
	PatchCertificate(ctx context.Context, cfg *Config, mask []string) (*Resource, error)
	RevokeCertificate(ctx context.Context, project, location, caPool, id, reason string) (*Resource, error)

	// GetOperation resolves a (done) long-running operation by name, for a
	// standalone package server's own operations poll.
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
