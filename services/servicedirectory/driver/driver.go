// Package driver defines the portable interface for the Google Cloud Service
// Directory control plane (servicedirectory.googleapis.com/v1). It is
// control-plane only — the three nested, client-named resource collections a
// Terraform google provider or a real google.golang.org/api/servicedirectory/v1
// client CRUDs are modeled:
//
//	projects/{p}/locations/{loc}/namespaces/{ns}
//	projects/{p}/locations/{loc}/namespaces/{ns}/services/{svc}
//	projects/{p}/locations/{loc}/namespaces/{ns}/services/{svc}/endpoints/{ep}
//
// Service resolution (the LookupService data plane, POST :resolve) is out of
// scope: CloudEmu emulates the registration control plane, not the runtime
// name-resolution service.
//
// Every Service Directory mutation is synchronous REST: Create/Get/List/Patch/
// Delete return the resource (or empty) directly, with no google.longrunning
// Operation wrapper — unlike most GCP control planes. No operation registry is
// involved.
//
// Each resource carries exactly one computed, output-only field — uid, a stable
// server-assigned UUID4 — minted once at create and returned unchanged on every
// read so a Terraform refresh never drifts. The resource name is the full
// deterministic path. Every other field (labels, annotations, address, port,
// network) is caller-supplied and round-trips verbatim.
package driver

import "context"

// Namespace is one Service Directory namespace. Name components are stored
// separately so the full resource name and parent scoping rebuild without
// re-parsing. UID is minted once at create and stays stable across reads.
type Namespace struct {
	Project  string
	Location string
	ID       string
	UID      string
	Labels   map[string]string
}

// Service is one Service Directory service under a namespace. Annotations is the
// caller-supplied string map (the Terraform google_service_directory_service
// `metadata` block maps to it). Endpoints are intentionally not embedded: the
// v1 control plane returns them only on ResolveService, and Terraform reads each
// endpoint as its own resource.
type Service struct {
	Project     string
	Location    string
	Namespace   string
	ID          string
	UID         string
	Annotations map[string]string
}

// Endpoint is one Service Directory endpoint under a service. Address and Port
// are the caller-supplied network target; Network is the optional VPC path.
// Annotations is the caller-supplied string map (Terraform `metadata`).
type Endpoint struct {
	Project     string
	Location    string
	Namespace   string
	Service     string
	ID          string
	UID         string
	Address     string
	Port        int
	Annotations map[string]string
	Network     string
}

// NamespaceConfig is the input to a namespace create or patch. ID is the
// client-assigned namespace id (from ?namespaceId= or the body name).
type NamespaceConfig struct {
	Project  string
	Location string
	ID       string
	Labels   map[string]string
}

// ServiceConfig is the input to a service create or patch.
type ServiceConfig struct {
	Project     string
	Location    string
	Namespace   string
	ID          string
	Annotations map[string]string
}

// EndpointConfig is the input to an endpoint create or patch.
type EndpointConfig struct {
	Project     string
	Location    string
	Namespace   string
	Service     string
	ID          string
	Address     string
	Port        int
	Annotations map[string]string
	Network     string
}

// ServiceDirectory is the control-plane interface a provider implements. Every
// method is synchronous; deleting a parent cascades to its descendants.
type ServiceDirectory interface {
	CreateNamespace(ctx context.Context, cfg *NamespaceConfig) (*Namespace, error)
	GetNamespace(ctx context.Context, project, location, id string) (*Namespace, error)
	ListNamespaces(ctx context.Context, project, location string) ([]Namespace, error)
	PatchNamespace(ctx context.Context, cfg *NamespaceConfig, mask []string) (*Namespace, error)
	DeleteNamespace(ctx context.Context, project, location, id string) error

	CreateService(ctx context.Context, cfg *ServiceConfig) (*Service, error)
	GetService(ctx context.Context, project, location, namespace, id string) (*Service, error)
	ListServices(ctx context.Context, project, location, namespace string) ([]Service, error)
	PatchService(ctx context.Context, cfg *ServiceConfig, mask []string) (*Service, error)
	DeleteService(ctx context.Context, project, location, namespace, id string) error

	CreateEndpoint(ctx context.Context, cfg *EndpointConfig) (*Endpoint, error)
	GetEndpoint(ctx context.Context, project, location, namespace, service, id string) (*Endpoint, error)
	ListEndpoints(ctx context.Context, project, location, namespace, service string) ([]Endpoint, error)
	PatchEndpoint(ctx context.Context, cfg *EndpointConfig, mask []string) (*Endpoint, error)
	DeleteEndpoint(ctx context.Context, project, location, namespace, service, id string) error
}
