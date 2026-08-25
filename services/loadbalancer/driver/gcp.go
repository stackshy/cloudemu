package driver

import "context"

// GCPResource is an opaque GCP global/regional Compute Load Balancing resource
// (healthChecks, targetPools, urlMaps) that the portable LoadBalancer model
// can't express. The GCP wire handler stores the decoded insert/patch body
// verbatim and re-emits it, so every field the client sent round-trips exactly.
// Server-injected identity (ID, CreationTimestamp) lives alongside the body.
type GCPResource struct {
	Collection        string         // "healthChecks", "targetPools", "urlMaps"
	Scope             string         // "global" or a region name
	Name              string         // user-assigned resource name (key)
	ID                string         // numeric string, GCP wire ID
	CreationTimestamp string         // RFC3339
	Body              map[string]any // decoded insert body
}

// GCPComputeResourceStore is an OPTIONAL, type-asserted capability implemented
// only by the GCP load-balancer provider. It persists opaque GCP Compute Load
// Balancing resources (healthChecks, targetPools, urlMaps) that have no
// cross-provider driver model, keyed by (collection, scope, name). Non-GCP
// providers do not implement it.
type GCPComputeResourceStore interface {
	// PutGCPResource stores res, returning AlreadyExists when a resource with
	// the same (collection, scope, name) already exists.
	PutGCPResource(ctx context.Context, res GCPResource) error
	// GetGCPResource returns the stored resource, or NotFound.
	GetGCPResource(ctx context.Context, collection, scope, name string) (*GCPResource, error)
	// ListGCPResources returns every resource in a (collection, scope) bucket.
	ListGCPResources(ctx context.Context, collection, scope string) ([]GCPResource, error)
	// DeleteGCPResource removes the resource, returning NotFound when absent.
	DeleteGCPResource(ctx context.Context, collection, scope, name string) error
}

// GCPBackendServicePatcher is an OPTIONAL, type-asserted capability implemented
// only by the GCP load-balancer provider. It applies an in-place mutation to a
// backend-service-backed target group (GCP compute.backendServices.patch),
// holding the store lock across the read-modify-write so overlapping patches
// don't lose updates. Non-GCP providers do not implement it.
type GCPBackendServicePatcher interface {
	// PatchGCPBackendService looks up the target group by its GCP name and
	// applies mutate to it in place, returning NotFound when no such backend
	// service exists.
	PatchGCPBackendService(ctx context.Context, name string, mutate func(*TargetGroupInfo)) error
}
