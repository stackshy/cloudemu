package driver

import "context"

// ResourcePolicy is the resource-based policy attached to a knowledge base.
// RevisionID changes on every put.
type ResourcePolicy struct {
	ResourceARN string
	Policy      string
	RevisionID  string
}

// ResourcePolicies attaches resource-based policies to knowledge bases.
//
// They grant other accounts access. A non-empty expectedRevisionID must equal the current
// revision.
type ResourcePolicies interface {
	PutResourcePolicy(ctx context.Context, resourceARN, policy, expectedRevisionID string) (*ResourcePolicy, error)
	GetResourcePolicy(ctx context.Context, resourceARN string) (*ResourcePolicy, error)
	DeleteResourcePolicy(ctx context.Context, resourceARN, expectedRevisionID string) (*ResourcePolicy, error)
}
