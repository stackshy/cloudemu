// Package gcpiam provides an in-memory mock implementation of GCP IAM.
package gcpiam

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/iam/driver"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
)

const timeFormat = "2006-01-02T15:04:05Z"

// Compile-time check that Mock implements driver.IAM.
var _ driver.IAM = (*Mock)(nil)

// Mock is an in-memory mock implementation of the GCP IAM service.
type Mock struct {
	users            *memstore.Store[*userData]
	roles            *memstore.Store[*roleData]
	policies         *memstore.Store[*policyData]
	groups           *memstore.Store[*groupData]
	accessKeys       *memstore.Store[*accessKeyData]
	instanceProfiles *memstore.Store[*driver.InstanceProfileInfo]

	mu           sync.RWMutex
	userPolicies map[string]map[string]bool
	rolePolicies map[string]map[string]bool
	groupUsers   map[string]map[string]bool

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

type groupData struct {
	Name      string
	ARN       string
	Path      string
	CreatedAt string
}

type accessKeyData struct {
	AccessKeyID     string
	SecretAccessKey string
	UserName        string
	Status          string
	CreatedAt       string
}

// New creates a new GCP IAM mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		users:            memstore.New[*userData](),
		roles:            memstore.New[*roleData](),
		policies:         memstore.New[*policyData](),
		groups:           memstore.New[*groupData](),
		accessKeys:       memstore.New[*accessKeyData](),
		instanceProfiles: memstore.New[*driver.InstanceProfileInfo](),
		userPolicies:     make(map[string]map[string]bool),
		rolePolicies:     make(map[string]map[string]bool),
		groupUsers:       make(map[string]map[string]bool),
		opts:             opts,
	}
}

// CreateUser creates a new IAM service account (user).
func (m *Mock) CreateUser(_ context.Context, cfg driver.UserConfig) (*driver.UserInfo, error) {
	if cfg.Name == "" {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "service account name is required")
	}

	if m.users.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "service account %q already exists", cfg.Name)
	}

	path := cfg.Path
	if path == "" {
		path = "/"
	}

	id := idgen.GenerateID("sa-")
	arn := fmt.Sprintf("projects/%s/serviceAccounts/%s@%s.iam.gserviceaccount.com",
		m.opts.ProjectID, cfg.Name, m.opts.ProjectID)

	tags := copyTags(cfg.Tags)

	u := &userData{
		Name:      cfg.Name,
		ID:        id,
		ARN:       arn,
		Path:      path,
		Tags:      tags,
		CreatedAt: m.opts.Clock.Now().UTC().Format(timeFormat),
	}
	m.users.Set(cfg.Name, u)

	info := toUserInfo(u)

	return &info, nil
}

// DeleteUser deletes the IAM service account with the given name.
func (m *Mock) DeleteUser(_ context.Context, name string) error {
	if !m.users.Delete(name) {
		return cerrors.Newf(cerrors.NotFound, "service account %q not found", name)
	}

	m.mu.Lock()
	delete(m.userPolicies, name)
	m.mu.Unlock()

	return nil
}

// GetUser returns the IAM service account with the given name.
func (m *Mock) GetUser(_ context.Context, name string) (*driver.UserInfo, error) {
	u, ok := m.users.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "service account %q not found", name)
	}

	info := toUserInfo(u)

	return &info, nil
}

// ListUsers returns all IAM service accounts.
func (m *Mock) ListUsers(_ context.Context) ([]driver.UserInfo, error) {
	all := m.users.All()
	result := make([]driver.UserInfo, 0, len(all))

	for _, u := range all {
		result = append(result, toUserInfo(u))
	}

	return result, nil
}

// CreateRole creates a new IAM custom role.
func (m *Mock) CreateRole(_ context.Context, cfg driver.RoleConfig) (*driver.RoleInfo, error) {
	if cfg.Name == "" {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "role name is required")
	}

	if m.roles.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "role %q already exists", cfg.Name)
	}

	path := cfg.Path
	if path == "" {
		path = "/"
	}

	id := idgen.GenerateID("role-")
	arn := idgen.GCPID(m.opts.ProjectID, "roles", cfg.Name)

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

// DeleteRole deletes the IAM custom role with the given name.
func (m *Mock) DeleteRole(_ context.Context, name string) error {
	if !m.roles.Delete(name) {
		return cerrors.Newf(cerrors.NotFound, "role %q not found", name)
	}

	m.mu.Lock()
	delete(m.rolePolicies, name)
	m.mu.Unlock()

	return nil
}

// GetRole returns the IAM custom role with the given name.
func (m *Mock) GetRole(_ context.Context, name string) (*driver.RoleInfo, error) {
	r, ok := m.roles.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "role %q not found", name)
	}

	info := toRoleInfo(r)

	return &info, nil
}

// ListRoles returns all IAM custom roles.
func (m *Mock) ListRoles(_ context.Context) ([]driver.RoleInfo, error) {
	all := m.roles.All()
	result := make([]driver.RoleInfo, 0, len(all))

	for _, r := range all {
		result = append(result, toRoleInfo(r))
	}

	return result, nil
}

// CreatePolicy creates a new IAM policy binding.
func (m *Mock) CreatePolicy(_ context.Context, cfg driver.PolicyConfig) (*driver.PolicyInfo, error) {
	if cfg.Name == "" {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "policy name is required")
	}

	path := cfg.Path
	if path == "" {
		path = "/"
	}

	id := idgen.GenerateID("pol-")
	arn := idgen.GCPID(m.opts.ProjectID, "policies", cfg.Name)

	if m.policies.Has(arn) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "policy %q already exists", cfg.Name)
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

// DeletePolicy deletes the IAM policy with the given resource name (ARN).
func (m *Mock) DeletePolicy(_ context.Context, arn string) error {
	if !m.policies.Delete(arn) {
		return cerrors.Newf(cerrors.NotFound, "policy %q not found", arn)
	}

	return nil
}

// GetPolicy returns the IAM policy with the given resource name (ARN).
func (m *Mock) GetPolicy(_ context.Context, arn string) (*driver.PolicyInfo, error) {
	p, ok := m.policies.Get(arn)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "policy %q not found", arn)
	}

	info := toPolicyInfo(p)

	return &info, nil
}

// ListPolicies returns all IAM policies.
func (m *Mock) ListPolicies(_ context.Context) ([]driver.PolicyInfo, error) {
	all := m.policies.All()
	result := make([]driver.PolicyInfo, 0, len(all))

	for _, p := range all {
		result = append(result, toPolicyInfo(p))
	}

	return result, nil
}

func (m *Mock) attachPolicy(
	principalStore interface{ Has(string) bool },
	principalName, policyARN string,
	policyMap map[string]map[string]bool,
	entityType string,
) error {
	if !principalStore.Has(principalName) {
		return cerrors.Newf(cerrors.NotFound, "%s %q not found", entityType, principalName)
	}

	if !m.policies.Has(policyARN) {
		return cerrors.Newf(cerrors.NotFound, "policy %q not found", policyARN)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if policyMap[principalName] == nil {
		policyMap[principalName] = make(map[string]bool)
	}

	policyMap[principalName][policyARN] = true

	return nil
}

// AttachUserPolicy binds a policy to a service account (user).
func (m *Mock) AttachUserPolicy(_ context.Context, userName, policyARN string) error {
	return m.attachPolicy(m.users, userName, policyARN, m.userPolicies, "service account")
}

// DetachUserPolicy removes a policy binding from a service account (user).
func (m *Mock) DetachUserPolicy(_ context.Context, userName, policyARN string) error {
	if !m.users.Has(userName) {
		return cerrors.Newf(cerrors.NotFound, "service account %q not found", userName)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	policies, ok := m.userPolicies[userName]
	if !ok || !policies[policyARN] {
		return cerrors.Newf(cerrors.NotFound, "policy %q is not attached to service account %q", policyARN, userName)
	}

	delete(policies, policyARN)

	return nil
}

// AttachRolePolicy binds a policy to a custom role.
func (m *Mock) AttachRolePolicy(_ context.Context, roleName, policyARN string) error {
	return m.attachPolicy(m.roles, roleName, policyARN, m.rolePolicies, "role")
}

// DetachRolePolicy removes a policy binding from a custom role.
func (m *Mock) DetachRolePolicy(_ context.Context, roleName, policyARN string) error {
	if !m.roles.Has(roleName) {
		return cerrors.Newf(cerrors.NotFound, "role %q not found", roleName)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	policies, ok := m.rolePolicies[roleName]
	if !ok || !policies[policyARN] {
		return cerrors.Newf(cerrors.NotFound, "policy %q is not attached to role %q", policyARN, roleName)
	}

	delete(policies, policyARN)

	return nil
}

// ListAttachedUserPolicies returns the resource names of policies attached to the given service account.
func (m *Mock) ListAttachedUserPolicies(_ context.Context, userName string) ([]string, error) {
	if !m.users.Has(userName) {
		return nil, cerrors.Newf(cerrors.NotFound, "service account %q not found", userName)
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

// ListAttachedRolePolicies returns the resource names of policies attached to the given role.
func (m *Mock) ListAttachedRolePolicies(_ context.Context, roleName string) ([]string, error) {
	if !m.roles.Has(roleName) {
		return nil, cerrors.Newf(cerrors.NotFound, "role %q not found", roleName)
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

type policyDoc struct {
	Version   string            `json:"Version"`
	Statement []policyStatement `json:"Statement"`
}

type policyStatement struct {
	Effect   string `json:"Effect"`
	Action   any    `json:"Action"`
	Resource any    `json:"Resource"`
}

func wildcardMatch(pattern, value string) bool {
	if pattern == "*" {
		return true
	}

	pParts := strings.Split(pattern, "*")

	if len(pParts) == 1 {
		return pattern == value
	}

	if !strings.HasPrefix(value, pParts[0]) {
		return false
	}

	remaining := value[len(pParts[0]):]

	for i := 1; i < len(pParts); i++ {
		idx := strings.Index(remaining, pParts[i])
		if idx < 0 {
			return false
		}

		remaining = remaining[idx+len(pParts[i]):]
	}

	return true
}

func toStringSlice(v any) []string {
	switch val := v.(type) {
	case string:
		return []string{val}
	case []any:
		out := make([]string, 0, len(val))

		for _, item := range val {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}

		return out
	}

	return nil
}

func matchesAction(actions []string, action string) bool {
	for _, a := range actions {
		if wildcardMatch(a, action) {
			return true
		}
	}

	return false
}

func matchesResource(resources []string, resource string) bool {
	for _, r := range resources {
		if wildcardMatch(r, resource) {
			return true
		}
	}

	return false
}

func evaluatePolicy(doc, action, resource string) (allow, deny bool) {
	var pd policyDoc
	if err := json.Unmarshal([]byte(doc), &pd); err != nil {
		return false, false
	}

	for _, stmt := range pd.Statement {
		actions := toStringSlice(stmt.Action)
		resources := toStringSlice(stmt.Resource)

		if !matchesAction(actions, action) {
			continue
		}

		if !matchesResource(resources, resource) {
			continue
		}

		if strings.EqualFold(stmt.Effect, "Deny") {
			deny = true
		} else if strings.EqualFold(stmt.Effect, "Allow") {
			allow = true
		}
	}

	return allow, deny
}

func (m *Mock) collectPolicyARNs(principal string) map[string]bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	policyARNs := make(map[string]bool)

	for arn := range m.userPolicies[principal] {
		policyARNs[arn] = true
	}

	for arn := range m.rolePolicies[principal] {
		policyARNs[arn] = true
	}

	return policyARNs
}

// CheckPermission evaluates attached policies to determine if a principal is allowed
// to perform the given action on the given resource. Explicit Deny wins over Allow.
func (m *Mock) CheckPermission(_ context.Context, principal, action, resource string) (bool, error) {
	policyARNs := m.collectPolicyARNs(principal)

	hasAllow := false

	for arn := range policyARNs {
		p, ok := m.policies.Get(arn)
		if !ok || p.PolicyDocument == "" {
			continue
		}

		allow, deny := evaluatePolicy(p.PolicyDocument, action, resource)

		if deny {
			return false, nil
		}

		if allow {
			hasAllow = true
		}
	}

	return hasAllow, nil
}

// CreateGroup creates a new GCP IAM group.
func (m *Mock) CreateGroup(
	_ context.Context, cfg driver.GroupConfig,
) (*driver.GroupInfo, error) {
	if cfg.Name == "" {
		return nil, cerrors.Newf(
			cerrors.InvalidArgument, "group name is required",
		)
	}

	if m.groups.Has(cfg.Name) {
		return nil, cerrors.Newf(
			cerrors.AlreadyExists,
			"group %q already exists", cfg.Name,
		)
	}

	path := cfg.Path
	if path == "" {
		path = "/"
	}

	arn := idgen.GCPID(m.opts.ProjectID, "groups", cfg.Name)

	g := &groupData{
		Name:      cfg.Name,
		ARN:       arn,
		Path:      path,
		CreatedAt: m.opts.Clock.Now().UTC().Format(timeFormat),
	}
	m.groups.Set(cfg.Name, g)

	info := toGroupInfo(g)

	return &info, nil
}

// DeleteGroup deletes the GCP IAM group with the given name.
func (m *Mock) DeleteGroup(_ context.Context, name string) error {
	if !m.groups.Delete(name) {
		return cerrors.Newf(
			cerrors.NotFound, "group %q not found", name,
		)
	}

	m.mu.Lock()
	delete(m.groupUsers, name)
	m.mu.Unlock()

	return nil
}

// GetGroup returns the GCP IAM group with the given name.
func (m *Mock) GetGroup(
	_ context.Context, name string,
) (*driver.GroupInfo, error) {
	g, ok := m.groups.Get(name)
	if !ok {
		return nil, cerrors.Newf(
			cerrors.NotFound, "group %q not found", name,
		)
	}

	info := toGroupInfo(g)

	return &info, nil
}

// ListGroups returns all GCP IAM groups.
func (m *Mock) ListGroups(
	_ context.Context,
) ([]driver.GroupInfo, error) {
	all := m.groups.All()
	result := make([]driver.GroupInfo, 0, len(all))

	for _, g := range all {
		result = append(result, toGroupInfo(g))
	}

	return result, nil
}

// AddUserToGroup adds a user to a group.
func (m *Mock) AddUserToGroup(
	_ context.Context, userName, groupName string,
) error {
	if !m.users.Has(userName) {
		return cerrors.Newf(
			cerrors.NotFound,
			"service account %q not found", userName,
		)
	}

	if !m.groups.Has(groupName) {
		return cerrors.Newf(
			cerrors.NotFound, "group %q not found", groupName,
		)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.groupUsers[groupName] == nil {
		m.groupUsers[groupName] = make(map[string]bool)
	}

	m.groupUsers[groupName][userName] = true

	return nil
}

// RemoveUserFromGroup removes a user from a group.
func (m *Mock) RemoveUserFromGroup(
	_ context.Context, userName, groupName string,
) error {
	if !m.groups.Has(groupName) {
		return cerrors.Newf(
			cerrors.NotFound, "group %q not found", groupName,
		)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	members, ok := m.groupUsers[groupName]
	if !ok || !members[userName] {
		return cerrors.Newf(
			cerrors.NotFound,
			"user %q is not a member of group %q",
			userName, groupName,
		)
	}

	delete(members, userName)

	return nil
}

// ListGroupsForUser returns all groups a user belongs to.
func (m *Mock) ListGroupsForUser(
	_ context.Context, userName string,
) ([]driver.GroupInfo, error) {
	if !m.users.Has(userName) {
		return nil, cerrors.Newf(
			cerrors.NotFound,
			"service account %q not found", userName,
		)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []driver.GroupInfo

	for groupName, members := range m.groupUsers {
		if !members[userName] {
			continue
		}

		g, ok := m.groups.Get(groupName)
		if !ok {
			continue
		}

		result = append(result, toGroupInfo(g))
	}

	return result, nil
}

// CreateAccessKey creates a new service account key.
func (m *Mock) CreateAccessKey(
	_ context.Context, cfg driver.AccessKeyConfig,
) (*driver.AccessKeyInfo, error) {
	if cfg.UserName == "" {
		return nil, cerrors.Newf(
			cerrors.InvalidArgument,
			"service account name is required",
		)
	}

	if !m.users.Has(cfg.UserName) {
		return nil, cerrors.Newf(
			cerrors.NotFound,
			"service account %q not found", cfg.UserName,
		)
	}

	keyID := fmt.Sprintf("gcp-key-%s", idgen.GenerateID(""))
	secret := fmt.Sprintf("secret-%s", idgen.GenerateID(""))

	ak := &accessKeyData{
		AccessKeyID:     keyID,
		SecretAccessKey: secret,
		UserName:        cfg.UserName,
		Status:          "Active",
		CreatedAt:       m.opts.Clock.Now().UTC().Format(timeFormat),
	}
	m.accessKeys.Set(keyID, ak)

	info := toAccessKeyInfo(ak)

	return &info, nil
}

// DeleteAccessKey deletes a service account key.
func (m *Mock) DeleteAccessKey(
	_ context.Context, userName, accessKeyID string,
) error {
	ak, ok := m.accessKeys.Get(accessKeyID)
	if !ok {
		return cerrors.Newf(
			cerrors.NotFound,
			"access key %q not found", accessKeyID,
		)
	}

	if ak.UserName != userName {
		return cerrors.Newf(
			cerrors.NotFound,
			"access key %q not found for user %q",
			accessKeyID, userName,
		)
	}

	m.accessKeys.Delete(accessKeyID)

	return nil
}

// ListAccessKeys returns all keys for the given service account.
func (m *Mock) ListAccessKeys(
	_ context.Context, userName string,
) ([]driver.AccessKeyInfo, error) {
	if !m.users.Has(userName) {
		return nil, cerrors.Newf(
			cerrors.NotFound,
			"service account %q not found", userName,
		)
	}

	all := m.accessKeys.All()

	var result []driver.AccessKeyInfo

	for _, ak := range all {
		if ak.UserName == userName {
			result = append(result, toAccessKeyInfo(ak))
		}
	}

	return result, nil
}

// CreateInstanceProfile creates a new service account binding (instance profile).
func (m *Mock) CreateInstanceProfile(
	_ context.Context, cfg driver.InstanceProfileConfig,
) (*driver.InstanceProfileInfo, error) {
	if cfg.Name == "" {
		return nil, cerrors.Newf(
			cerrors.InvalidArgument,
			"service account binding name is required",
		)
	}

	if m.instanceProfiles.Has(cfg.Name) {
		return nil, cerrors.Newf(
			cerrors.AlreadyExists,
			"service account binding %q already exists", cfg.Name,
		)
	}

	id := idgen.GenerateID("sa-binding-")
	arn := idgen.GCPID(
		m.opts.ProjectID,
		"serviceAccountBindings", cfg.Name,
	)

	info := &driver.InstanceProfileInfo{
		ID:        id,
		Name:      cfg.Name,
		RoleName:  cfg.RoleName,
		ARN:       arn,
		CreatedAt: m.opts.Clock.Now().UTC().Format(timeFormat),
		Tags:      copyTags(cfg.Tags),
	}
	m.instanceProfiles.Set(cfg.Name, info)

	return copyProfileInfo(info), nil
}

// DeleteInstanceProfile deletes the service account binding with the given name.
func (m *Mock) DeleteInstanceProfile(
	_ context.Context, name string,
) error {
	if !m.instanceProfiles.Delete(name) {
		return cerrors.Newf(
			cerrors.NotFound,
			"service account binding %q not found", name,
		)
	}

	return nil
}

// GetInstanceProfile returns the service account binding with the given name.
func (m *Mock) GetInstanceProfile(
	_ context.Context, name string,
) (*driver.InstanceProfileInfo, error) {
	p, ok := m.instanceProfiles.Get(name)
	if !ok {
		return nil, cerrors.Newf(
			cerrors.NotFound,
			"service account binding %q not found", name,
		)
	}

	return copyProfileInfo(p), nil
}

// ListInstanceProfiles returns all service account bindings.
func (m *Mock) ListInstanceProfiles(
	_ context.Context,
) ([]driver.InstanceProfileInfo, error) {
	all := m.instanceProfiles.All()
	result := make([]driver.InstanceProfileInfo, 0, len(all))

	for _, p := range all {
		result = append(result, *copyProfileInfo(p))
	}

	return result, nil
}

// AddRoleToInstanceProfile associates a role with a service account binding.
func (m *Mock) AddRoleToInstanceProfile(
	_ context.Context, profileName, roleName string,
) error {
	p, ok := m.instanceProfiles.Get(profileName)
	if !ok {
		return cerrors.Newf(
			cerrors.NotFound,
			"service account binding %q not found", profileName,
		)
	}

	if !m.roles.Has(roleName) {
		return cerrors.Newf(
			cerrors.NotFound, "role %q not found", roleName,
		)
	}

	if p.RoleName != "" {
		return cerrors.Newf(
			cerrors.AlreadyExists,
			"service account binding %q already has role %q",
			profileName, p.RoleName,
		)
	}

	p.RoleName = roleName
	m.instanceProfiles.Set(profileName, p)

	return nil
}

// RemoveRoleFromInstanceProfile removes a role from a service account binding.
func (m *Mock) RemoveRoleFromInstanceProfile(
	_ context.Context, profileName, roleName string,
) error {
	p, ok := m.instanceProfiles.Get(profileName)
	if !ok {
		return cerrors.Newf(
			cerrors.NotFound,
			"service account binding %q not found", profileName,
		)
	}

	if p.RoleName != roleName {
		return cerrors.Newf(
			cerrors.NotFound,
			"role %q is not associated with service account binding %q",
			roleName, profileName,
		)
	}

	p.RoleName = ""
	m.instanceProfiles.Set(profileName, p)

	return nil
}

func copyProfileInfo(p *driver.InstanceProfileInfo) *driver.InstanceProfileInfo {
	return &driver.InstanceProfileInfo{
		ID:        p.ID,
		Name:      p.Name,
		RoleName:  p.RoleName,
		ARN:       p.ARN,
		CreatedAt: p.CreatedAt,
		Tags:      copyTags(p.Tags),
	}
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

func toGroupInfo(g *groupData) driver.GroupInfo {
	return driver.GroupInfo{
		Name:      g.Name,
		Path:      g.Path,
		ARN:       g.ARN,
		CreatedAt: g.CreatedAt,
	}
}

func toAccessKeyInfo(ak *accessKeyData) driver.AccessKeyInfo {
	return driver.AccessKeyInfo{
		AccessKeyID:     ak.AccessKeyID,
		SecretAccessKey: ak.SecretAccessKey,
		UserName:        ak.UserName,
		Status:          ak.Status,
		CreatedAt:       ak.CreatedAt,
	}
}
