package identity

import iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"

// definedTags is OCI's namespaced tag map. CloudEmu records only freeform
// tags, so the field is always present and empty.
type definedTags map[string]map[string]any

// createPrincipalBody is the request body of CreateUser and CreateGroup.
type createPrincipalBody struct {
	CompartmentID string            `json:"compartmentId"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	FreeformTags  map[string]string `json:"freeformTags"`
}

// updateBody is the request body of UpdateUser, UpdateGroup and
// UpdateCompartment.
type updateBody struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	FreeformTags map[string]string `json:"freeformTags"`
}

// identityUpdate maps the request body onto the driver's update struct.
func (b *updateBody) identityUpdate() iamdriver.IdentityUpdate {
	return iamdriver.IdentityUpdate{
		Name:         b.Name,
		Description:  b.Description,
		FreeformTags: b.FreeformTags,
	}
}

// principalResource is OCI's User and Group response shape.
type principalResource struct {
	ID             string            `json:"id"`
	CompartmentID  string            `json:"compartmentId"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	TimeCreated    string            `json:"timeCreated"`
	LifecycleState string            `json:"lifecycleState"`
	FreeformTags   map[string]string `json:"freeformTags"`
	DefinedTags    definedTags       `json:"definedTags"`
}

// createMembershipBody is the request body of AddUserToGroup.
type createMembershipBody struct {
	UserID  string `json:"userId"`
	GroupID string `json:"groupId"`
}

// membershipResource is OCI's UserGroupMembership response shape.
type membershipResource struct {
	ID             string `json:"id"`
	CompartmentID  string `json:"compartmentId"`
	UserID         string `json:"userId"`
	GroupID        string `json:"groupId"`
	TimeCreated    string `json:"timeCreated"`
	LifecycleState string `json:"lifecycleState"`
}

// createPolicyBody is the request body of CreatePolicy.
type createPolicyBody struct {
	CompartmentID string            `json:"compartmentId"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Statements    []string          `json:"statements"`
	FreeformTags  map[string]string `json:"freeformTags"`
}

// updatePolicyBody is the request body of UpdatePolicy.
type updatePolicyBody struct {
	Description  string            `json:"description"`
	Statements   []string          `json:"statements"`
	FreeformTags map[string]string `json:"freeformTags"`
}

// policyUpdate maps the request body onto the driver's update struct.
func (b *updatePolicyBody) policyUpdate() iamdriver.PolicyUpdate {
	return iamdriver.PolicyUpdate{
		Description:  b.Description,
		Statements:   b.Statements,
		FreeformTags: b.FreeformTags,
	}
}

// policyResource is OCI's Policy response shape.
type policyResource struct {
	ID             string            `json:"id"`
	CompartmentID  string            `json:"compartmentId"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Statements     []string          `json:"statements"`
	TimeCreated    string            `json:"timeCreated"`
	VersionDate    string            `json:"versionDate"`
	LifecycleState string            `json:"lifecycleState"`
	FreeformTags   map[string]string `json:"freeformTags"`
	DefinedTags    definedTags       `json:"definedTags"`
}

// createCompartmentBody is the request body of CreateCompartment, where
// compartmentId names the parent.
type createCompartmentBody struct {
	CompartmentID string            `json:"compartmentId"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	FreeformTags  map[string]string `json:"freeformTags"`
}

// compartmentResource is OCI's Compartment response shape.
type compartmentResource struct {
	ID             string            `json:"id"`
	CompartmentID  string            `json:"compartmentId"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	TimeCreated    string            `json:"timeCreated"`
	LifecycleState string            `json:"lifecycleState"`
	IsAccessible   bool              `json:"isAccessible"`
	FreeformTags   map[string]string `json:"freeformTags"`
	DefinedTags    definedTags       `json:"definedTags"`
}

func toPrincipalResource(p *iamdriver.PrincipalInfo) principalResource {
	return principalResource{
		ID:             p.ID,
		CompartmentID:  p.CompartmentID,
		Name:           p.Name,
		Description:    p.Description,
		TimeCreated:    p.TimeCreated,
		LifecycleState: p.LifecycleState,
		FreeformTags:   nonNilTags(p.FreeformTags),
		DefinedTags:    definedTags{},
	}
}

func toMembershipResource(m *iamdriver.MembershipInfo) membershipResource {
	return membershipResource{
		ID:             m.ID,
		CompartmentID:  m.CompartmentID,
		UserID:         m.UserID,
		GroupID:        m.GroupID,
		TimeCreated:    m.TimeCreated,
		LifecycleState: m.LifecycleState,
	}
}

func toPolicyResource(p *iamdriver.StatementPolicyInfo) policyResource {
	return policyResource{
		ID:             p.ID,
		CompartmentID:  p.CompartmentID,
		Name:           p.Name,
		Description:    p.Description,
		Statements:     p.Statements,
		TimeCreated:    p.TimeCreated,
		VersionDate:    p.VersionDate,
		LifecycleState: p.LifecycleState,
		FreeformTags:   nonNilTags(p.FreeformTags),
		DefinedTags:    definedTags{},
	}
}

func toCompartmentResource(c *iamdriver.CompartmentInfo) compartmentResource {
	return compartmentResource{
		ID:             c.ID,
		CompartmentID:  c.ParentID,
		Name:           c.Name,
		Description:    c.Description,
		TimeCreated:    c.TimeCreated,
		LifecycleState: c.LifecycleState,
		IsAccessible:   true,
		FreeformTags:   nonNilTags(c.FreeformTags),
		DefinedTags:    definedTags{},
	}
}

// nonNilTags keeps the tag map an object rather than null on the wire.
func nonNilTags(tags map[string]string) map[string]string {
	if tags == nil {
		return map[string]string{}
	}

	return tags
}
