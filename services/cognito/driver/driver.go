// Package driver defines the interface and types for the AWS Cognito user-pools
// (cognito-idp) control plane. It models user pools, their app clients, and
// hosted-UI domains, plus resource tagging.
//
// This is the configuration control plane only: creating and reading the pool,
// client, and domain resources and their settings. There is no authentication
// data plane behind the emulator — sign-up, sign-in, token issuance, users, and
// groups are out of scope — so a caller that only provisions Cognito resources
// (Terraform, CloudFormation, the console's create flow) behaves as it would
// against real Cognito, while token/user operations are deferred.
package driver

import "context"

// Cognito is the interface an AWS Cognito user-pools backend implements. It
// covers user pools, app clients, hosted-UI domains, and resource tagging.
type Cognito interface {
	userPoolAPI
	userPoolClientAPI
	userPoolDomainAPI
	tagAPI
}

// userPoolAPI covers the user-pool control plane.
type userPoolAPI interface {
	// CreateUserPool creates a user pool, generating its id and ARN, seeding the
	// 20 default OIDC schema attributes, and materializing the default password
	// policy, MFA configuration, deletion protection, and tier that real Cognito
	// reports back from DescribeUserPool.
	CreateUserPool(ctx context.Context, in CreateUserPoolInput) (*UserPool, error)
	// DescribeUserPool returns a deep copy of a user pool, or a
	// ResourceNotFoundException when it does not exist.
	DescribeUserPool(ctx context.Context, id string) (*UserPool, error)
	// UpdateUserPool applies the mutable pool settings. A nil field is left
	// unchanged; UserPoolTags, when non-nil, replaces the pool's tag set.
	UpdateUserPool(ctx context.Context, in UpdateUserPoolInput) error
	// DeleteUserPool removes a user pool and its clients, domains, and tags.
	DeleteUserPool(ctx context.Context, id string) error
	// ListUserPools returns pool descriptions in a deterministic order.
	ListUserPools(ctx context.Context, page Pagination) ([]UserPoolDescription, string, error)
	// GetUserPoolMfaConfig returns a pool's MFA configuration. The Terraform AWS
	// provider reads this on every user-pool refresh, so it must return the
	// pool's MfaConfiguration (default OFF) alongside its SMS/software-token
	// settings.
	GetUserPoolMfaConfig(ctx context.Context, id string) (*UserPoolMfaConfig, error)
	// SetUserPoolMfaConfig replaces a pool's MFA configuration and returns the
	// stored result.
	SetUserPoolMfaConfig(ctx context.Context, in SetUserPoolMfaConfigInput) (*UserPoolMfaConfig, error)
}

// userPoolClientAPI covers app clients within a user pool.
type userPoolClientAPI interface {
	// CreateUserPoolClient creates an app client, generating its 26-character id
	// and (only when GenerateSecret is set) a 51-character secret, and
	// materializing the token-validity, auth-flow, and existence-error defaults.
	CreateUserPoolClient(ctx context.Context, in CreateUserPoolClientInput) (*UserPoolClient, error)
	DescribeUserPoolClient(ctx context.Context, userPoolID, clientID string) (*UserPoolClient, error)
	// UpdateUserPoolClient replaces the client's settings and returns the result.
	// The client secret is never regenerated or cleared by an update.
	UpdateUserPoolClient(ctx context.Context, in CreateUserPoolClientInput) (*UserPoolClient, error)
	DeleteUserPoolClient(ctx context.Context, userPoolID, clientID string) error
	// ListUserPoolClients returns client descriptions in a user pool in a
	// deterministic order.
	ListUserPoolClients(ctx context.Context, userPoolID string, page Pagination) ([]UserPoolClientDescription, string, error)
}

// userPoolDomainAPI covers hosted-UI domains.
type userPoolDomainAPI interface {
	CreateUserPoolDomain(ctx context.Context, in CreateUserPoolDomainInput) error
	// DescribeUserPoolDomain returns a domain's description. An unknown domain
	// yields an empty (zero) description and no error, matching real Cognito,
	// which returns an empty DomainDescription member rather than a 404.
	DescribeUserPoolDomain(ctx context.Context, domain string) (*UserPoolDomain, error)
	DeleteUserPoolDomain(ctx context.Context, domain, userPoolID string) error
}

// tagAPI covers resource tagging, keyed by the resource ARN the caller supplies.
type tagAPI interface {
	TagResource(ctx context.Context, resourceARN string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceARN string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceARN string) (map[string]string, error)
}
