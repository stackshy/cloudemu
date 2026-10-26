package driver

import "context"

// ResourcePolicy is the resource-based policy attached to a domain or a
// repository. Revision changes on every successful put and is the optimistic
// lock token of the next put or delete.
type ResourcePolicy struct {
	ResourceArn string
	Revision    string
	Document    string
}

// Policies is the optional capability for domain and repository permissions policies.
//
// A put or delete that names a revision other than the current one is a ConflictException.
type Policies interface {
	// PutDomainPermissionsPolicy sets the resource policy on a domain.
	PutDomainPermissionsPolicy(ctx context.Context, ref DomainRef, policyRevision, document string) (*ResourcePolicy, error)
	// GetDomainPermissionsPolicy returns the resource policy on a domain.
	GetDomainPermissionsPolicy(ctx context.Context, ref DomainRef) (*ResourcePolicy, error)
	// DeleteDomainPermissionsPolicy removes the resource policy from a domain.
	DeleteDomainPermissionsPolicy(ctx context.Context, ref DomainRef, policyRevision string) (*ResourcePolicy, error)

	// PutRepositoryPermissionsPolicy sets the resource policy on a repository.
	PutRepositoryPermissionsPolicy(ctx context.Context, ref RepositoryRef, policyRevision, document string) (*ResourcePolicy, error)
	// GetRepositoryPermissionsPolicy returns the resource policy on a repository.
	GetRepositoryPermissionsPolicy(ctx context.Context, ref RepositoryRef) (*ResourcePolicy, error)
	// DeleteRepositoryPermissionsPolicy removes the resource policy from a repository.
	DeleteRepositoryPermissionsPolicy(ctx context.Context, ref RepositoryRef, policyRevision string) (*ResourcePolicy, error)
}
