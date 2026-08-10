package kinesis

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

const (
	defaultShardLimit        = 500
	onDemandStreamCountLimit = 50
)

// PutResourcePolicy attaches a resource policy to a stream or consumer ARN.
func (m *Mock) PutResourcePolicy(_ context.Context, resourceARN, policy string) error {
	if policy == "" {
		return invalidArg("Policy is required")
	}

	sd, err := m.resolve("", resourceARN)
	if err != nil {
		return err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.policy = policy

	return nil
}

// GetResourcePolicy returns a stream's resource policy.
func (m *Mock) GetResourcePolicy(_ context.Context, resourceARN string) (string, error) {
	sd, err := m.resolve("", resourceARN)
	if err != nil {
		return "", err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if sd.policy == "" {
		return "", errNotFound("no resource policy attached to %q", resourceARN)
	}

	return sd.policy, nil
}

// DeleteResourcePolicy removes a stream's resource policy.
func (m *Mock) DeleteResourcePolicy(_ context.Context, resourceARN string) error {
	sd, err := m.resolve("", resourceARN)
	if err != nil {
		return err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.policy = ""

	return nil
}

// DescribeLimits reports account shard limits and current usage.
func (m *Mock) DescribeLimits(_ context.Context) (*driver.Limits, error) {
	var open, onDemand int32

	for _, sd := range m.streams.All() {
		sd.mu.RLock()
		open += openShardCount(sd.shards)

		if sd.desc.StreamModeDetails == driver.ModeOnDemand {
			onDemand++
		}
		sd.mu.RUnlock()
	}

	return &driver.Limits{
		ShardLimit:               defaultShardLimit,
		OpenShardCount:           open,
		OnDemandStreamCount:      onDemand,
		OnDemandStreamCountLimit: onDemandStreamCountLimit,
	}, nil
}

// DescribeAccountSettings returns the account-level settings.
func (m *Mock) DescribeAccountSettings(_ context.Context) (*driver.AccountSettings, error) {
	m.settingsMu.RLock()
	defer m.settingsMu.RUnlock()

	s := m.settings

	return &s, nil
}

// UpdateAccountSettings updates the account-level settings.
//
//nolint:gocritic // in is the public UpdateAccountSettings input, taken by value to match the driver API
func (m *Mock) UpdateAccountSettings(_ context.Context, in driver.AccountSettings) (*driver.AccountSettings, error) {
	m.settingsMu.Lock()
	defer m.settingsMu.Unlock()

	if in.CommitmentStatus != "" {
		m.settings.CommitmentStatus = in.CommitmentStatus
	}

	if in.CommitmentStatus == "ENABLED" && m.settings.StartedAt.IsZero() {
		m.settings.StartedAt = m.now()
	}

	s := m.settings

	return &s, nil
}
