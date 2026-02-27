// Package awsiam provides an in-memory mock implementation of AWS IAM.
package awsiam

import (
	"context"
	"sync"

	"github.com/NitinKumar004/cloudemu/config"
	"github.com/NitinKumar004/cloudemu/errors"
	"github.com/NitinKumar004/cloudemu/iam/driver"
	"github.com/NitinKumar004/cloudemu/internal/idgen"
	"github.com/NitinKumar004/cloudemu/internal/memstore"
)

// Compile-time check that Mock implements driver.IAM.
var _ driver.IAM = (*Mock)(nil)

// Mock is an in-memory mock implementation of the AWS IAM service.
type Mock struct {
	users    *memstore.Store[*userData]
	roles    *memstore.Store[*roleData]
	policies *memstore.Store[*policyData]

	mu           sync.RWMutex
	userPolicies map[string]map[string]bool // userName -> set of policy ARNs
	rolePolicies map[string]map[string]bool // roleName -> set of policy ARNs

	opts *config.Options
}

type userData struct {
	Name      string
	ID        string
	ARN       string
	Path      string
	Tags      map[string]string
	CreatedAt string
}

type roleData struct {
	Name                string
	ID                  string
	ARN                 string
	Path                string
	AssumeRolePolicyDoc string
	Tags                map[string]string
}

type policyData struct {
	Name           string
	ID             string
	ARN            string
	Path           string
	PolicyDocument string
	Description    string
}

// New creates a new IAM mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		users:        memstore.New[*userData](),
		roles:        memstore.New[*roleData](),
		policies:     memstore.New[*policyData](),
		userPolicies: make(map[string]map[string]bool),
		rolePolicies: make(map[string]map[string]bool),
		opts:         opts,
	}
}

// CreateUser creates a new IAM user.
func (m *Mock) CreateUser(ctx context.Context, cfg driver.UserConfig) (*driver.UserInfo, error) {
	if cfg.Name == "" {
		return nil, errors.Newf(errors.InvalidArgument, "user name is required")
	}

	if m.users.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "user %q already exists", cfg.Name)
	}

	path := cfg.Path
	if path == "" {
		path = "/"
	}

	id := idgen.GenerateID("AIDA")
	arn := idgen.AWSARN("iam", "", m.opts.AccountID, "user/"+cfg.Name)

	tags := copyTags(cfg.Tags)

	u := &userData{
		Name:      cfg.Name,
		ID:        id,
		ARN:       arn,
		Path:      path,
		Tags:      tags,
		CreatedAt: m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	m.users.Set(cfg.Name, u)

	info := toUserInfo(u)
	return &info, nil
}

// DeleteUser deletes the IAM user with the given name.
func (m *Mock) DeleteUser(ctx context.Context, name string) error {
	if !m.users.Delete(name) {
		return errors.Newf(errors.NotFound, "user %q not found", name)
	}

	m.mu.Lock()
	delete(m.userPolicies, name)
	m.mu.Unlock()

	return nil
}

// GetUser returns the IAM user with the given name.
func (m *Mock) GetUser(ctx context.Context, name string) (*driver.UserInfo, error) {
	u, ok := m.users.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "user %q not found", name)
	}

	info := toUserInfo(u)
	return &info, nil
}

// ListUsers returns all IAM users.
func (m *Mock) ListUsers(ctx context.Context) ([]driver.UserInfo, error) {
	all := m.users.All()
	result := make([]driver.UserInfo, 0, len(all))
	for _, u := range all {
		result = append(result, toUserInfo(u))
	}
	return result, nil
}

// CreateRole creates a new IAM role.
func (m *Mock) CreateRole(ctx context.Context, cfg driver.RoleConfig) (*driver.RoleInfo, error) {
	if cfg.Name == "" {
		return nil, errors.Newf(errors.InvalidArgument, "role name is required")
	}

	if m.roles.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "role %q already exists", cfg.Name)
	}

	path := cfg.Path
	if path == "" {
		path = "/"
	}

	id := idgen.GenerateID("AROA")
	arn := idgen.AWSARN("iam", "", m.opts.AccountID, "role/"+cfg.Name)

	tags := copyTags(cfg.Tags)

	r := &roleData{
		Name:                cfg.Name,
		ID:                  id,
		ARN:                 arn,
		Path:                path,
		AssumeRolePolicyDoc: cfg.AssumeRolePolicyDoc,
		Tags:                tags,
	}
	m.roles.Set(cfg.Name, r)

	info := toRoleInfo(r)
	return &info, nil
}

// DeleteRole deletes the IAM role with the given name.
func (m *Mock) DeleteRole(ctx context.Context, name string) error {
	if !m.roles.Delete(name) {
		return errors.Newf(errors.NotFound, "role %q not found", name)
	}

	m.mu.Lock()
	delete(m.rolePolicies, name)
	m.mu.Unlock()

	return nil
}

// GetRole returns the IAM role with the given name.
func (m *Mock) GetRole(ctx context.Context, name string) (*driver.RoleInfo, error) {
	r, ok := m.roles.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "role %q not found", name)
	}

	info := toRoleInfo(r)
	return &info, nil
}

// ListRoles returns all IAM roles.
func (m *Mock) ListRoles(ctx context.Context) ([]driver.RoleInfo, error) {
	all := m.roles.All()
	result := make([]driver.RoleInfo, 0, len(all))
	for _, r := range all {
		result = append(result, toRoleInfo(r))
	}
	return result, nil
}

// CreatePolicy creates a new IAM policy.
func (m *Mock) CreatePolicy(ctx context.Context, cfg driver.PolicyConfig) (*driver.PolicyInfo, error) {
	if cfg.Name == "" {
		return nil, errors.Newf(errors.InvalidArgument, "policy name is required")
	}

	path := cfg.Path
	if path == "" {
		path = "/"
	}

	id := idgen.GenerateID("ANPA")
	arn := idgen.AWSARN("iam", "", m.opts.AccountID, "policy/"+cfg.Name)

	if m.policies.Has(arn) {
		return nil, errors.Newf(errors.AlreadyExists, "policy %q already exists", cfg.Name)
	}

	p := &policyData{
		Name:           cfg.Name,
		ID:             id,
		ARN:            arn,
		Path:           path,
		PolicyDocument: cfg.PolicyDocument,
		Description:    cfg.Description,
	}
	m.policies.Set(arn, p)

	info := toPolicyInfo(p)
	return &info, nil
}

// DeletePolicy deletes the IAM policy with the given ARN.
func (m *Mock) DeletePolicy(ctx context.Context, arn string) error {
	if !m.policies.Delete(arn) {
		return errors.Newf(errors.NotFound, "policy %q not found", arn)
	}
	return nil
}

// GetPolicy returns the IAM policy with the given ARN.
func (m *Mock) GetPolicy(ctx context.Context, arn string) (*driver.PolicyInfo, error) {
	p, ok := m.policies.Get(arn)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "policy %q not found", arn)
	}

	info := toPolicyInfo(p)
	return &info, nil
}

// ListPolicies returns all IAM policies.
func (m *Mock) ListPolicies(ctx context.Context) ([]driver.PolicyInfo, error) {
	all := m.policies.All()
	result := make([]driver.PolicyInfo, 0, len(all))
	for _, p := range all {
		result = append(result, toPolicyInfo(p))
	}
	return result, nil
}

// AttachUserPolicy attaches a policy to a user.
func (m *Mock) AttachUserPolicy(ctx context.Context, userName, policyARN string) error {
	if !m.users.Has(userName) {
		return errors.Newf(errors.NotFound, "user %q not found", userName)
	}
	if !m.policies.Has(policyARN) {
		return errors.Newf(errors.NotFound, "policy %q not found", policyARN)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.userPolicies[userName] == nil {
		m.userPolicies[userName] = make(map[string]bool)
	}
	m.userPolicies[userName][policyARN] = true

	return nil
}

// DetachUserPolicy detaches a policy from a user.
func (m *Mock) DetachUserPolicy(ctx context.Context, userName, policyARN string) error {
	if !m.users.Has(userName) {
		return errors.Newf(errors.NotFound, "user %q not found", userName)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	policies, ok := m.userPolicies[userName]
	if !ok || !policies[policyARN] {
		return errors.Newf(errors.NotFound, "policy %q is not attached to user %q", policyARN, userName)
	}

	delete(policies, policyARN)
	return nil
}

// AttachRolePolicy attaches a policy to a role.
func (m *Mock) AttachRolePolicy(ctx context.Context, roleName, policyARN string) error {
	if !m.roles.Has(roleName) {
		return errors.Newf(errors.NotFound, "role %q not found", roleName)
	}
	if !m.policies.Has(policyARN) {
		return errors.Newf(errors.NotFound, "policy %q not found", policyARN)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.rolePolicies[roleName] == nil {
		m.rolePolicies[roleName] = make(map[string]bool)
	}
	m.rolePolicies[roleName][policyARN] = true

	return nil
}

// DetachRolePolicy detaches a policy from a role.
func (m *Mock) DetachRolePolicy(ctx context.Context, roleName, policyARN string) error {
	if !m.roles.Has(roleName) {
		return errors.Newf(errors.NotFound, "role %q not found", roleName)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	policies, ok := m.rolePolicies[roleName]
	if !ok || !policies[policyARN] {
		return errors.Newf(errors.NotFound, "policy %q is not attached to role %q", policyARN, roleName)
	}

	delete(policies, policyARN)
	return nil
}

// ListAttachedUserPolicies returns the ARNs of policies attached to the given user.
func (m *Mock) ListAttachedUserPolicies(ctx context.Context, userName string) ([]string, error) {
	if !m.users.Has(userName) {
		return nil, errors.Newf(errors.NotFound, "user %q not found", userName)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	policies := m.userPolicies[userName]
	result := make([]string, 0, len(policies))
	for arn := range policies {
		result = append(result, arn)
	}
	return result, nil
}

// ListAttachedRolePolicies returns the ARNs of policies attached to the given role.
func (m *Mock) ListAttachedRolePolicies(ctx context.Context, roleName string) ([]string, error) {
	if !m.roles.Has(roleName) {
		return nil, errors.Newf(errors.NotFound, "role %q not found", roleName)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	policies := m.rolePolicies[roleName]
	result := make([]string, 0, len(policies))
	for arn := range policies {
		result = append(result, arn)
	}
	return result, nil
}

// CheckPermission checks if a principal has any attached policy (simplified check).
// Returns true if the principal (user or role name) has at least one attached policy.
func (m *Mock) CheckPermission(ctx context.Context, principal, action, resource string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Check user policies.
	if policies, ok := m.userPolicies[principal]; ok && len(policies) > 0 {
		return true, nil
	}

	// Check role policies.
	if policies, ok := m.rolePolicies[principal]; ok && len(policies) > 0 {
		return true, nil
	}

	return false, nil
}

// copyTags creates a shallow copy of a tags map.
func copyTags(tags map[string]string) map[string]string {
	if tags == nil {
		return make(map[string]string)
	}
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		out[k] = v
	}
	return out
}

func toUserInfo(u *userData) driver.UserInfo {
	return driver.UserInfo{
		Name:      u.Name,
		ID:        u.ID,
		ARN:       u.ARN,
		Path:      u.Path,
		Tags:      copyTags(u.Tags),
		CreatedAt: u.CreatedAt,
	}
}

func toRoleInfo(r *roleData) driver.RoleInfo {
	return driver.RoleInfo{
		Name:                r.Name,
		ID:                  r.ID,
		ARN:                 r.ARN,
		Path:                r.Path,
		AssumeRolePolicyDoc: r.AssumeRolePolicyDoc,
		Tags:                copyTags(r.Tags),
	}
}

func toPolicyInfo(p *policyData) driver.PolicyInfo {
	return driver.PolicyInfo{
		Name:           p.Name,
		ID:             p.ID,
		ARN:            p.ARN,
		Path:           p.Path,
		PolicyDocument: p.PolicyDocument,
		Description:    p.Description,
	}
}
