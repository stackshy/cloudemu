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
	// UpdateGCPResource applies mutate to the stored resource in place under the
	// store lock (compute *.patch / *.update), returning NotFound when absent.
	// The handler decides patch-merge vs full-replace by what mutate does to Body.
	UpdateGCPResource(ctx context.Context, collection, scope, name string, mutate func(*GCPResource)) error
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

// GCPBackendBucketCollection is the Collection a Cloud CDN backend bucket is
// stored under; backend buckets are always global.
const GCPBackendBucketCollection = "backendBuckets"

// GCPBackendBucketStore is an OPTIONAL, type-asserted capability implemented
// only by the GCP load-balancer provider. It persists Cloud CDN backend buckets
// (compute.backendBuckets): a global load-balancer backend that serves a Cloud
// Storage bucket. Records are GCPResource values (Collection
// GCPBackendBucketCollection, Scope "global") living alongside the other
// opaque GCP resources, so they snapshot and restore with them. Non-GCP
// providers do not implement it.
type GCPBackendBucketStore interface {
	// InsertGCPBackendBucket stores res, returning AlreadyExists when a backend
	// bucket with the same name already exists.
	InsertGCPBackendBucket(ctx context.Context, res GCPResource) error
	// GetGCPBackendBucket returns the named backend bucket, or NotFound.
	GetGCPBackendBucket(ctx context.Context, name string) (*GCPResource, error)
	// ListGCPBackendBuckets returns every backend bucket.
	ListGCPBackendBuckets(ctx context.Context) ([]GCPResource, error)
	// UpdateGCPBackendBucket applies mutate to the named backend bucket under
	// the store lock (compute backendBuckets.patch / update /
	// setEdgeSecurityPolicy). When mutate returns an error the stored record is
	// left unchanged and that error is returned; mutate must therefore replace
	// Body rather than edit the stored map in place. Returns NotFound when absent.
	UpdateGCPBackendBucket(ctx context.Context, name string, mutate func(*GCPResource) error) error
	// DeleteGCPBackendBucket removes the named backend bucket, returning
	// NotFound when absent.
	DeleteGCPBackendBucket(ctx context.Context, name string) error
}
