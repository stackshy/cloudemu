package route53resolver

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

const (
	autodefinedReverseEnabled  = "ENABLED"
	autodefinedReverseDisabled = "DISABLED"
	autodefinedReverseLocal    = "USE_LOCAL_RESOURCE_SETTING"

	dnssecStatusEnabled  = "ENABLED"
	dnssecStatusDisabled = "DISABLED"
	dnssecStatusLocal    = "USE_LOCAL_RESOURCE_SETTING"

	flagEnable = "ENABLE"
	flagLocal  = "USE_LOCAL_RESOURCE_SETTING"
)

func cloneResolverConfig(c *driver.ResolverConfig) driver.ResolverConfig { return *c }

func cloneDnssecConfig(c *driver.ResolverDnssecConfig) driver.ResolverDnssecConfig { return *c }

// autodefinedReverseFor maps a request flag to a stored autodefined-reverse
// status. Autodefined reverse-DNS rules are enabled by default in AWS.
func autodefinedReverseFor(flag string) string {
	switch flag {
	case flagEnable:
		return autodefinedReverseEnabled
	case flagLocal:
		return autodefinedReverseLocal
	default:
		return autodefinedReverseDisabled
	}
}

// dnssecStatusFor maps a request validation value to a stored DNSSEC status.
// DNSSEC validation is disabled by default in AWS.
func dnssecStatusFor(validation string) string {
	switch validation {
	case flagEnable:
		return dnssecStatusEnabled
	case flagLocal:
		return dnssecStatusLocal
	default:
		return dnssecStatusDisabled
	}
}

// resolverConfigFor returns the stored config for a VPC, materializing a
// default (autodefined reverse enabled) one on first access. Caller holds m.mu.
func (m *Mock) resolverConfigFor(resourceID string) *driver.ResolverConfig {
	if c, ok := m.rslvrConfigs.Get(resourceID); ok {
		return c
	}

	c := &driver.ResolverConfig{
		ID:                 idgen.GenerateID("rslvr-rc-"),
		OwnerID:            m.opts.AccountID,
		ResourceID:         resourceID,
		AutodefinedReverse: autodefinedReverseEnabled,
	}
	m.rslvrConfigs.Set(resourceID, c)

	return c
}

// dnssecConfigFor returns the stored DNSSEC config for a VPC, materializing a
// default (validation disabled) one on first access. Caller holds m.mu.
func (m *Mock) dnssecConfigFor(resourceID string) *driver.ResolverDnssecConfig {
	if c, ok := m.dnssecCfgs.Get(resourceID); ok {
		return c
	}

	c := &driver.ResolverDnssecConfig{
		ID:               idgen.GenerateID("rslvr-ds-"),
		OwnerID:          m.opts.AccountID,
		ResourceID:       resourceID,
		ValidationStatus: dnssecStatusDisabled,
	}
	m.dnssecCfgs.Set(resourceID, c)

	return c
}

func (m *Mock) GetResolverConfig(_ context.Context, resourceID string) (*driver.ResolverConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := cloneResolverConfig(m.resolverConfigFor(resourceID))

	return &out, nil
}

func (m *Mock) UpdateResolverConfig(
	_ context.Context, resourceID, autodefinedReverseFlag string,
) (*driver.ResolverConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c := m.resolverConfigFor(resourceID)
	c.AutodefinedReverse = autodefinedReverseFor(autodefinedReverseFlag)

	out := cloneResolverConfig(c)

	return &out, nil
}

func (m *Mock) ListResolverConfigs(_ context.Context) ([]driver.ResolverConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.rslvrConfigs.All(), cloneResolverConfig), nil
}

func (m *Mock) GetResolverDnssecConfig(
	_ context.Context, resourceID string,
) (*driver.ResolverDnssecConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := cloneDnssecConfig(m.dnssecConfigFor(resourceID))

	return &out, nil
}

func (m *Mock) UpdateResolverDnssecConfig(
	_ context.Context, resourceID, validation string,
) (*driver.ResolverDnssecConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c := m.dnssecConfigFor(resourceID)
	c.ValidationStatus = dnssecStatusFor(validation)

	out := cloneDnssecConfig(c)

	return &out, nil
}

func (m *Mock) ListResolverDnssecConfigs(_ context.Context) ([]driver.ResolverDnssecConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.dnssecCfgs.All(), cloneDnssecConfig), nil
}
