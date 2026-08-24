package iam

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// Account-level IAM operations (GetAccountSummary, the account password policy,
// and MFA devices) are AWS-only; the wire layer reaches them via type
// assertions on the Mock rather than the portable driver.

// AWS default account quotas reported by GetAccountSummary. Real values, held
// as named constants to keep the summary map free of magic numbers.
const (
	quotaUsers                      = 5000
	quotaGroups                     = 300
	quotaRoles                      = 1000
	quotaPolicies                   = 1500
	quotaInstanceProfiles           = 1000
	quotaServerCertificates         = 20
	quotaGroupsPerUser              = 10
	quotaSigningCertificatesPerUser = 2
	quotaAttachedPoliciesPerGroup   = 10
	quotaAttachedPoliciesPerRole    = 10
	quotaAttachedPoliciesPerUser    = 10
	quotaVersionsPerPolicy          = 5
	quotaPolicySize                 = 6144
	quotaUserPolicySize             = 2048
	quotaGroupPolicySize            = 5120
	quotaRolePolicySize             = 10240
	quotaAssumeRolePolicySize       = 2048
	quotaGlobalEndpointTokenVersion = 1
	quotaPolicyVersionsInUse        = 10000
)

// awsManagedPolicyARNPrefix is the ARN prefix AWS-published managed policies
// carry; everything else is a customer-managed policy counted in the summary.
const awsManagedPolicyARNPrefix = "arn:aws:iam::aws:policy/"

// defaultMinimumPasswordLength is the length AWS assigns when
// UpdateAccountPasswordPolicy omits MinimumPasswordLength.
const defaultMinimumPasswordLength = 6

// boolToInt maps a presence flag to the 0/1 integer GetAccountSummary reports.
func boolToInt(b bool) int {
	if b {
		return 1
	}

	return 0
}

// AccountSummary returns the IAM entity-usage and quota map for the account.
func (m *Mock) AccountSummary(_ context.Context) (map[string]int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mfaTotal, mfaInUse := m.mfaCountsLocked()

	return map[string]int{
		"Users":                             len(m.users.All()),
		"UsersQuota":                        quotaUsers,
		"Groups":                            len(m.groups.All()),
		"GroupsQuota":                       quotaGroups,
		"Roles":                             len(m.roles.All()),
		"RolesQuota":                        quotaRoles,
		"Policies":                          m.customerManagedPolicyCountLocked(),
		"PoliciesQuota":                     quotaPolicies,
		"InstanceProfiles":                  len(m.instanceProfiles.All()),
		"InstanceProfilesQuota":             quotaInstanceProfiles,
		"ServerCertificates":                0,
		"ServerCertificatesQuota":           quotaServerCertificates,
		"MFADevices":                        mfaTotal,
		"MFADevicesInUse":                   mfaInUse,
		"AccountMFAEnabled":                 0,
		"AccountAccessKeysPresent":          boolToInt(len(m.accessKeys.All()) > 0),
		"AccountSigningCertificatesPresent": 0,
		"AccountPasswordPresent":            boolToInt(m.passwordPolicy != nil),
		"GroupsPerUserQuota":                quotaGroupsPerUser,
		"AccessKeysPerUserQuota":            maxAccessKeysPerUser,
		"SigningCertificatesPerUserQuota":   quotaSigningCertificatesPerUser,
		"AttachedPoliciesPerGroupQuota":     quotaAttachedPoliciesPerGroup,
		"AttachedPoliciesPerRoleQuota":      quotaAttachedPoliciesPerRole,
		"AttachedPoliciesPerUserQuota":      quotaAttachedPoliciesPerUser,
		"VersionsPerPolicyQuota":            quotaVersionsPerPolicy,
		"PolicySizeQuota":                   quotaPolicySize,
		"UserPolicySizeQuota":               quotaUserPolicySize,
		"GroupPolicySizeQuota":              quotaGroupPolicySize,
		"RolePolicySizeQuota":               quotaRolePolicySize,
		"AssumeRolePolicySizeQuota":         quotaAssumeRolePolicySize,
		"PolicyVersionsInUseQuota":          quotaPolicyVersionsInUse,
		"GlobalEndpointTokenVersion":        quotaGlobalEndpointTokenVersion,
	}, nil
}

// customerManagedPolicyCountLocked counts non-AWS-managed policies. Caller holds m.mu.
func (m *Mock) customerManagedPolicyCountLocked() int {
	count := 0

	for _, p := range m.policies.All() {
		if !strings.HasPrefix(p.ARN, awsManagedPolicyARNPrefix) {
			count++
		}
	}

	return count
}

// UpdateAccountPasswordPolicy creates or replaces the account password policy
// (IAM UpdateAccountPasswordPolicy — the single create-or-update operation).
func (m *Mock) UpdateAccountPasswordPolicy(_ context.Context, p driver.PasswordPolicy) error {
	if p.MinimumPasswordLength == 0 {
		p.MinimumPasswordLength = defaultMinimumPasswordLength
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cp := p
	m.passwordPolicy = &cp

	return nil
}

// GetAccountPasswordPolicy returns the account password policy, or NoSuchEntity
// when none has been set.
func (m *Mock) GetAccountPasswordPolicy(_ context.Context) (*driver.PasswordPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.passwordPolicy == nil {
		return nil, errors.Newf(errors.NotFound, "The account password policy cannot be found")
	}

	cp := *m.passwordPolicy

	return &cp, nil
}

// DeleteAccountPasswordPolicy removes the account password policy.
func (m *Mock) DeleteAccountPasswordPolicy(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.passwordPolicy == nil {
		return errors.Newf(errors.NotFound, "The account password policy cannot be found")
	}

	m.passwordPolicy = nil

	return nil
}
