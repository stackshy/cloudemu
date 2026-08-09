package identity

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// statementSeparator splits the portable policy document, which carries OCI
// statements one per line rather than a JSON document.
const statementSeparator = "\n"

// CreateUser creates a user in the configured compartment.
func (m *Mock) CreateUser(_ context.Context, cfg driver.UserConfig) (*driver.UserInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	info, err := m.createPrincipal(m.users, kindUser, driver.PrincipalSpec{
		Name:         cfg.Name,
		FreeformTags: cfg.Tags,
	})
	if err != nil {
		return nil, err
	}

	return toUserInfo(info), nil
}

// DeleteUser deletes the user with the given name.
func (m *Mock) DeleteUser(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := findByName(m.users, name)
	if !ok {
		return namedNotFound(kindUser, name)
	}

	return m.deleteUser(u.ID)
}

// GetUser returns the user with the given name.
func (m *Mock) GetUser(_ context.Context, name string) (*driver.UserInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	u, ok := findByName(m.users, name)
	if !ok {
		return nil, namedNotFound(kindUser, name)
	}

	return toUserInfo(toPrincipalInfo(u)), nil
}

// ListUsers returns every user, across compartments.
func (m *Mock) ListUsers(_ context.Context) ([]driver.UserInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.users.SortedValues()
	out := make([]driver.UserInfo, 0, len(all))

	for _, u := range all {
		out = append(out, *toUserInfo(toPrincipalInfo(u)))
	}

	return out, nil
}

// CreateGroup creates a group in the configured compartment.
func (m *Mock) CreateGroup(_ context.Context, cfg driver.GroupConfig) (*driver.GroupInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	info, err := m.createPrincipal(m.groups, kindGroup, driver.PrincipalSpec{Name: cfg.Name})
	if err != nil {
		return nil, err
	}

	return toGroupInfo(info), nil
}

// DeleteGroup deletes the group with the given name.
func (m *Mock) DeleteGroup(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, ok := findByName(m.groups, name)
	if !ok {
		return namedNotFound(kindGroup, name)
	}

	return m.deleteGroup(g.ID)
}

// GetGroup returns the group with the given name.
func (m *Mock) GetGroup(_ context.Context, name string) (*driver.GroupInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	g, ok := findByName(m.groups, name)
	if !ok {
		return nil, namedNotFound(kindGroup, name)
	}

	return toGroupInfo(toPrincipalInfo(g)), nil
}

// ListGroups returns every group, across compartments.
func (m *Mock) ListGroups(_ context.Context) ([]driver.GroupInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.groups.SortedValues()
	out := make([]driver.GroupInfo, 0, len(all))

	for _, g := range all {
		out = append(out, *toGroupInfo(toPrincipalInfo(g)))
	}

	return out, nil
}

// AddUserToGroup adds a user to a group by name.
func (m *Mock) AddUserToGroup(_ context.Context, userName, groupName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, g, err := m.principalsNamed(userName, groupName)
	if err != nil {
		return err
	}

	_, err = m.createMembership(u.ID, g.ID)

	return err
}

// RemoveUserFromGroup removes a user from a group by name.
func (m *Mock) RemoveUserFromGroup(_ context.Context, userName, groupName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, g, err := m.principalsNamed(userName, groupName)
	if err != nil {
		return err
	}

	mem, ok := m.findMembership(u.ID, g.ID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "user %q is not a member of group %q", userName, groupName)
	}

	return m.deleteMembership(mem.ID)
}

// ListGroupsForUser returns the groups a user belongs to.
func (m *Mock) ListGroupsForUser(_ context.Context, userName string) ([]driver.GroupInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	u, ok := findByName(m.users, userName)
	if !ok {
		return nil, namedNotFound(kindUser, userName)
	}

	var out []driver.GroupInfo

	for _, mem := range m.memberships.SortedValues() {
		if mem.UserID != u.ID {
			continue
		}

		if g, found := m.groups.Get(mem.GroupID); found {
			out = append(out, *toGroupInfo(toPrincipalInfo(g)))
		}
	}

	return out, nil
}

// principalsNamed resolves a user and a group by name.
func (m *Mock) principalsNamed(userName, groupName string) (u, g *principal, err error) {
	u, ok := findByName(m.users, userName)
	if !ok {
		return nil, nil, namedNotFound(kindUser, userName)
	}

	g, ok = findByName(m.groups, groupName)
	if !ok {
		return nil, nil, namedNotFound(kindGroup, groupName)
	}

	return u, g, nil
}

// CreateRole creates a dynamic group, the OCI resource that grants an identity
// to something other than a user. The assume-role document is its matching rule.
func (m *Mock) CreateRole(_ context.Context, cfg driver.RoleConfig) (*driver.RoleInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validateName("dynamic group", cfg.Name); err != nil {
		return nil, err
	}

	if _, found := m.dynamicGroupNamed(cfg.Name); found {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "dynamic group %q already exists", cfg.Name)
	}

	dg := &dynamicGroup{
		ID:           idgen.GlobalOCID(kindDynamicGroup, m.opts.Realm),
		Name:         cfg.Name,
		MatchingRule: cfg.AssumeRolePolicyDoc,
		TimeCreated:  m.now(),
		Scope:        scope.Scope{Compartment: m.opts.CompartmentID},
		FreeformTags: copyTags(cfg.Tags),
	}
	m.dynamicGroups.Set(dg.ID, dg)

	return toRoleInfo(dg), nil
}

// DeleteRole deletes the dynamic group with the given name.
func (m *Mock) DeleteRole(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	dg, ok := m.dynamicGroupNamed(name)
	if !ok {
		return namedNotFound(kindDynamicGroup, name)
	}

	m.dynamicGroups.Delete(dg.ID)

	return nil
}

// GetRole returns the dynamic group with the given name.
func (m *Mock) GetRole(_ context.Context, name string) (*driver.RoleInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	dg, ok := m.dynamicGroupNamed(name)
	if !ok {
		return nil, namedNotFound(kindDynamicGroup, name)
	}

	return toRoleInfo(dg), nil
}

// ListRoles returns every dynamic group.
func (m *Mock) ListRoles(_ context.Context) ([]driver.RoleInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.dynamicGroups.SortedValues()
	out := make([]driver.RoleInfo, 0, len(all))

	for _, dg := range all {
		out = append(out, *toRoleInfo(dg))
	}

	return out, nil
}

// dynamicGroupNamed returns the dynamic group with the given name, which
// policy statements match without regard to case.
func (m *Mock) dynamicGroupNamed(name string) (*dynamicGroup, bool) {
	for _, dg := range m.dynamicGroups.SortedValues() {
		if strings.EqualFold(dg.Name, name) {
			return dg, true
		}
	}

	return nil, false
}

// CreatePolicy creates a policy whose document is its statements, one per line.
func (m *Mock) CreatePolicy(_ context.Context, cfg driver.PolicyConfig) (*driver.PolicyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	info, err := m.createPolicy(&driver.PolicySpec{
		Name:        cfg.Name,
		Description: cfg.Description,
		Statements:  splitStatements(cfg.PolicyDocument),
	})
	if err != nil {
		return nil, err
	}

	return toPolicyInfo(info), nil
}

// DeletePolicy deletes the policy with the given OCID.
func (m *Mock) DeletePolicy(ctx context.Context, arn string) error {
	return m.DeleteStatementPolicy(ctx, arn)
}

// GetPolicy returns the policy with the given OCID.
func (m *Mock) GetPolicy(_ context.Context, arn string) (*driver.PolicyInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	info, err := m.statementPolicy(arn)
	if err != nil {
		return nil, err
	}

	return toPolicyInfo(info), nil
}

// ListPolicies returns every policy, across compartments.
func (m *Mock) ListPolicies(_ context.Context) ([]driver.PolicyInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.policies.SortedValues()
	out := make([]driver.PolicyInfo, 0, len(all))

	for _, p := range all {
		out = append(out, *toPolicyInfo(toStatementPolicyInfo(p)))
	}

	return out, nil
}

// CreateAccessKey issues an auth token for a user.
func (m *Mock) CreateAccessKey(_ context.Context, cfg driver.AccessKeyConfig) (*driver.AccessKeyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := findByName(m.users, cfg.UserName); !ok {
		return nil, namedNotFound(kindUser, cfg.UserName)
	}

	tok := &authToken{
		ID:          idgen.GlobalOCID(kindCredential, m.opts.Realm),
		UserName:    cfg.UserName,
		Token:       idgen.GenerateID("authtoken-"),
		TimeCreated: m.now(),
	}
	m.authTokens.Set(tok.ID, tok)

	return toAccessKeyInfo(tok), nil
}

// DeleteAccessKey revokes a user's auth token.
func (m *Mock) DeleteAccessKey(_ context.Context, userName, accessKeyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tok, ok := m.authTokens.Get(accessKeyID)
	if !ok || tok.UserName != userName {
		return cerrors.Newf(cerrors.NotFound, "auth token %q not found for user %q", accessKeyID, userName)
	}

	m.authTokens.Delete(accessKeyID)

	return nil
}

// ListAccessKeys returns a user's auth tokens.
func (m *Mock) ListAccessKeys(_ context.Context, userName string) ([]driver.AccessKeyInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := findByName(m.users, userName); !ok {
		return nil, namedNotFound(kindUser, userName)
	}

	var out []driver.AccessKeyInfo

	for _, tok := range m.authTokens.SortedValues() {
		if tok.UserName == userName {
			out = append(out, *toAccessKeyInfo(tok))
		}
	}

	return out, nil
}

// CheckPermission evaluates the policy statements covering a user. The portable
// signature carries no compartment, so the check runs against the user's own.
func (m *Mock) CheckPermission(_ context.Context, principal, action, resource string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	u, ok := findByName(m.users, principal)
	if !ok {
		return false, nil
	}

	return m.evaluate(&driver.AccessRequest{
		Groups:        m.groupNamesFor(u.ID),
		AnyUser:       true,
		Verb:          action,
		ResourceType:  resourceTypeOf(resource),
		CompartmentID: u.Scope.Compartment,
	})
}

// resourceTypeOf reduces a resource reference to the type token a statement
// names, so both "buckets" and "…/buckets" evaluate the same.
func resourceTypeOf(resource string) string {
	if idx := strings.LastIndex(resource, defaultPath); idx >= 0 {
		return resource[idx+1:]
	}

	return resource
}

// splitStatements splits a portable policy document into OCI statements.
func splitStatements(document string) []string {
	var out []string

	for _, line := range strings.Split(document, statementSeparator) {
		if text := strings.TrimSpace(line); text != "" {
			out = append(out, text)
		}
	}

	return out
}

func namedNotFound(kind, name string) error {
	return cerrors.Newf(cerrors.NotFound, "%s %q not found", kind, name)
}

func toUserInfo(p *driver.PrincipalInfo) *driver.UserInfo {
	return &driver.UserInfo{
		Name:      p.Name,
		ID:        p.ID,
		ARN:       p.ID,
		Path:      defaultPath,
		Tags:      p.FreeformTags,
		CreatedAt: p.TimeCreated,
	}
}

func toGroupInfo(p *driver.PrincipalInfo) *driver.GroupInfo {
	return &driver.GroupInfo{
		Name:      p.Name,
		Path:      defaultPath,
		ARN:       p.ID,
		CreatedAt: p.TimeCreated,
	}
}

func toRoleInfo(dg *dynamicGroup) *driver.RoleInfo {
	return &driver.RoleInfo{
		Name:                dg.Name,
		ID:                  dg.ID,
		ARN:                 dg.ID,
		Path:                defaultPath,
		AssumeRolePolicyDoc: dg.MatchingRule,
		Tags:                copyTags(dg.FreeformTags),
	}
}

func toPolicyInfo(p *driver.StatementPolicyInfo) *driver.PolicyInfo {
	return &driver.PolicyInfo{
		Name:           p.Name,
		ID:             p.ID,
		ARN:            p.ID,
		Path:           defaultPath,
		PolicyDocument: strings.Join(p.Statements, statementSeparator),
		Description:    p.Description,
	}
}

func toAccessKeyInfo(tok *authToken) *driver.AccessKeyInfo {
	return &driver.AccessKeyInfo{
		AccessKeyID:     tok.ID,
		SecretAccessKey: tok.Token,
		UserName:        tok.UserName,
		Status:          lifecycleActive,
		CreatedAt:       tok.TimeCreated,
	}
}
