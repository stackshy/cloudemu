package eks

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

// Error messages real EKS returns for access entry calls.
const (
	msgAuthModeNotAPI = "The cluster's authentication mode must be set to one of [API, API_AND_CONFIG_MAP] " +
		"to perform this operation."
	msgEntryInUse    = "The specified access entry resource is already in use on this cluster."
	msgEntryNotFound = "The specified principalArn could not be found. " +
		"You can view your available access entries with 'list-access-entries'."
	msgPolicyNotAssociated = "The specified policyArn is not associated with the access entry."
)

// Default usernames EKS sets when the caller leaves username out.
const (
	usernameEC2Node     = "system:node:{{EC2PrivateDNSName}}"
	usernameSessionNode = "system:node:{{SessionName}}"
	sessionNameVar      = "{{SessionName}}"
	sessionNameRawVar   = "{{SessionNameRaw}}"
)

// IAM principal kinds an access entry accepts.
const (
	kindRole = "role"
	kindUser = "user"
)

// principal is a parsed IAM principal ARN.
type principal struct {
	account string
	kind    string // role or user
	path    string // path plus name, as it appears after role/ or user/
}

// name returns the principal name without its IAM path.
func (p principal) name() string {
	return p.path[strings.LastIndex(p.path, "/")+1:]
}

func accessEntryKey(clusterName, principalArn string) string {
	return clusterName + "|" + principalArn
}

// validAccessEntryTypes lists the types CreateAccessEntry accepts.
func validAccessEntryTypes() []string {
	return []string{
		eksdriver.AccessEntryTypeStandard, eksdriver.AccessEntryTypeFargateLinux,
		eksdriver.AccessEntryTypeEC2Linux, eksdriver.AccessEntryTypeEC2Windows,
		eksdriver.AccessEntryTypeEC2, eksdriver.AccessEntryTypeHybridLinux,
		eksdriver.AccessEntryTypeHyperPodLinux,
	}
}

// policiesAllowed reports whether an entry type can have access policies.
// Node types other than EC2 (Auto Mode) can't.
func policiesAllowed(entryType string) bool {
	return entryType == eksdriver.AccessEntryTypeStandard || entryType == eksdriver.AccessEntryTypeEC2
}

// accountIDLen is the length of an AWS account ID.
const accountIDLen = 12

// parsePrincipal validates an access entry principal ARN. It must be an IAM
// role or user, as in arn:aws:iam::111122223333:role/dev/my-role.
func parsePrincipal(arn string) (principal, error) {
	if strings.Contains(arn, ":assumed-role/") {
		return principal{}, cerrors.New(cerrors.InvalidArgument,
			"The principalArn parameter format is not valid. STS session principals are not supported.")
	}

	p, ok := splitPrincipalARN(arn)
	if !ok {
		return principal{}, cerrors.New(cerrors.InvalidArgument, "The principalArn parameter format is not valid.")
	}

	if p.kind == kindRole && strings.HasPrefix(p.path, "aws-service-role/") {
		return principal{}, cerrors.New(cerrors.InvalidArgument,
			"Service-linked roles are not supported as access entry principals.")
	}

	return p, nil
}

// splitPrincipalARN splits arn:<partition>:iam::<account>:<role|user>/<path>.
func splitPrincipalARN(arn string) (principal, bool) {
	fields := strings.SplitN(arn, ":", arnParts)
	if len(fields) != arnParts || fields[0] != "arn" || fields[2] != "iam" || fields[3] != "" {
		return principal{}, false
	}

	kind, path, ok := strings.Cut(fields[5], "/")
	if !ok || (kind != kindRole && kind != kindUser) || path == "" || strings.HasSuffix(path, "/") {
		return principal{}, false
	}

	return principal{account: fields[4], kind: kind, path: path}, isAccountID(fields[4])
}

func isAccountID(s string) bool {
	if len(s) != accountIDLen {
		return false
	}

	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

// validateUsername applies the EKS custom username rules.
func validateUsername(username string) error {
	for _, prefix := range []string{"system:", "eks:", "aws:", "amazon:", "iam:"} {
		if strings.HasPrefix(username, prefix) {
			return cerrors.Newf(cerrors.InvalidArgument,
				"The username %s is not valid. A username can't start with %s", username, prefix)
		}
	}

	for _, token := range []string{sessionNameRawVar, sessionNameVar} {
		idx := strings.Index(username, token)
		if idx >= 0 && !strings.Contains(username[:idx], ":") {
			return cerrors.Newf(cerrors.InvalidArgument,
				"The username %s is not valid. A colon must come before %s", username, token)
		}
	}

	return nil
}

// defaultUsername returns the username EKS generates for an entry.
func defaultUsername(entryType string, p principal) string {
	switch entryType {
	case eksdriver.AccessEntryTypeStandard:
		if p.kind == kindUser {
			return "arn:aws:iam::" + p.account + ":user/" + p.path
		}

		return "arn:aws:sts::" + p.account + ":assumed-role/" + p.name() + "/" + sessionNameVar
	case eksdriver.AccessEntryTypeEC2Linux, eksdriver.AccessEntryTypeEC2Windows, eksdriver.AccessEntryTypeEC2:
		return usernameEC2Node
	default:
		return usernameSessionNode
	}
}

// validateEntryType checks the type-specific access entry rules.
//
//nolint:gocritic // cfg is read-only here; a pointer would only obscure that.
func (m *Mock) validateEntryType(cfg eksdriver.AccessEntryConfig, p principal) error {
	if !containsString(validAccessEntryTypes(), cfg.Type) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"The type %s is not valid. Valid values are [%s]", cfg.Type, strings.Join(validAccessEntryTypes(), ", "))
	}

	if cfg.Type == eksdriver.AccessEntryTypeStandard {
		if cfg.Username != "" {
			return validateUsername(cfg.Username)
		}

		return nil
	}

	if p.kind != kindRole || p.account != m.opts.AccountID {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Access entries of type %s require an IAM role in the cluster's account.", cfg.Type)
	}

	if len(cfg.KubernetesGroups) > 0 || cfg.Username != "" {
		return cerrors.Newf(cerrors.InvalidArgument,
			"kubernetesGroups and username can't be set on an access entry of type %s.", cfg.Type)
	}

	return nil
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}

	return false
}

// accessEntryClusterLocked returns the cluster an access entry call targets.
// The cluster must exist and use an authentication mode that includes the
// EKS API. Callers hold m.mu.
func (m *Mock) accessEntryClusterLocked(name string) (*eksdriver.Cluster, error) {
	c, ok := m.clusters.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "No cluster found for name: %s.", name)
	}

	if c.AccessConfig.AuthenticationMode == defaultAuthenticationMode {
		return nil, cerrors.New(cerrors.FailedPrecondition, msgAuthModeNotAPI)
	}

	return &c, nil
}

// accessEntryLocked loads an access entry after the cluster checks pass.
func (m *Mock) accessEntryLocked(clusterName, principalArn string) (eksdriver.AccessEntry, error) {
	if _, err := m.accessEntryClusterLocked(clusterName); err != nil {
		return eksdriver.AccessEntry{}, err
	}

	e, ok := m.accessEntries.Get(accessEntryKey(clusterName, principalArn))
	if !ok {
		return eksdriver.AccessEntry{}, cerrors.New(cerrors.NotFound, msgEntryNotFound)
	}

	return e, nil
}

func (m *Mock) accessEntryARN(c *eksdriver.Cluster, p principal) string {
	return idgen.AWSARN("eks", arnRegion(c.ARN, m.opts.Region), m.opts.AccountID,
		"access-entry/"+c.Name+"/"+p.kind+"/"+p.account+"/"+p.name()+"/"+newUpdateID())
}

func copyAccessEntry(e *eksdriver.AccessEntry) *eksdriver.AccessEntry {
	out := *e
	out.KubernetesGroups = copyStrings(e.KubernetesGroups)
	out.Tags = copyTags(e.Tags)
	out.Policies = make([]eksdriver.AssociatedAccessPolicy, len(e.Policies))

	for i, p := range e.Policies {
		p.AccessScope.Namespaces = copyStrings(p.AccessScope.Namespaces)
		out.Policies[i] = p
	}

	return &out
}

// CreateAccessEntry creates an access entry for one IAM principal.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateAccessEntry(_ context.Context, cfg eksdriver.AccessEntryConfig) (*eksdriver.AccessEntry, error) {
	if cfg.Type == "" {
		cfg.Type = eksdriver.AccessEntryTypeStandard
	}

	p, err := parsePrincipal(cfg.PrincipalArn)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	c, err := m.accessEntryClusterLocked(cfg.ClusterName)
	if err != nil {
		return nil, err
	}

	key := accessEntryKey(cfg.ClusterName, cfg.PrincipalArn)
	if existing, ok := m.accessEntries.Get(key); ok {
		// A retry with the same client token returns the entry it made.
		if cfg.ClientRequestToken != "" && existing.ClientRequestToken == cfg.ClientRequestToken {
			return copyAccessEntry(&existing), nil
		}

		return nil, cerrors.New(cerrors.AlreadyExists, msgEntryInUse)
	}

	if err := m.validateEntryType(cfg, p); err != nil {
		return nil, err
	}

	username := cfg.Username
	if username == "" {
		username = defaultUsername(cfg.Type, p)
	}

	now := m.opts.Clock.Now().UTC()
	entry := eksdriver.AccessEntry{
		ClusterName:        cfg.ClusterName,
		PrincipalArn:       cfg.PrincipalArn,
		ARN:                m.accessEntryARN(c, p),
		Type:               cfg.Type,
		Username:           username,
		KubernetesGroups:   copyStrings(cfg.KubernetesGroups),
		Tags:               copyTags(cfg.Tags),
		CreatedAt:          now,
		ModifiedAt:         now,
		ClientRequestToken: cfg.ClientRequestToken,
	}

	m.accessEntries.Set(key, entry)

	return copyAccessEntry(&entry), nil
}

// DescribeAccessEntry returns one access entry.
func (m *Mock) DescribeAccessEntry(_ context.Context, clusterName, principalArn string) (*eksdriver.AccessEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	e, err := m.accessEntryLocked(clusterName, principalArn)
	if err != nil {
		return nil, err
	}

	return copyAccessEntry(&e), nil
}

// ListAccessEntries returns the principal ARNs of a cluster's access entries.
// A non-empty associatedPolicyArn keeps only entries that have that policy.
func (m *Mock) ListAccessEntries(_ context.Context, clusterName, associatedPolicyArn string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, err := m.accessEntryClusterLocked(clusterName); err != nil {
		return nil, err
	}

	out := make([]string, 0)

	//nolint:gocritic // Store.All copies values out anyway; the per-iter copy here is no extra cost.
	for _, e := range m.accessEntries.All() {
		if e.ClusterName != clusterName {
			continue
		}

		if associatedPolicyArn != "" && policyIndex(e.Policies, associatedPolicyArn) < 0 {
			continue
		}

		out = append(out, e.PrincipalArn)
	}

	return out, nil
}

// UpdateAccessEntry changes the Kubernetes groups or username of an entry.
// Node type entries can't have either, so only a no-op value is accepted.
func (m *Mock) UpdateAccessEntry(_ context.Context, upd eksdriver.AccessEntryUpdate) (*eksdriver.AccessEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, err := m.accessEntryLocked(upd.ClusterName, upd.PrincipalArn)
	if err != nil {
		return nil, err
	}

	if err := validateEntryUpdate(&e, upd); err != nil {
		return nil, err
	}

	if upd.KubernetesGroups != nil {
		e.KubernetesGroups = copyStrings(*upd.KubernetesGroups)
	}

	if upd.Username != nil && *upd.Username != "" {
		e.Username = *upd.Username
	}

	e.ModifiedAt = m.opts.Clock.Now().UTC()
	m.accessEntries.Set(accessEntryKey(e.ClusterName, e.PrincipalArn), e)

	return copyAccessEntry(&e), nil
}

func validateEntryUpdate(e *eksdriver.AccessEntry, upd eksdriver.AccessEntryUpdate) error {
	if e.Type != eksdriver.AccessEntryTypeStandard {
		groupsChanged := upd.KubernetesGroups != nil && len(*upd.KubernetesGroups) > 0
		userChanged := upd.Username != nil && *upd.Username != "" && *upd.Username != e.Username

		if groupsChanged || userChanged {
			return cerrors.Newf(cerrors.InvalidArgument,
				"kubernetesGroups and username can't be set on an access entry of type %s.", e.Type)
		}

		return nil
	}

	if upd.Username != nil && *upd.Username != "" && *upd.Username != e.Username {
		return validateUsername(*upd.Username)
	}

	return nil
}

// DeleteAccessEntry removes an access entry and its policy associations.
func (m *Mock) DeleteAccessEntry(_ context.Context, clusterName, principalArn string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.accessEntryLocked(clusterName, principalArn); err != nil {
		return err
	}

	m.accessEntries.Delete(accessEntryKey(clusterName, principalArn))

	return nil
}

// deleteClusterAccessEntriesLocked drops every access entry of a cluster.
// Real EKS deletes them together with the cluster. Callers hold m.mu.
func (m *Mock) deleteClusterAccessEntriesLocked(clusterName string) {
	//nolint:gocritic // Store.All copies values out anyway; the per-iter copy here is no extra cost.
	for key, e := range m.accessEntries.All() {
		if e.ClusterName == clusterName {
			m.accessEntries.Delete(key)
		}
	}
}

func policyIndex(policies []eksdriver.AssociatedAccessPolicy, policyArn string) int {
	for i := range policies {
		if policies[i].PolicyArn == policyArn {
			return i
		}
	}

	return -1
}

// validateAccessScope checks the scope of a policy association.
func validateAccessScope(scope eksdriver.AccessScope) error {
	switch scope.Type {
	case eksdriver.AccessScopeCluster:
		if len(scope.Namespaces) > 0 {
			return cerrors.New(cerrors.InvalidArgument, "namespaces can't be set when the access scope type is cluster.")
		}
	case eksdriver.AccessScopeNamespace:
		if len(scope.Namespaces) == 0 {
			return cerrors.New(cerrors.InvalidArgument, "namespaces must be set when the access scope type is namespace.")
		}
	default:
		return cerrors.Newf(cerrors.InvalidArgument,
			"The access scope type %q is not valid. Valid values are [cluster, namespace]", scope.Type)
	}

	return nil
}

// AssociateAccessPolicy links an access policy to an access entry. Calling
// it again for the same policy replaces the scope.
func (m *Mock) AssociateAccessPolicy(
	_ context.Context, clusterName, principalArn, policyArn string, scope eksdriver.AccessScope,
) (*eksdriver.AssociatedAccessPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, err := m.accessEntryLocked(clusterName, principalArn)
	if err != nil {
		return nil, err
	}

	if err := validatePolicyAssociation(&e, policyArn, scope); err != nil {
		return nil, err
	}

	now := m.opts.Clock.Now().UTC()
	assoc := eksdriver.AssociatedAccessPolicy{
		PolicyArn:    policyArn,
		AccessScope:  eksdriver.AccessScope{Type: scope.Type, Namespaces: copyStrings(scope.Namespaces)},
		AssociatedAt: now,
		ModifiedAt:   now,
	}

	if i := policyIndex(e.Policies, policyArn); i >= 0 {
		assoc.AssociatedAt = e.Policies[i].AssociatedAt
		e.Policies[i] = assoc
	} else {
		e.Policies = append(e.Policies, assoc)
	}

	m.accessEntries.Set(accessEntryKey(clusterName, principalArn), e)

	out := assoc
	out.AccessScope.Namespaces = copyStrings(assoc.AccessScope.Namespaces)

	return &out, nil
}

func validatePolicyAssociation(e *eksdriver.AccessEntry, policyArn string, scope eksdriver.AccessScope) error {
	if !policiesAllowed(e.Type) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Access policies can't be associated with an access entry of type %s.", e.Type)
	}

	def, ok := lookupAccessPolicy(policyArn)
	if !ok {
		return cerrors.Newf(cerrors.InvalidArgument, "The policyArn %s is not a valid access policy.", policyArn)
	}

	if def.serviceLinkedOnly {
		return cerrors.Newf(cerrors.InvalidArgument,
			"The access policy %s can only be associated with an AWS service-linked role.", policyArn)
	}

	return validateAccessScope(scope)
}

// DisassociateAccessPolicy removes an access policy from an access entry.
func (m *Mock) DisassociateAccessPolicy(_ context.Context, clusterName, principalArn, policyArn string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, err := m.accessEntryLocked(clusterName, principalArn)
	if err != nil {
		return err
	}

	i := policyIndex(e.Policies, policyArn)
	if i < 0 {
		return cerrors.New(cerrors.NotFound, msgPolicyNotAssociated)
	}

	e.Policies = append(e.Policies[:i], e.Policies[i+1:]...)
	m.accessEntries.Set(accessEntryKey(clusterName, principalArn), e)

	return nil
}

// ListAssociatedAccessPolicies returns the policies linked to an entry.
func (m *Mock) ListAssociatedAccessPolicies(
	_ context.Context, clusterName, principalArn string,
) ([]eksdriver.AssociatedAccessPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	e, err := m.accessEntryLocked(clusterName, principalArn)
	if err != nil {
		return nil, err
	}

	return copyAccessEntry(&e).Policies, nil
}
