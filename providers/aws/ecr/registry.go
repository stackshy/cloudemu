package ecr

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

const (
	scanTypeBasic    = "BASIC"
	scanTypeEnhanced = "ENHANCED"
)

// PutRegistryPolicy sets the registry-level permissions policy and echoes the
// owning registryId. AWS-specific; reached by the ECR wire handler via type
// assertion.
func (m *Mock) PutRegistryPolicy(_ context.Context, policyText string) (registryID, policy string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.registryPolicy = policyText

	return m.opts.AccountID, m.registryPolicy, nil
}

// GetRegistryPolicy returns the registry policy and owning registryId, or
// RegistryPolicyNotFoundException when none is set.
func (m *Mock) GetRegistryPolicy(_ context.Context) (registryID, policy string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.registryPolicy == "" {
		return "", "", apiErrf(excRegistryPolicyNotFound, "no registry policy set")
	}

	return m.opts.AccountID, m.registryPolicy, nil
}

// DeleteRegistryPolicy removes the registry policy and returns the policy that
// was deleted, or RegistryPolicyNotFoundException when none is set.
func (m *Mock) DeleteRegistryPolicy(_ context.Context) (registryID, policy string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.registryPolicy == "" {
		return "", "", apiErrf(excRegistryPolicyNotFound, "no registry policy set")
	}

	policy = m.registryPolicy
	m.registryPolicy = ""

	return m.opts.AccountID, policy, nil
}

// PutReplicationConfiguration replaces the registry's replication configuration
// and returns the stored configuration. AWS-specific.
func (m *Mock) PutReplicationConfiguration(
	_ context.Context, cfg driver.ReplicationConfiguration,
) (driver.ReplicationConfiguration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	stored := copyReplication(cfg)
	m.replication = &stored

	return copyReplication(stored), nil
}

// DescribeRegistry returns the owning registryId and the current replication
// configuration. When none is configured it returns an empty (non-nil) rule
// set, matching real ECR.
func (m *Mock) DescribeRegistry(_ context.Context) (registryID string, cfg driver.ReplicationConfiguration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.replication == nil {
		return m.opts.AccountID, driver.ReplicationConfiguration{Rules: []driver.ReplicationRule{}}, nil
	}

	return m.opts.AccountID, copyReplication(*m.replication), nil
}

// CreatePullThroughCacheRule adds a pull-through cache rule keyed by its
// repository prefix. A duplicate prefix is PullThroughCacheRuleAlreadyExists.
func (m *Mock) CreatePullThroughCacheRule(
	_ context.Context, rule *driver.PullThroughCacheRule,
) (driver.PullThroughCacheRule, error) {
	if rule.ECRRepositoryPrefix == "" {
		return driver.PullThroughCacheRule{}, errors.New(errors.InvalidArgument, "ecrRepositoryPrefix is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.pullThrough[rule.ECRRepositoryPrefix]; ok {
		return driver.PullThroughCacheRule{}, apiErrExistsf(excPullThroughRuleExists,
			"pull through cache rule for prefix %q already exists", rule.ECRRepositoryPrefix)
	}

	now := m.opts.Clock.Now().UTC().Format(time.RFC3339)
	stored := *rule
	stored.RegistryID = m.opts.AccountID
	stored.CreatedAt = now
	stored.UpdatedAt = now

	ruleCopy := stored
	m.pullThrough[rule.ECRRepositoryPrefix] = &ruleCopy

	return stored, nil
}

// UpdatePullThroughCacheRule updates the credential ARN of an existing rule.
// A missing prefix is PullThroughCacheRuleNotFound.
func (m *Mock) UpdatePullThroughCacheRule(
	_ context.Context, prefix, credentialARN string,
) (driver.PullThroughCacheRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rule, ok := m.pullThrough[prefix]
	if !ok {
		return driver.PullThroughCacheRule{}, apiErrf(excPullThroughRuleNotFound,
			"pull through cache rule for prefix %q not found", prefix)
	}

	rule.CredentialARN = credentialARN
	rule.UpdatedAt = m.opts.Clock.Now().UTC().Format(time.RFC3339)

	return *rule, nil
}

// DescribePullThroughCacheRules returns the rules whose prefix is in prefixes,
// or all rules when prefixes is empty, plus the owning registryId. A named
// prefix that does not exist is PullThroughCacheRuleNotFound.
func (m *Mock) DescribePullThroughCacheRules(
	_ context.Context, prefixes []string,
) (rules []driver.PullThroughCacheRule, registryID string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(prefixes) == 0 {
		return m.allPullThroughRules(), m.opts.AccountID, nil
	}

	rules = make([]driver.PullThroughCacheRule, 0, len(prefixes))

	for _, prefix := range prefixes {
		rule, ok := m.pullThrough[prefix]
		if !ok {
			return nil, "", apiErrf(excPullThroughRuleNotFound,
				"pull through cache rule for prefix %q not found", prefix)
		}

		rules = append(rules, *rule)
	}

	return rules, m.opts.AccountID, nil
}

// allPullThroughRules returns every rule sorted by prefix for a stable order.
func (m *Mock) allPullThroughRules() []driver.PullThroughCacheRule {
	rules := make([]driver.PullThroughCacheRule, 0, len(m.pullThrough))
	for _, rule := range m.pullThrough {
		rules = append(rules, *rule)
	}

	sortPullThrough(rules)

	return rules
}

// DeletePullThroughCacheRule removes a rule and returns the deleted rule, or
// PullThroughCacheRuleNotFound when the prefix has no rule.
func (m *Mock) DeletePullThroughCacheRule(_ context.Context, prefix string) (driver.PullThroughCacheRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rule, ok := m.pullThrough[prefix]
	if !ok {
		return driver.PullThroughCacheRule{}, apiErrf(excPullThroughRuleNotFound,
			"pull through cache rule for prefix %q not found", prefix)
	}

	deleted := *rule

	delete(m.pullThrough, prefix)

	return deleted, nil
}

// PutRegistryScanningConfiguration replaces the registry scanning configuration
// and returns the stored configuration plus the owning registryId. An unset or
// empty scanType defaults to BASIC.
func (m *Mock) PutRegistryScanningConfiguration(
	_ context.Context, cfg driver.RegistryScanningConfiguration,
) (stored driver.RegistryScanningConfiguration, registryID string, err error) {
	if cfg.ScanType == "" {
		cfg.ScanType = scanTypeBasic
	}

	if cfg.ScanType != scanTypeBasic && cfg.ScanType != scanTypeEnhanced {
		return driver.RegistryScanningConfiguration{}, "", errors.Newf(errors.InvalidArgument,
			"invalid scanType %q; expected BASIC or ENHANCED", cfg.ScanType)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	saved := copyScanConfig(cfg)
	m.registryScan = &saved

	return copyScanConfig(saved), m.opts.AccountID, nil
}

// GetRegistryScanningConfiguration returns the owning registryId and the
// scanning configuration. When none is configured it returns the ECR default
// (BASIC, no rules).
func (m *Mock) GetRegistryScanningConfiguration(
	_ context.Context,
) (registryID string, cfg driver.RegistryScanningConfiguration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.registryScan == nil {
		return m.opts.AccountID, driver.RegistryScanningConfiguration{
			ScanType: scanTypeBasic, Rules: []driver.RegistryScanningRule{},
		}, nil
	}

	return m.opts.AccountID, copyScanConfig(*m.registryScan), nil
}

// PutAccountSetting stores a registry account-level setting (e.g.
// BASIC_SCAN_TYPE_VERSION, REGISTRY_POLICY_SCOPE) and echoes it back.
func (m *Mock) PutAccountSetting(_ context.Context, name, value string) (settingName, settingValue string, err error) {
	if name == "" {
		return "", "", errors.New(errors.InvalidArgument, "name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.accountSettings[name] = value

	return name, value, nil
}

// GetAccountSetting returns a registry account-level setting. An unset name
// reads back with an empty value, matching real ECR's default behavior.
func (m *Mock) GetAccountSetting(_ context.Context, name string) (settingName, settingValue string, err error) {
	if name == "" {
		return "", "", errors.New(errors.InvalidArgument, "name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return name, m.accountSettings[name], nil
}
