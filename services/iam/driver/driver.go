// Package driver defines the interface for IAM service implementations.
package driver

import "context"

// UserConfig describes a user to create.
type UserConfig struct {
	Name string
	Path string
	Tags map[string]string
}

// UserInfo describes an IAM user.
type UserInfo struct {
	Name      string
	ID        string
	ARN       string
	Path      string
	Tags      map[string]string
	CreatedAt string
}

// RoleConfig describes a role to create.
type RoleConfig struct {
	Name                string
	Path                string
	Description         string
	AssumeRolePolicyDoc string
	MaxSessionDuration  int
	Tags                map[string]string
}

// RoleInfo describes an IAM role.
type RoleInfo struct {
	Name                string
	ID                  string
	ARN                 string
	Path                string
	Description         string
	AssumeRolePolicyDoc string
	MaxSessionDuration  int
	CreatedAt           string
	Tags                map[string]string
}

// PolicyConfig describes a policy to create.
type PolicyConfig struct {
	Name           string
	Path           string
	PolicyDocument string
	Description    string
}

// PolicyInfo describes an IAM policy.
type PolicyInfo struct {
	Name           string
	ID             string
	ARN            string
	Path           string
	PolicyDocument string
	Description    string
}

// PolicyVersionConfig describes a new version of a managed policy to create.
type PolicyVersionConfig struct {
	PolicyARN      string
	PolicyDocument string
	SetAsDefault   bool
}

// PolicyVersionInfo describes a single version of a managed policy.
type PolicyVersionInfo struct {
	VersionID        string
	PolicyDocument   string
	IsDefaultVersion bool
	CreatedAt        string
}

// GroupConfig describes a group to create.
type GroupConfig struct {
	Name string
	Path string
}

// GroupInfo describes an IAM group.
type GroupInfo struct {
	Name      string
	ID        string
	Path      string
	ARN       string
	CreatedAt string
}

// AccessKeyConfig describes an access key to create.
type AccessKeyConfig struct {
	UserName string
}

// AccessKeyInfo describes an IAM access key.
type AccessKeyInfo struct {
	AccessKeyID     string
	SecretAccessKey string
	UserName        string
	Status          string
	CreatedAt       string
}

// InstanceProfileConfig describes an instance profile to create.
type InstanceProfileConfig struct {
	Name     string
	Path     string
	RoleName string
	Tags     map[string]string
}

// InstanceProfileInfo describes an IAM instance profile.
type InstanceProfileInfo struct {
	ID        string
	Name      string
	Path      string
	RoleName  string
	ARN       string
	CreatedAt string
	Tags      map[string]string
}

// SimulationResult is one action-on-resource evaluation returned by an IAM
// policy simulation (SimulatePrincipalPolicy / SimulateCustomPolicy). Decision
// is one of "allowed", "explicitDeny", or "implicitDeny". It is an AWS-only
// shape, so it is not referenced by the IAM interface below; providers that
// support simulation expose it through a type-asserted optional method.
type SimulationResult struct {
	ActionName   string
	ResourceName string
	Decision     string
}

// PolicyEntity is one principal (user, group, or role) that a managed policy is
// attached to. Path lets the wire layer apply the ListEntitiesForPolicy
// PathPrefix filter. It is an AWS-only shape (ListEntitiesForPolicy), so it is
// not referenced by the IAM interface below; providers that support it expose
// it through a type-asserted optional method.
type PolicyEntity struct {
	Name string
	ID   string
	Path string
}

// PolicyEntities are the principals a managed policy is attached to, split by
// type. AWS-only (ListEntitiesForPolicy).
type PolicyEntities struct {
	Users  []PolicyEntity
	Groups []PolicyEntity
	Roles  []PolicyEntity
}

// PasswordPolicy describes an AWS account password policy. ExpirePasswords is
// derived (MaxPasswordAge > 0) and reported by the wire layer, not stored. It
// is AWS-only and not referenced by the IAM interface.
type PasswordPolicy struct {
	MinimumPasswordLength      int
	RequireSymbols             bool
	RequireNumbers             bool
	RequireUppercaseCharacters bool
	RequireLowercaseCharacters bool
	AllowUsersToChangePassword bool
	MaxPasswordAge             int
	PasswordReusePrevention    int
	HardExpiry                 bool
}

// MFADeviceInfo describes an MFA device assigned to a user. AWS-only.
type MFADeviceInfo struct {
	UserName     string
	SerialNumber string
	EnableDate   string
}

// VirtualMFADeviceInfo describes a newly created virtual MFA device. The seed
// and QR-code payloads are opaque bytes the wire layer base64-encodes. AWS-only.
type VirtualMFADeviceInfo struct {
	SerialNumber     string
	Base32StringSeed []byte
	QRCodePNG        []byte
}

// VirtualMFADeviceMetadata describes a virtual MFA device as returned by
// ListVirtualMFADevices. Unlike VirtualMFADeviceInfo it carries no seed/QR
// payload (real AWS omits both from the list response) and instead reports
// the assigned user, nil for a device that has not been enabled. AWS-only.
type VirtualMFADeviceMetadata struct {
	SerialNumber string
	EnableDate   string
	AssignedUser *UserInfo
}

// AccessKeyAuth carries the secret and owning principal for one access key id.
// It is used by the AWS SigV4 request-authentication gate to verify an
// incoming signature and resolve the caller, and by STS GetCallerIdentity to
// reflect the presented credential's owning user; the secret never leaves the
// server. It is AWS-only, so it is not referenced by the IAM interface below.
type AccessKeyAuth struct {
	AccessKeyID     string
	SecretAccessKey string
	UserName        string
	UserARN         string
	// UserID is the owning user's unique id (the "AIDA..." value CreateUser
	// generates), reported by GetCallerIdentity as UserId.
	UserID    string
	AccountID string
}

// AccessKeyResolver is an optional capability: an IAM implementation that can
// resolve an access key id to its secret and owning principal. The AWS SigV4
// authentication gate type-asserts for it. It is deliberately NOT part of the
// IAM interface because all four providers (AWS, Azure, GCP, OCI) share that
// interface, yet only the AWS provider serves SigV4-authenticated requests.
type AccessKeyResolver interface {
	AccessKeyByID(ctx context.Context, id string) (AccessKeyAuth, bool)
}

// AccessKeyImporter is an optional capability: an IAM implementation that can
// register an access key whose id and secret the caller chooses. The seed
// fixtures use it so the first IAM user under --enforce-auth gets a key the
// operator already knows (CreateAccessKey would need a signed request, and
// there is no key to sign it with yet). AWS-only, like AccessKeyResolver.
type AccessKeyImporter interface {
	ImportAccessKey(ctx context.Context, userName, accessKeyID, secretAccessKey string) error
}

// PolicyInspector is an optional capability: an IAM implementation that can
// report whether a principal has any policies in effect. The AWS authorization
// gate type-asserts for it so it can leave a principal with no policies defined
// unrestricted (a dev-friendly bootstrap default), enforcing CheckPermission
// only once policies exist. Like AccessKeyResolver it is AWS-only and therefore
// not part of the shared IAM interface.
type PolicyInspector interface {
	PrincipalHasPolicies(ctx context.Context, name string) bool
}

// ContextualAuthorizer is an optional capability: an IAM implementation that can
// evaluate a permission with an explicit request condition context (aws:SourceIp,
// aws:CurrentTime, aws:SecureTransport, aws:PrincipalArn, aws:RequestedRegion, …)
// so statements guarded by a Condition are evaluated against the real request.
// The AWS authorization gate type-asserts for it to authorize the routed
// action against the derived target resource with the caller's context. Like
// PolicyInspector it is AWS-only and therefore not part of the shared IAM
// interface. A nil context makes it equivalent to CheckPermission.
type ContextualAuthorizer interface {
	CheckPermissionWithContext(
		ctx context.Context, principal, action, resource string, condCtx map[string]string,
	) (bool, error)
}

// Decision is the outcome of one policy evaluation. The values match the AWS
// SimulatePolicy EvalDecision strings.
type Decision string

// Policy evaluation outcomes. An explicit Deny always wins; an implicit deny
// means no statement allowed the request.
const (
	DecisionAllowed      Decision = "allowed"
	DecisionImplicitDeny Decision = "implicitDeny"
	DecisionExplicitDeny Decision = "explicitDeny"
)

// EvalRequest is one permission question for PermissionEvaluator. When
// ResourceKnown is false the caller cannot name the target resource, and the
// evaluator answers conservatively: it never allows more than it would for any
// concrete resource. Resource is ignored in that case.
type EvalRequest struct {
	Principal     string
	Action        string
	Resource      string
	ResourceKnown bool
	Context       map[string]string
}

// PermissionEvaluator is an optional capability: an IAM implementation that
// reports the full tri-state decision (allowed, implicit deny, explicit deny)
// and can evaluate a request whose resource is unknown, or a whole service
// ("may this principal use any s3 action on anything?"). The AWS authorization
// gate type-asserts for it. Like ContextualAuthorizer it is AWS-only and
// therefore not part of the shared IAM interface.
type PermissionEvaluator interface {
	EvaluatePermission(ctx context.Context, req EvalRequest) Decision
	EvaluateServiceWide(ctx context.Context, principal, service string, condCtx map[string]string) Decision
}

// ConditionValueSeparator joins the values of a multivalued condition key
// (aws:TagKeys, sts:TransitiveTagKeys) in a condition context. Tag keys
// cannot contain it.
const ConditionValueSeparator = "\x1f"

// TrustRequest asks whether a role's trust policy lets a caller perform
// Action (sts:AssumeRole, sts:TagSession, sts:SetSourceIdentity) on it.
// CallerARNs are every ARN the caller is known by: an IAM user's ARN, or a
// role session's role ARN and assumed-role ARN. CallerAccount is the
// caller's account, matched by a trust that names the account.
type TrustRequest struct {
	RoleName      string
	Action        string
	CallerARNs    []string
	CallerAccount string
	Context       map[string]string
}

// TrustResult is the trust policy's answer. NamedDirectly is set when an
// allowing statement names the caller's IAM user, role session or federated
// user ARN exactly. NamedRole is set when it names the IAM role ARN of a role
// session caller. Naming the caller's account or "*" sets neither.
type TrustResult struct {
	RoleExists    bool
	Allow         bool
	ExplicitDeny  bool
	NamedDirectly bool
	NamedRole     bool
}

// TrustEvaluator is an optional capability: an IAM implementation that
// evaluates a role trust policy for a real caller, with principal types,
// NotPrincipal and conditions. The STS handler type-asserts for it. AWS-only.
type TrustEvaluator interface {
	EvaluateTrust(ctx context.Context, req *TrustRequest) TrustResult
}

// BoundaryEvaluator is an optional capability: an IAM implementation that
// evaluates a principal's permissions boundary on its own. A principal with
// no boundary is allowed. A resource policy that grants an IAM role ARN is
// still limited by the role's boundary, so the STS handler needs the boundary
// decision apart from the identity decision. AWS-only.
type BoundaryEvaluator interface {
	EvaluateBoundary(ctx context.Context, req EvalRequest) Decision
}

// IAM is the interface that IAM provider implementations must satisfy.
type IAM interface {
	CreateUser(ctx context.Context, config UserConfig) (*UserInfo, error)
	DeleteUser(ctx context.Context, name string) error
	GetUser(ctx context.Context, name string) (*UserInfo, error)
	ListUsers(ctx context.Context) ([]UserInfo, error)

	CreateRole(ctx context.Context, config RoleConfig) (*RoleInfo, error)
	DeleteRole(ctx context.Context, name string) error
	GetRole(ctx context.Context, name string) (*RoleInfo, error)
	ListRoles(ctx context.Context) ([]RoleInfo, error)

	CreatePolicy(ctx context.Context, config PolicyConfig) (*PolicyInfo, error)
	DeletePolicy(ctx context.Context, arn string) error
	GetPolicy(ctx context.Context, arn string) (*PolicyInfo, error)
	ListPolicies(ctx context.Context) ([]PolicyInfo, error)

	CreatePolicyVersion(ctx context.Context, config PolicyVersionConfig) (*PolicyVersionInfo, error)
	GetPolicyVersion(ctx context.Context, policyARN, versionID string) (*PolicyVersionInfo, error)
	ListPolicyVersions(ctx context.Context, policyARN string) ([]PolicyVersionInfo, error)
	DeletePolicyVersion(ctx context.Context, policyARN, versionID string) error
	SetDefaultPolicyVersion(ctx context.Context, policyARN, versionID string) error

	AttachUserPolicy(ctx context.Context, userName, policyARN string) error
	DetachUserPolicy(ctx context.Context, userName, policyARN string) error
	AttachRolePolicy(ctx context.Context, roleName, policyARN string) error
	DetachRolePolicy(ctx context.Context, roleName, policyARN string) error

	ListAttachedUserPolicies(ctx context.Context, userName string) ([]string, error)
	ListAttachedRolePolicies(ctx context.Context, roleName string) ([]string, error)

	CheckPermission(ctx context.Context, principal, action, resource string) (bool, error)

	CreateGroup(ctx context.Context, config GroupConfig) (*GroupInfo, error)
	DeleteGroup(ctx context.Context, name string) error
	GetGroup(ctx context.Context, name string) (*GroupInfo, error)
	ListGroups(ctx context.Context) ([]GroupInfo, error)

	AddUserToGroup(ctx context.Context, userName, groupName string) error
	RemoveUserFromGroup(ctx context.Context, userName, groupName string) error
	ListGroupsForUser(ctx context.Context, userName string) ([]GroupInfo, error)

	CreateAccessKey(ctx context.Context, config AccessKeyConfig) (*AccessKeyInfo, error)
	DeleteAccessKey(ctx context.Context, userName, accessKeyID string) error
	ListAccessKeys(ctx context.Context, userName string) ([]AccessKeyInfo, error)

	CreateInstanceProfile(ctx context.Context, config InstanceProfileConfig) (*InstanceProfileInfo, error)
	DeleteInstanceProfile(ctx context.Context, name string) error
	GetInstanceProfile(ctx context.Context, name string) (*InstanceProfileInfo, error)
	ListInstanceProfiles(ctx context.Context) ([]InstanceProfileInfo, error)
	AddRoleToInstanceProfile(ctx context.Context, profileName, roleName string) error
	RemoveRoleFromInstanceProfile(ctx context.Context, profileName, roleName string) error
}
