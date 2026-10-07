package apprunner

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// CreateAutoScalingConfiguration provisions a new revision of a named auto
// scaling configuration. Reusing a name mints the next incremental revision and
// demotes the previous latest revision, matching real App Runner.
func (m *Mock) CreateAutoScalingConfiguration(
	_ context.Context, in *driver.CreateAutoScalingConfigurationInput,
) (*driver.AutoScalingConfiguration, error) {
	if in.AutoScalingConfigurationName == "" {
		return nil, invalidRequest("AutoScalingConfigurationName is required")
	}

	revision := m.nextAutoScalingRevision(in.AutoScalingConfigurationName)
	m.demoteAutoScalingLatest(in.AutoScalingConfigurationName)

	id := newID()
	arn := m.autoScalingARN(in.AutoScalingConfigurationName, revision, id)

	cfg := driver.AutoScalingConfiguration{
		AutoScalingConfigurationArn:      arn,
		AutoScalingConfigurationName:     in.AutoScalingConfigurationName,
		AutoScalingConfigurationRevision: revision,
		Latest:                           true,
		Status:                           driver.AutoScalingStatusActive,
		MaxConcurrency:                   int32Or(in.MaxConcurrency, defaultMaxConcurrency),
		MinSize:                          int32Or(in.MinSize, defaultMinSize),
		MaxSize:                          int32Or(in.MaxSize, defaultMaxSize),
		CreatedAt:                        m.now(),
		Tags:                             copyTags(in.Tags),
	}

	m.autoScaling.Set(arn, cfg)

	out := m.autoScalingView(&cfg)

	return &out, nil
}

// autoScalingView copies a stored configuration and fills HasAssociatedService,
// which is derived from the services that currently reference it.
func (m *Mock) autoScalingView(c *driver.AutoScalingConfiguration) driver.AutoScalingConfiguration {
	out := copyAutoScaling(c)
	out.HasAssociatedService = m.autoScalingInUse(c.AutoScalingConfigurationArn)

	return out
}

// autoScalingInUse reports whether any service is associated with the
// configuration.
func (m *Mock) autoScalingInUse(arn string) bool {
	svcs := m.services.SortedValues()
	for i := range svcs {
		if s := svcs[i].AutoScalingConfigurationSummary; s != nil && s.AutoScalingConfigurationArn == arn {
			return true
		}
	}

	return false
}

// int32Or returns *p when p is non-nil, else def.
func int32Or(p *int32, def int32) int32 {
	if p != nil {
		return *p
	}

	return def
}

// nextAutoScalingRevision returns one past the highest existing revision of name,
// or the first revision when the name is new.
func (m *Mock) nextAutoScalingRevision(name string) int32 {
	highest := int32(0)
	all := m.autoScaling.All()

	for arn := range all {
		c := all[arn]
		if c.AutoScalingConfigurationName == name && c.AutoScalingConfigurationRevision > highest {
			highest = c.AutoScalingConfigurationRevision
		}
	}

	return highest + 1
}

// demoteAutoScalingLatest clears the Latest flag on every stored revision of name.
func (m *Mock) demoteAutoScalingLatest(name string) {
	all := m.autoScaling.All()

	for arn := range all {
		c := all[arn]
		if c.AutoScalingConfigurationName == name && c.Latest {
			c.Latest = false
			m.autoScaling.Set(arn, c)
		}
	}
}

// DescribeAutoScalingConfiguration returns the configuration by ARN.
func (m *Mock) DescribeAutoScalingConfiguration(
	_ context.Context, arn string,
) (*driver.AutoScalingConfiguration, error) {
	cfg, ok := m.autoScaling.Get(arn)
	if !ok {
		return nil, notFound("auto scaling configuration %q does not exist", arn)
	}

	out := m.autoScalingView(&cfg)

	return &out, nil
}

// DeleteAutoScalingConfiguration removes a configuration and returns it with an
// INACTIVE status, so a subsequent describe 404s. The default configuration and
// a configuration that a service uses are rejected with an
// InvalidRequestException, as in real App Runner.
func (m *Mock) DeleteAutoScalingConfiguration(
	_ context.Context, arn string,
) (*driver.AutoScalingConfiguration, error) {
	m.refMu.Lock()
	defer m.refMu.Unlock()

	cfg, ok := m.autoScaling.Get(arn)
	if !ok {
		return nil, notFound("auto scaling configuration %q does not exist", arn)
	}

	if cfg.IsDefault {
		return nil, invalidRequest("the default auto scaling configuration can't be deleted")
	}

	if m.autoScalingInUse(arn) {
		return nil, invalidRequest("auto scaling configuration is used by one or more App Runner services")
	}

	m.autoScaling.Delete(arn)

	out := copyAutoScaling(&cfg)
	out.Status = driver.AutoScalingStatusInactive
	out.DeletedAt = m.now()

	return &out, nil
}

// ListAutoScalingConfigurations returns a page of configurations, optionally
// narrowed to a name and to only the latest revision of each name.
func (m *Mock) ListAutoScalingConfigurations(
	_ context.Context, name string, latestOnly bool, page driver.Page,
) ([]*driver.AutoScalingConfiguration, string, error) {
	stored := m.autoScaling.SortedValues()
	matched := make([]driver.AutoScalingConfiguration, 0, len(stored))

	for i := range stored {
		if name != "" && stored[i].AutoScalingConfigurationName != name {
			continue
		}

		if latestOnly && !stored[i].Latest {
			continue
		}

		matched = append(matched, stored[i])
	}

	return m.pageAutoScaling(matched, page)
}

// ListAutoScalingConfigurationRevisions returns every stored revision of a named
// configuration.
func (m *Mock) ListAutoScalingConfigurationRevisions(
	_ context.Context, name string, page driver.Page,
) ([]*driver.AutoScalingConfiguration, string, error) {
	stored := m.autoScaling.SortedValues()
	matched := make([]driver.AutoScalingConfiguration, 0, len(stored))

	for i := range stored {
		if name == "" || stored[i].AutoScalingConfigurationName == name {
			matched = append(matched, stored[i])
		}
	}

	return m.pageAutoScaling(matched, page)
}

// pageAutoScaling paginates a matched slice and returns alias-free copies.
func (m *Mock) pageAutoScaling(
	matched []driver.AutoScalingConfiguration, page driver.Page,
) ([]*driver.AutoScalingConfiguration, string, error) {
	start, end, next := paginate(len(matched), page)
	out := make([]*driver.AutoScalingConfiguration, 0, end-start)

	for i := start; i < end; i++ {
		c := m.autoScalingView(&matched[i])
		out = append(out, &c)
	}

	return out, next, nil
}
