package eks

import (
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

// Cluster authentication modes. The order is the only direction EKS allows:
// CONFIG_MAP, then API_AND_CONFIG_MAP, then API. A mode can't be skipped or
// reverted.
const (
	authModeConfigMap       = defaultAuthenticationMode
	authModeAPIAndConfigMap = "API_AND_CONFIG_MAP"
	authModeAPI             = "API"
)

// defaultCreatorUser is the principal a cluster creator maps to when the
// request carries no credentials. It matches the STS GetCallerIdentity
// default for an unsigned call.
const defaultCreatorUser = "cloudemu"

func authModeOrder() []string {
	return []string{authModeConfigMap, authModeAPIAndConfigMap, authModeAPI}
}

func authModeRank(mode string) int {
	for i, m := range authModeOrder() {
		if m == mode {
			return i
		}
	}

	return -1
}

// apiAuthMode reports whether a mode includes the EKS access entry API.
func apiAuthMode(mode string) bool {
	return mode == authModeAPI || mode == authModeAPIAndConfigMap
}

func validateAuthMode(mode string) error {
	if authModeRank(mode) < 0 {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Unsupported authentication mode %s. Valid values are [%s]", mode, strings.Join(authModeOrder(), ", "))
	}

	return nil
}

// validateAuthModeUpdate allows only the next mode in the one-way order.
// Real EKS also rejects a request for the current mode, with the same
// message.
func validateAuthModeUpdate(from, to string) error {
	if err := validateAuthMode(to); err != nil {
		return err
	}

	if authModeRank(to) != authModeRank(from)+1 {
		return cerrors.Newf(cerrors.InvalidArgument, "Unsupported authentication mode update from %s to %s", from, to)
	}

	return nil
}

// creatorPrincipalArn maps the cluster creator to the IAM principal EKS puts
// in the bootstrap access entry. An assumed-role session maps to its role,
// as real EKS does. Any other ARN is kept as it is. A federated user can't be
// an access entry principal, so its bootstrap entry is skipped later, as on
// AWS. Without an ARN it falls back to a user named after the access key,
// like STS does, or to the default user.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) creatorPrincipalArn(cfg eksdriver.ClusterConfig) string {
	arn := cfg.CreatorPrincipalArn

	if role, ok := assumedRoleToRole(arn); ok {
		return role
	}

	if arn != "" {
		return arn
	}

	name := defaultCreatorUser
	if cfg.CreatorAccessKeyID != "" {
		name = strings.ReplaceAll(cfg.CreatorAccessKeyID, "/", "-")
	}

	return "arn:aws:iam::" + m.opts.AccountID + ":user/" + name
}

// assumedRoleToRole turns arn:<partition>:sts::<account>:assumed-role/<role>/<session>
// into arn:<partition>:iam::<account>:role/<role>. ok is false for any other
// ARN. An assumed-role ARN carries no role path, so none can be kept.
func assumedRoleToRole(arn string) (string, bool) {
	fields := strings.SplitN(arn, ":", arnParts)
	if len(fields) != arnParts || fields[0] != "arn" || fields[2] != "sts" || fields[3] != "" {
		return "", false
	}

	rest, ok := strings.CutPrefix(fields[5], "assumed-role/")
	if !ok {
		return "", false
	}

	role, session, ok := strings.Cut(rest, "/")
	if !ok || role == "" || session == "" {
		return "", false
	}

	return "arn:" + fields[1] + ":iam::" + fields[4] + ":role/" + role, true
}

// bootstrapCreatorEntryLocked gives the cluster creator a STANDARD access
// entry with AmazonEKSClusterAdminPolicy at cluster scope. Real EKS does this
// when bootstrapClusterCreatorAdminPermissions is true and the mode includes
// the API, either at create time or when a CONFIG_MAP cluster first moves to
// an API mode. A creator ARN EKS can't use as a principal is skipped, and an
// existing entry for the creator is kept. Callers hold m.mu.
func (m *Mock) bootstrapCreatorEntryLocked(c *eksdriver.Cluster) {
	if !c.AccessConfig.BootstrapClusterCreatorAdminPermissions || !apiAuthMode(c.AccessConfig.AuthenticationMode) {
		return
	}

	principalArn := c.CreatorPrincipalArn

	p, err := parsePrincipal(principalArn)
	if err != nil {
		return
	}

	if _, ok := m.accessEntries.Get(accessEntryKey(c.Name, principalArn)); ok {
		return
	}

	now := m.opts.Clock.Now().UTC()
	m.accessEntries.Set(accessEntryKey(c.Name, principalArn), eksdriver.AccessEntry{
		ClusterName:  c.Name,
		PrincipalArn: principalArn,
		ARN:          m.accessEntryARN(c, p),
		Type:         eksdriver.AccessEntryTypeStandard,
		Username:     defaultUsername(eksdriver.AccessEntryTypeStandard, p),
		CreatedAt:    now,
		ModifiedAt:   now,
		Policies: []eksdriver.AssociatedAccessPolicy{{
			PolicyArn:    accessPolicyARNPrefix + "AmazonEKSClusterAdminPolicy",
			AccessScope:  eksdriver.AccessScope{Type: eksdriver.AccessScopeCluster},
			AssociatedAt: now,
			ModifiedAt:   now,
		}},
	})
}
