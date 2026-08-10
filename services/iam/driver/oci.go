package driver

import "context"

// CompartmentSpec describes a compartment to create.
type CompartmentSpec struct {
	ParentID     string
	Name         string
	Description  string
	FreeformTags map[string]string
}

// CompartmentInfo describes a compartment and its place in the tree.
type CompartmentInfo struct {
	ID             string
	ParentID       string
	Name           string
	Description    string
	TimeCreated    string
	LifecycleState string
	FreeformTags   map[string]string
}

// IdentityUpdate carries the mutable fields of a compartment, user or group.
// An empty field leaves the stored value alone.
type IdentityUpdate struct {
	Name         string
	Description  string
	FreeformTags map[string]string
}

// Compartments is an OPTIONAL capability, discovered by type assertion (like
// storage/driver.BucketAttributes). Compartments are OCI's resource container
// and they nest; no other cloud in this repo has an equivalent, so the portable
// IAM interface does not model them.
type Compartments interface {
	CreateCompartment(ctx context.Context, spec CompartmentSpec) (*CompartmentInfo, error)
	GetCompartment(ctx context.Context, id string) (*CompartmentInfo, error)
	// ListCompartments returns the direct children of parentID, or every
	// descendant when inSubtree is set.
	ListCompartments(ctx context.Context, parentID string, inSubtree bool) ([]CompartmentInfo, error)
	UpdateCompartment(ctx context.Context, id string, upd IdentityUpdate) (*CompartmentInfo, error)
	DeleteCompartment(ctx context.Context, id string) error
}

// PrincipalSpec describes a compartment-scoped user or group to create.
type PrincipalSpec struct {
	CompartmentID string
	Name          string
	Description   string
	FreeformTags  map[string]string
}

// PrincipalInfo describes a compartment-scoped user or group.
type PrincipalInfo struct {
	ID             string
	CompartmentID  string
	Name           string
	Description    string
	TimeCreated    string
	LifecycleState string
	FreeformTags   map[string]string
}

// MembershipInfo binds a user to a group as its own addressable resource.
type MembershipInfo struct {
	ID             string
	CompartmentID  string
	UserID         string
	GroupID        string
	TimeCreated    string
	LifecycleState string
}

// OCIIdentity is an OPTIONAL capability, discovered by type assertion. The
// portable interface keys users and groups by name and lists them unscoped;
// OCI addresses them by OCID, scopes them to a compartment, and makes group
// membership a resource in its own right.
type OCIIdentity interface {
	CreateOCIUser(ctx context.Context, spec PrincipalSpec) (*PrincipalInfo, error)
	GetOCIUser(ctx context.Context, id string) (*PrincipalInfo, error)
	ListOCIUsers(ctx context.Context, compartmentID string) ([]PrincipalInfo, error)
	UpdateOCIUser(ctx context.Context, id string, upd IdentityUpdate) (*PrincipalInfo, error)
	DeleteOCIUser(ctx context.Context, id string) error

	CreateOCIGroup(ctx context.Context, spec PrincipalSpec) (*PrincipalInfo, error)
	GetOCIGroup(ctx context.Context, id string) (*PrincipalInfo, error)
	ListOCIGroups(ctx context.Context, compartmentID string) ([]PrincipalInfo, error)
	UpdateOCIGroup(ctx context.Context, id string, upd IdentityUpdate) (*PrincipalInfo, error)
	DeleteOCIGroup(ctx context.Context, id string) error

	CreateOCIGroupMembership(ctx context.Context, userID, groupID string) (*MembershipInfo, error)
	GetOCIGroupMembership(ctx context.Context, id string) (*MembershipInfo, error)
	// ListOCIGroupMemberships filters by compartment, and further by userID or
	// groupID when either is non-empty.
	ListOCIGroupMemberships(ctx context.Context, compartmentID, userID, groupID string) ([]MembershipInfo, error)
	DeleteOCIGroupMembership(ctx context.Context, id string) error
}

// PolicySpec describes a statement policy to create.
type PolicySpec struct {
	CompartmentID string
	Name          string
	Description   string
	Statements    []string
	FreeformTags  map[string]string
}

// PolicyUpdate carries the mutable fields of a statement policy. Nil Statements
// leaves the stored statements alone.
type PolicyUpdate struct {
	Description  string
	Statements   []string
	FreeformTags map[string]string
}

// StatementPolicyInfo describes a statement policy.
type StatementPolicyInfo struct {
	ID             string
	CompartmentID  string
	Name           string
	Description    string
	Statements     []string
	TimeCreated    string
	VersionDate    string
	LifecycleState string
	FreeformTags   map[string]string
}

// AccessRequest is an access check evaluated against policy statements.
type AccessRequest struct {
	// Groups and DynamicGroups are the names or OCIDs the principal belongs to.
	Groups        []string
	DynamicGroups []string
	// AnyUser reports whether the principal is an authenticated user, which is
	// what an "any-user" subject grants to.
	AnyUser bool
	// Verb is one of inspect, read, use, manage.
	Verb string
	// ResourceType is an OCI resource type or family, e.g. buckets,
	// object-family, all-resources.
	ResourceType string
	// CompartmentID is the compartment the target resource lives in.
	CompartmentID string
}

// StatementPolicies is an OPTIONAL capability, discovered by type assertion.
// OCI policies are English-like statement strings evaluated against a
// compartment subtree, not the JSON documents the portable policy operations
// assume, so neither the document model nor policy attachment applies.
type StatementPolicies interface {
	CreateStatementPolicy(ctx context.Context, spec *PolicySpec) (*StatementPolicyInfo, error)
	GetStatementPolicy(ctx context.Context, id string) (*StatementPolicyInfo, error)
	ListStatementPolicies(ctx context.Context, compartmentID string) ([]StatementPolicyInfo, error)
	UpdateStatementPolicy(ctx context.Context, id string, upd PolicyUpdate) (*StatementPolicyInfo, error)
	DeleteStatementPolicy(ctx context.Context, id string) error
	// Evaluate reports whether any statement grants the request. OCI policies
	// are allow-only, so there is nothing for a deny to override. A statement
	// the implementation cannot resolve returns Unimplemented rather than a
	// grant, so a restriction is never silently dropped.
	Evaluate(ctx context.Context, req *AccessRequest) (bool, error)
}
