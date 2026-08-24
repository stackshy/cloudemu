// Package iam provides an in-memory mock implementation of AWS IAM.
package iam

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const timeFormat = "2006-01-02T15:04:05Z"

// defaultMaxSessionDuration is the AWS default (1 hour) for a role's session
// duration when the create request does not specify one.
const defaultMaxSessionDuration = 3600

// Compile-time check that Mock implements driver.IAM.
var _ driver.IAM = (*Mock)(nil)

// Mock is an in-memory mock implementation of the AWS IAM service.
type Mock struct {
	users            *memstore.Store[*userData]
	roles            *memstore.Store[*roleData]
	policies         *memstore.Store[*policyData]
	groups           *memstore.Store[*groupData]
	accessKeys       *memstore.Store[*accessKeyData]
	instanceProfiles *memstore.Store[*driver.InstanceProfileInfo]

	mu            sync.RWMutex
	userPolicies  map[string]map[string]bool // userName -> set of managed policy ARNs
	rolePolicies  map[string]map[string]bool // roleName -> set of managed policy ARNs
	groupPolicies map[string]map[string]bool // groupName -> set of managed policy ARNs
	groupUsers    map[string]map[string]bool // groupName -> set of userNames

	userInlinePolicies  map[string]map[string]string // userName -> policyName -> document
	groupInlinePolicies map[string]map[string]string // groupName -> policyName -> document

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
	Description         string
	AssumeRolePolicyDoc string
	MaxSessionDuration  int
	CreatedAt           string
	Tags                map[string]string
	inlinePolicies      map[string]string // policyName -> policy document JSON
}

type policyData struct {
	Name           string
	ID             string
	ARN            string
	Path           string
	PolicyDocument string
	Description    string
	versions       []*policyVersionData
	versionCounter int
}

type policyVersionData struct {
	VersionID      string
	PolicyDocument string
	IsDefault      bool
	CreatedAt      string
}

type groupData struct {
	Name      string
	ID        string
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

// New creates a new IAM mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		users:               memstore.New[*userData](),
		roles:               memstore.New[*roleData](),
		policies:            memstore.New[*policyData](),
		groups:              memstore.New[*groupData](),
		accessKeys:          memstore.New[*accessKeyData](),
		instanceProfiles:    memstore.New[*driver.InstanceProfileInfo](),
		userPolicies:        make(map[string]map[string]bool),
		rolePolicies:        make(map[string]map[string]bool),
		groupPolicies:       make(map[string]map[string]bool),
		groupUsers:          make(map[string]map[string]bool),
		userInlinePolicies:  make(map[string]map[string]string),
		groupInlinePolicies: make(map[string]map[string]string),
		opts:                opts,
	}
}

// CreateUser creates a new IAM user.
func (m *Mock) CreateUser(_ context.Context, cfg driver.UserConfig) (*driver.UserInfo, error) {
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
		CreatedAt: m.opts.Clock.Now().UTC().Format(timeFormat),
	}
	m.users.Set(cfg.Name, u)

	info := toUserInfo(u)

	return &info, nil
}

// DeleteUser deletes the IAM user with the given name. Like real IAM it refuses
// (DeleteConflict) while managed policies are still attached, access keys still
// exist, or the user is still a member of a group — the caller must remove
// those first.
func (m *Mock) DeleteUser(_ context.Context, name string) error {
	if !m.users.Has(name) {
		return errors.Newf(errors.NotFound, "user %q not found", name)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.userPolicies[name]) > 0 {
		return errors.Newf(errors.FailedPrecondition,
			"cannot delete user %q: managed policies are still attached (detach them first)", name)
	}

	if len(m.userInlinePolicies[name]) > 0 {
		return errors.Newf(errors.FailedPrecondition,
			"cannot delete user %q: inline policies still exist (delete them first)", name)
	}

	for _, members := range m.groupUsers {
		if members[name] {
			return errors.Newf(errors.FailedPrecondition,
				"cannot delete user %q: still a member of a group (remove it first)", name)
		}
	}

	for _, ak := range m.accessKeys.All() {
		if ak.UserName == name {
			return errors.Newf(errors.FailedPrecondition,
				"cannot delete user %q: access keys still exist (delete them first)", name)
		}
	}

	m.users.Delete(name)
	delete(m.userPolicies, name)
	delete(m.userInlinePolicies, name)

	return nil
}

// GetUser returns the IAM user with the given name.
func (m *Mock) GetUser(_ context.Context, name string) (*driver.UserInfo, error) {
	u, ok := m.users.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "user %q not found", name)
	}

	info := toUserInfo(u)

	return &info, nil
}

// ListUsers returns all IAM users.
func (m *Mock) ListUsers(_ context.Context) ([]driver.UserInfo, error) {
	all := m.users.All()
	result := make([]driver.UserInfo, 0, len(all))

	for _, u := range all {
		result = append(result, toUserInfo(u))
	}

	return result, nil
}

// CreateRole creates a new IAM role.
func (m *Mock) CreateRole(_ context.Context, cfg driver.RoleConfig) (*driver.RoleInfo, error) {
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

	maxSession := cfg.MaxSessionDuration
	if maxSession == 0 {
		maxSession = defaultMaxSessionDuration // AWS default when unspecified
	}

	r := &roleData{
		Name:                cfg.Name,
		ID:                  id,
		ARN:                 arn,
		Path:                path,
		Description:         cfg.Description,
		AssumeRolePolicyDoc: cfg.AssumeRolePolicyDoc,
		MaxSessionDuration:  maxSession,
		CreatedAt:           m.opts.Clock.Now().UTC().Format(timeFormat),
		Tags:                tags,
	}
	m.roles.Set(cfg.Name, r)

	info := toRoleInfo(r)

	return &info, nil
}

// DeleteRole deletes the IAM role with the given name. Like real IAM, it
// refuses (DeleteConflict) while managed policies are still attached or inline
// policies still exist — the caller must detach/delete them first.
func (m *Mock) DeleteRole(_ context.Context, name string) error {
	role, ok := m.roles.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "role %q not found", name)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.rolePolicies[name]) > 0 {
		return errors.Newf(errors.FailedPrecondition,
			"cannot delete role %q: managed policies are still attached (detach them first)", name)
	}

	if len(role.inlinePolicies) > 0 {
		return errors.Newf(errors.FailedPrecondition,
			"cannot delete role %q: inline policies still exist (delete them first)", name)
	}

	for _, p := range m.instanceProfiles.All() {
		if p.RoleName == name {
			return errors.Newf(errors.FailedPrecondition,
				"cannot delete role %q: it is still associated with instance profile %q "+
					"(remove it with RemoveRoleFromInstanceProfile first)", name, p.Name)
		}
	}

	m.roles.Delete(name)
	delete(m.rolePolicies, name)

	return nil
}

// GetRole returns the IAM role with the given name.
func (m *Mock) GetRole(_ context.Context, name string) (*driver.RoleInfo, error) {
	r, ok := m.roles.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "role %q not found", name)
	}

	info := toRoleInfo(r)

	return &info, nil
}

// ListRoles returns all IAM roles.
func (m *Mock) ListRoles(_ context.Context) ([]driver.RoleInfo, error) {
	all := m.roles.All()
	result := make([]driver.RoleInfo, 0, len(all))

	for _, r := range all {
		result = append(result, toRoleInfo(r))
	}

	return result, nil
}

// CreatePolicy creates a new IAM policy.
func (m *Mock) CreatePolicy(_ context.Context, cfg driver.PolicyConfig) (*driver.PolicyInfo, error) {
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
	seedInitialVersion(p, cfg.PolicyDocument, m.opts.Clock.Now().UTC().Format(timeFormat))

	// Snapshot before publishing p to the store: once Set runs, p is shared and
	// its document may be mutated concurrently by the version operations.
	info := toPolicyInfo(p)
	m.policies.Set(arn, p)

	return &info, nil
}

// DeletePolicy deletes the IAM policy with the given ARN. Like real IAM it
// refuses (DeleteConflict) while the policy is still attached to any user or
// role — the caller must detach it everywhere first.
func (m *Mock) DeletePolicy(_ context.Context, arn string) error {
	if !m.policies.Has(arn) {
		return errors.Newf(errors.NotFound, "policy %q not found", arn)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.policyAttachmentCountLocked(arn) > 0 {
		return errors.Newf(errors.FailedPrecondition,
			"cannot delete policy %q: still attached to one or more users or roles (detach it first)", arn)
	}

	m.policies.Delete(arn)

	return nil
}

// policyAttachmentCountLocked returns how many users and roles currently have
// the given policy ARN attached. The caller must hold m.mu.
func (m *Mock) policyAttachmentCountLocked(arn string) int {
	count := 0

	for _, arns := range m.userPolicies {
		if arns[arn] {
			count++
		}
	}

	for _, arns := range m.rolePolicies {
		if arns[arn] {
			count++
		}
	}

	return count
}

// PolicyAttachmentCount returns how many principals have the given managed
// policy attached. It exists for the wire layer to populate AttachmentCount and
// to honor ListPolicies OnlyAttached; it is not part of the portable driver.
func (m *Mock) PolicyAttachmentCount(_ context.Context, arn string) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.policyAttachmentCountLocked(arn), nil
}

// GetPolicy returns the IAM policy with the given ARN.
func (m *Mock) GetPolicy(_ context.Context, arn string) (*driver.PolicyInfo, error) {
	// Same reasoning as attachPolicy: an AWS-managed policy is readable in
	// every real account without having been created.
	m.ensureAWSManagedPolicy(arn)

	p, ok := m.policies.Get(arn)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "policy %q not found", arn)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	info := toPolicyInfo(p)

	return &info, nil
}

// ListPolicies returns all IAM policies.
func (m *Mock) ListPolicies(_ context.Context) ([]driver.PolicyInfo, error) {
	all := m.policies.All()
	result := make([]driver.PolicyInfo, 0, len(all))

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, p := range all {
		result = append(result, toPolicyInfo(p))
	}

	return result, nil
}

const maxPolicyVersions = 5

// seedInitialVersion records the default v1 version when a policy is created.
func seedInitialVersion(p *policyData, document, createdAt string) {
	p.versionCounter = 1
	p.versions = []*policyVersionData{{
		VersionID:      "v1",
		PolicyDocument: document,
		IsDefault:      true,
		CreatedAt:      createdAt,
	}}
}

// CreatePolicyVersion adds a new version to a managed policy, optionally as the default.
func (m *Mock) CreatePolicyVersion(_ context.Context, cfg driver.PolicyVersionConfig) (*driver.PolicyVersionInfo, error) {
	p, ok := m.policies.Get(cfg.PolicyARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "policy %q not found", cfg.PolicyARN)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(p.versions) >= maxPolicyVersions {
		return nil, errors.Newf(errors.ResourceExhausted,
			"policy %q already has the maximum of %d versions", cfg.PolicyARN, maxPolicyVersions)
	}

	p.versionCounter++
	v := &policyVersionData{
		VersionID:      fmt.Sprintf("v%d", p.versionCounter),
		PolicyDocument: cfg.PolicyDocument,
		IsDefault:      cfg.SetAsDefault,
		CreatedAt:      m.opts.Clock.Now().UTC().Format(timeFormat),
	}

	if cfg.SetAsDefault {
		clearDefaults(p.versions)
		p.PolicyDocument = cfg.PolicyDocument
	}

	p.versions = append(p.versions, v)
	m.policies.Set(cfg.PolicyARN, p)

	info := toPolicyVersionInfo(v)

	return &info, nil
}

// GetPolicyVersion returns a single version of a managed policy.
func (m *Mock) GetPolicyVersion(_ context.Context, policyARN, versionID string) (*driver.PolicyVersionInfo, error) {
	p, ok := m.policies.Get(policyARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "policy %q not found", policyARN)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, v := range p.versions {
		if v.VersionID == versionID {
			info := toPolicyVersionInfo(v)
			return &info, nil
		}
	}

	return nil, errors.Newf(errors.NotFound, "version %q not found for policy %q", versionID, policyARN)
}

// ListPolicyVersions returns all versions of a managed policy.
func (m *Mock) ListPolicyVersions(_ context.Context, policyARN string) ([]driver.PolicyVersionInfo, error) {
	p, ok := m.policies.Get(policyARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "policy %q not found", policyARN)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]driver.PolicyVersionInfo, 0, len(p.versions))
	for _, v := range p.versions {
		result = append(result, toPolicyVersionInfo(v))
	}

	return result, nil
}

// DeletePolicyVersion removes a non-default version of a managed policy.
func (m *Mock) DeletePolicyVersion(_ context.Context, policyARN, versionID string) error {
	p, ok := m.policies.Get(policyARN)
	if !ok {
		return errors.Newf(errors.NotFound, "policy %q not found", policyARN)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for idx, v := range p.versions {
		if v.VersionID != versionID {
			continue
		}

		if v.IsDefault {
			return errors.Newf(errors.FailedPrecondition,
				"cannot delete the default version %q of policy %q", versionID, policyARN)
		}

		p.versions = append(p.versions[:idx], p.versions[idx+1:]...)
		m.policies.Set(policyARN, p)

		return nil
	}

	return errors.Newf(errors.NotFound, "version %q not found for policy %q", versionID, policyARN)
}

// SetDefaultPolicyVersion marks an existing version as the default for a managed policy.
func (m *Mock) SetDefaultPolicyVersion(_ context.Context, policyARN, versionID string) error {
	p, ok := m.policies.Get(policyARN)
	if !ok {
		return errors.Newf(errors.NotFound, "policy %q not found", policyARN)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	target := findVersion(p.versions, versionID)
	if target == nil {
		return errors.Newf(errors.NotFound, "version %q not found for policy %q", versionID, policyARN)
	}

	clearDefaults(p.versions)

	target.IsDefault = true
	p.PolicyDocument = target.PolicyDocument
	m.policies.Set(policyARN, p)

	return nil
}

func clearDefaults(versions []*policyVersionData) {
	for _, v := range versions {
		v.IsDefault = false
	}
}

func findVersion(versions []*policyVersionData, versionID string) *policyVersionData {
	for _, v := range versions {
		if v.VersionID == versionID {
			return v
		}
	}

	return nil
}

func toPolicyVersionInfo(v *policyVersionData) driver.PolicyVersionInfo {
	return driver.PolicyVersionInfo{
		VersionID:        v.VersionID,
		PolicyDocument:   v.PolicyDocument,
		IsDefaultVersion: v.IsDefault,
		CreatedAt:        v.CreatedAt,
	}
}

func (m *Mock) attachPolicy(
	principalStore interface{ Has(string) bool },
	principalName, policyARN string,
	policyMap map[string]map[string]bool,
	entityType string,
) error {
	if !principalStore.Has(principalName) {
		return errors.Newf(errors.NotFound, "%s %q not found", entityType, principalName)
	}

	// AWS-managed policies are never created by the caller — they already
	// exist in every account — so attaching one must not require a preceding
	// CreatePolicy.
	if !m.ensureAWSManagedPolicy(policyARN) {
		return errors.Newf(errors.NotFound, "policy %q not found", policyARN)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if policyMap[principalName] == nil {
		policyMap[principalName] = make(map[string]bool)
	}

	policyMap[principalName][policyARN] = true

	return nil
}

// AttachUserPolicy attaches a policy to a user.
func (m *Mock) AttachUserPolicy(_ context.Context, userName, policyARN string) error {
	return m.attachPolicy(m.users, userName, policyARN, m.userPolicies, "user")
}

// DetachUserPolicy detaches a policy from a user.
func (m *Mock) DetachUserPolicy(_ context.Context, userName, policyARN string) error {
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
func (m *Mock) AttachRolePolicy(_ context.Context, roleName, policyARN string) error {
	return m.attachPolicy(m.roles, roleName, policyARN, m.rolePolicies, "role")
}

// DetachRolePolicy detaches a policy from a role.
func (m *Mock) DetachRolePolicy(_ context.Context, roleName, policyARN string) error {
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
func (m *Mock) ListAttachedUserPolicies(_ context.Context, userName string) ([]string, error) {
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
func (m *Mock) ListAttachedRolePolicies(_ context.Context, roleName string) ([]string, error) {
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

	m.mu.RLock()
	defer m.mu.RUnlock()

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

// CreateGroup creates a new IAM group.
func (m *Mock) CreateGroup(
	_ context.Context, cfg driver.GroupConfig,
) (*driver.GroupInfo, error) {
	if cfg.Name == "" {
		return nil, errors.Newf(
			errors.InvalidArgument, "group name is required",
		)
	}

	if m.groups.Has(cfg.Name) {
		return nil, errors.Newf(
			errors.AlreadyExists, "group %q already exists", cfg.Name,
		)
	}

	path := cfg.Path
	if path == "" {
		path = "/"
	}

	arn := idgen.AWSARN(
		"iam", "", m.opts.AccountID, "group/"+cfg.Name,
	)

	g := &groupData{
		Name:      cfg.Name,
		ID:        idgen.GenerateID("AGPA"),
		ARN:       arn,
		Path:      path,
		CreatedAt: m.opts.Clock.Now().UTC().Format(timeFormat),
	}
	m.groups.Set(cfg.Name, g)

	info := toGroupInfo(g)

	return &info, nil
}

// DeleteGroup deletes the IAM group with the given name.
func (m *Mock) DeleteGroup(_ context.Context, name string) error {
	if !m.groups.Has(name) {
		return errors.Newf(
			errors.NotFound, "group %q not found", name,
		)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.groupUsers[name]) > 0 {
		return errors.Newf(errors.FailedPrecondition,
			"cannot delete group %q: it still has member users (remove them first)", name)
	}

	if len(m.groupPolicies[name]) > 0 {
		return errors.Newf(errors.FailedPrecondition,
			"cannot delete group %q: managed policies are still attached (detach them first)", name)
	}

	if len(m.groupInlinePolicies[name]) > 0 {
		return errors.Newf(errors.FailedPrecondition,
			"cannot delete group %q: inline policies still exist (delete them first)", name)
	}

	m.groups.Delete(name)
	delete(m.groupUsers, name)
	delete(m.groupPolicies, name)
	delete(m.groupInlinePolicies, name)

	return nil
}

// GetGroup returns the IAM group with the given name.
func (m *Mock) GetGroup(
	_ context.Context, name string,
) (*driver.GroupInfo, error) {
	g, ok := m.groups.Get(name)
	if !ok {
		return nil, errors.Newf(
			errors.NotFound, "group %q not found", name,
		)
	}

	info := toGroupInfo(g)

	return &info, nil
}

// ListGroups returns all IAM groups.
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

// ListGroupMembers returns the users that belong to the given group. It exists
// for the wire layer to populate GetGroup's Users list; it is not part of the
// portable driver.
func (m *Mock) ListGroupMembers(_ context.Context, groupName string) ([]driver.UserInfo, error) {
	if !m.groups.Has(groupName) {
		return nil, errors.Newf(errors.NotFound, "group %q not found", groupName)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	members := m.groupUsers[groupName]
	result := make([]driver.UserInfo, 0, len(members))

	for userName := range members {
		u, ok := m.users.Get(userName)
		if !ok {
			continue
		}

		result = append(result, toUserInfo(u))
	}

	return result, nil
}

// AddUserToGroup adds a user to a group.
func (m *Mock) AddUserToGroup(
	_ context.Context, userName, groupName string,
) error {
	if !m.users.Has(userName) {
		return errors.Newf(
			errors.NotFound, "user %q not found", userName,
		)
	}

	if !m.groups.Has(groupName) {
		return errors.Newf(
			errors.NotFound, "group %q not found", groupName,
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
		return errors.Newf(
			errors.NotFound, "group %q not found", groupName,
		)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	members, ok := m.groupUsers[groupName]
	if !ok || !members[userName] {
		return errors.Newf(
			errors.NotFound,
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
		return nil, errors.Newf(
			errors.NotFound, "user %q not found", userName,
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

// CreateAccessKey creates a new access key for the given user.
func (m *Mock) CreateAccessKey(
	_ context.Context, cfg driver.AccessKeyConfig,
) (*driver.AccessKeyInfo, error) {
	if cfg.UserName == "" {
		return nil, errors.Newf(
			errors.InvalidArgument, "user name is required",
		)
	}

	if !m.users.Has(cfg.UserName) {
		return nil, errors.Newf(
			errors.NotFound, "user %q not found", cfg.UserName,
		)
	}

	keyID := fmt.Sprintf("AKIA%s", idgen.GenerateID(""))
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

// DeleteAccessKey deletes an access key.
func (m *Mock) DeleteAccessKey(
	_ context.Context, userName, accessKeyID string,
) error {
	ak, ok := m.accessKeys.Get(accessKeyID)
	if !ok {
		return errors.Newf(
			errors.NotFound,
			"access key %q not found", accessKeyID,
		)
	}

	if ak.UserName != userName {
		return errors.Newf(
			errors.NotFound,
			"access key %q not found for user %q",
			accessKeyID, userName,
		)
	}

	m.accessKeys.Delete(accessKeyID)

	return nil
}

// ListAccessKeys returns all access keys for the given user.
func (m *Mock) ListAccessKeys(
	_ context.Context, userName string,
) ([]driver.AccessKeyInfo, error) {
	if !m.users.Has(userName) {
		return nil, errors.Newf(
			errors.NotFound, "user %q not found", userName,
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

// CreateInstanceProfile creates a new instance profile.
func (m *Mock) CreateInstanceProfile(
	_ context.Context, cfg driver.InstanceProfileConfig,
) (*driver.InstanceProfileInfo, error) {
	if cfg.Name == "" {
		return nil, errors.Newf(
			errors.InvalidArgument,
			"instance profile name is required",
		)
	}

	if m.instanceProfiles.Has(cfg.Name) {
		return nil, errors.Newf(
			errors.AlreadyExists,
			"instance profile %q already exists", cfg.Name,
		)
	}

	id := idgen.GenerateID("AIPA")
	arn := idgen.AWSARN(
		"iam", "", m.opts.AccountID,
		"instance-profile/"+cfg.Name,
	)

	path := cfg.Path
	if path == "" {
		path = "/"
	}

	info := &driver.InstanceProfileInfo{
		ID:        id,
		Name:      cfg.Name,
		Path:      path,
		RoleName:  cfg.RoleName,
		ARN:       arn,
		CreatedAt: m.opts.Clock.Now().UTC().Format(timeFormat),
		Tags:      copyTags(cfg.Tags),
	}
	m.instanceProfiles.Set(cfg.Name, info)

	return copyProfileInfo(info), nil
}

// DeleteInstanceProfile deletes the instance profile with the given name.
func (m *Mock) DeleteInstanceProfile(
	_ context.Context, name string,
) error {
	p, ok := m.instanceProfiles.Get(name)
	if !ok {
		return errors.Newf(
			errors.NotFound,
			"instance profile %q not found", name,
		)
	}

	if p.RoleName != "" {
		return errors.Newf(errors.FailedPrecondition,
			"cannot delete instance profile %q: role %q is still associated "+
				"(remove it with RemoveRoleFromInstanceProfile first)", name, p.RoleName)
	}

	m.instanceProfiles.Delete(name)

	return nil
}

// GetInstanceProfile returns the instance profile with the given name.
func (m *Mock) GetInstanceProfile(
	_ context.Context, name string,
) (*driver.InstanceProfileInfo, error) {
	p, ok := m.instanceProfiles.Get(name)
	if !ok {
		return nil, errors.Newf(
			errors.NotFound,
			"instance profile %q not found", name,
		)
	}

	return copyProfileInfo(p), nil
}

// ListInstanceProfiles returns all instance profiles.
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

// AddRoleToInstanceProfile associates a role with an instance profile.
func (m *Mock) AddRoleToInstanceProfile(
	_ context.Context, profileName, roleName string,
) error {
	p, ok := m.instanceProfiles.Get(profileName)
	if !ok {
		return errors.Newf(
			errors.NotFound,
			"instance profile %q not found", profileName,
		)
	}

	if !m.roles.Has(roleName) {
		return errors.Newf(
			errors.NotFound, "role %q not found", roleName,
		)
	}

	if p.RoleName != "" {
		return errors.Newf(
			errors.AlreadyExists,
			"instance profile %q already has role %q",
			profileName, p.RoleName,
		)
	}

	p.RoleName = roleName
	m.instanceProfiles.Set(profileName, p)

	return nil
}

// RemoveRoleFromInstanceProfile removes a role from an instance profile.
func (m *Mock) RemoveRoleFromInstanceProfile(
	_ context.Context, profileName, roleName string,
) error {
	p, ok := m.instanceProfiles.Get(profileName)
	if !ok {
		return errors.Newf(
			errors.NotFound,
			"instance profile %q not found", profileName,
		)
	}

	if p.RoleName != roleName {
		return errors.Newf(
			errors.NotFound,
			"role %q is not associated with instance profile %q",
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
		Path:      p.Path,
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
		Description:         r.Description,
		AssumeRolePolicyDoc: r.AssumeRolePolicyDoc,
		MaxSessionDuration:  r.MaxSessionDuration,
		CreatedAt:           r.CreatedAt,
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
		ID:        g.ID,
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
