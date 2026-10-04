// Package driver defines the interface and types for AWS Cognito user pools
// (cognito-idp). It models user pools, their app clients, hosted-UI domains,
// resource tagging, the users of a pool with the admin user-management
// operations, groups, self sign-up, and password sign-in with RS256 tokens.
package driver

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/jwtsign"
)

// Cognito is the interface an AWS Cognito user-pools backend implements. It
// covers user pools, app clients, hosted-UI domains, and resource tagging.
type Cognito interface {
	userPoolAPI
	userPoolClientAPI
	userPoolDomainAPI
	tagAPI
	userAPI
	groupAPI
	signUpAPI
	authAPI
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
	// DeleteUserPool removes a user pool with its users, clients and tags. Like
	// real Cognito it refuses (InvalidParameterException) while deletion
	// protection is ACTIVE or a hosted-UI domain is still attached.
	DeleteUserPool(ctx context.Context, id string) error
	// AddCustomAttributes appends custom attributes to a pool's schema. Names get
	// the "custom:" prefix, and an existing name is rejected.
	AddCustomAttributes(ctx context.Context, userPoolID string, attrs []SchemaAttribute) error
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

// userAPI covers the users of a user pool and the admin user-management
// operations. Usernames are looked up the way Cognito does: by username, or by
// email/phone number when the pool uses them as username attributes or aliases.
type userAPI interface {
	// AdminCreateUser creates a user in FORCE_CHANGE_PASSWORD with a generated
	// sub. MessageAction RESEND re-invites an existing FORCE_CHANGE_PASSWORD user.
	AdminCreateUser(ctx context.Context, in AdminCreateUserInput) (*User, error)
	AdminGetUser(ctx context.Context, userPoolID, username string) (*User, error)
	// ListUsers returns users sorted by username, filtered by an optional
	// `attr = "v"` or `attr ^= "v"` expression on a searchable attribute.
	ListUsers(ctx context.Context, in ListUsersInput) ([]User, string, error)
	AdminDeleteUser(ctx context.Context, userPoolID, username string) error
	AdminUpdateUserAttributes(ctx context.Context, userPoolID, username string, attrs []Attribute) error
	AdminDeleteUserAttributes(ctx context.Context, userPoolID, username string, names []string) error
	// AdminSetUserPassword sets a password checked against the pool policy. A
	// permanent password confirms the user; otherwise it is FORCE_CHANGE_PASSWORD.
	AdminSetUserPassword(ctx context.Context, userPoolID, username, password string, permanent bool) error
	AdminEnableUser(ctx context.Context, userPoolID, username string) error
	AdminDisableUser(ctx context.Context, userPoolID, username string) error
	// AdminResetUserPassword moves the user to RESET_REQUIRED.
	AdminResetUserPassword(ctx context.Context, userPoolID, username string) error
}

// groupAPI covers user-pool groups and group membership.
type groupAPI interface {
	// CreateGroup creates a group. A name already in the pool fails with
	// GroupExistsException.
	CreateGroup(ctx context.Context, in CreateGroupInput) (*Group, error)
	GetGroup(ctx context.Context, userPoolID, groupName string) (*Group, error)
	// UpdateGroup changes the fields the input sets and returns the group.
	UpdateGroup(ctx context.Context, in UpdateGroupInput) (*Group, error)
	// DeleteGroup removes a group and every membership in it.
	DeleteGroup(ctx context.Context, userPoolID, groupName string) error
	ListGroups(ctx context.Context, userPoolID string, page Pagination) ([]Group, string, error)
	AdminAddUserToGroup(ctx context.Context, userPoolID, username, groupName string) error
	AdminRemoveUserFromGroup(ctx context.Context, userPoolID, username, groupName string) error
	AdminListGroupsForUser(ctx context.Context, userPoolID, username string, page Pagination) ([]Group, string, error)
	ListUsersInGroup(ctx context.Context, userPoolID, groupName string, page Pagination) ([]User, string, error)
}

// signUpAPI covers self-service registration through an app client.
type signUpAPI interface {
	// SignUp registers an UNCONFIRMED user, checks the password against the
	// pool policy, and issues a confirmation code.
	SignUp(ctx context.Context, in SignUpInput) (*SignUpOutput, error)
	// ConfirmSignUp confirms a user with the code SignUp or
	// ResendConfirmationCode issued.
	ConfirmSignUp(ctx context.Context, in ConfirmSignUpInput) error
	ResendConfirmationCode(ctx context.Context, in ClientUserInput) (*CodeDeliveryDetails, error)
	AdminConfirmSignUp(ctx context.Context, userPoolID, username string) error
}

// authAPI covers password sign-in, challenges, and the token-authenticated
// user operations. Tokens are RS256 JWTs signed with per-pool keys.
type authAPI interface {
	// InitiateAuth runs USER_PASSWORD_AUTH or REFRESH_TOKEN_AUTH for a client.
	InitiateAuth(ctx context.Context, in InitiateAuthInput) (*AuthResult, error)
	// AdminInitiateAuth runs ADMIN_USER_PASSWORD_AUTH, ADMIN_NO_SRP_AUTH or
	// REFRESH_TOKEN_AUTH for a client of in.UserPoolID.
	AdminInitiateAuth(ctx context.Context, in InitiateAuthInput) (*AuthResult, error)
	// RespondToAuthChallenge answers the NEW_PASSWORD_REQUIRED challenge.
	RespondToAuthChallenge(ctx context.Context, in RespondToAuthChallengeInput) (*AuthResult, error)
	AdminRespondToAuthChallenge(ctx context.Context, in RespondToAuthChallengeInput) (*AuthResult, error)
	// GetUser returns the user an access token was issued to.
	GetUser(ctx context.Context, accessToken string) (*User, error)
	// GlobalSignOut revokes every token issued to the access token's user.
	GlobalSignOut(ctx context.Context, accessToken string) error
	AdminUserGlobalSignOut(ctx context.Context, userPoolID, username string) error
	// RevokeToken revokes a refresh token and the access and ID tokens minted
	// from the same authentication.
	RevokeToken(ctx context.Context, in RevokeTokenInput) error
}

// KeySetProvider publishes a user pool's token-signing keys and issuer for the
// /{poolId}/.well-known endpoints. It is not a cognito-idp API operation.
type KeySetProvider interface {
	// SigningKeys returns the pool's issuer URL and its public JSON Web Key Set.
	SigningKeys(ctx context.Context, userPoolID string) (issuer string, keys jwtsign.JWKSet, err error)
}

// CodeInspector exposes the confirmation codes the emulator would have sent by
// email or SMS, so a test can complete ConfirmSignUp. It is not a cognito-idp
// API operation.
type CodeInspector interface {
	ConfirmationCode(ctx context.Context, userPoolID, username string) (*IssuedCode, error)
}
