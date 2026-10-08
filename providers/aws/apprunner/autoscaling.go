package apprunner

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// CreateAutoScalingConfiguration provisions a new revision of a named auto
// scaling configuration. Reusing a name mints the next incremental revision and
// demotes the previous latest revision, matching real App Runner. MaxConcurrency
// is 1-200, MinSize 1-25 and MinSize may not exceed MaxSize.
func (m *Mock) CreateAutoScalingConfiguration(
	_ context.Context, in *driver.CreateAutoScalingConfigurationInput,
) (*driver.AutoScalingConfiguration, error) {
	if err := validateConfigName("AutoScalingConfigurationName", in.AutoScalingConfigurationName); err != nil {
		return nil, err
	}

	maxConcurrency := int32Or(in.MaxConcurrency, defaultMaxConcurrency)
	minSize := int32Or(in.MinSize, defaultMinSize)
	maxSize := int32Or(in.MaxSize, defaultMaxSize)

	if err := validateScaling(maxConcurrency, minSize, maxSize); err != nil {
		return nil, err
	}

	m.refMu.Lock()
	defer m.refMu.Unlock()

	revision := m.nextAutoScalingRevision(in.AutoScalingConfigurationName)
	m.demoteAutoScalingLatest(in.AutoScalingConfigurationName)

	arn := m.autoScalingARN(in.AutoScalingConfigurationName, revision, newID())

	cfg := driver.AutoScalingConfiguration{
		AutoScalingConfigurationArn:      arn,
		AutoScalingConfigurationName:     in.AutoScalingConfigurationName,
		AutoScalingConfigurationRevision: revision,
		Latest:                           true,
		Status:                           driver.AutoScalingStatusActive,
		MaxConcurrency:                   maxConcurrency,
		MinSize:                          minSize,
		MaxSize:                          maxSize,
		CreatedAt:                        m.now(),
		Tags:                             copyTags(in.Tags),
	}

	m.autoScaling.Set(arn, cfg)

	out := m.autoScalingView(&cfg)

	return &out, nil
}

// validateScaling applies the documented auto scaling ranges.
func validateScaling(maxConcurrency, minSize, maxSize int32) error {
	switch {
	case maxConcurrency < minMaxConcurrency || maxConcurrency > maxMaxConcurrency:
		return invalidRequest("MaxConcurrency must be between 1 and 200")
	case minSize < 1 || minSize > maxMinSize:
		return invalidRequest("MinSize must be between 1 and 25")
	case maxSize < 1:
		return invalidRequest("MaxSize must be at least 1")
	case minSize > maxSize:
		return invalidRequest("MinSize must not be greater than MaxSize")
	}

	return nil
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

	return m.nextRevision("autoscaling", name, highest)
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

// resolveAutoScaling finds the configuration an ARN names. An empty ARN is the
// account's default; an ARN may be full, or partial (".../name" for the latest
// active revision, ".../name/revision"). An unknown or inactive configuration is
// an InvalidRequestException.
func (m *Mock) resolveAutoScaling(arn string) (driver.AutoScalingConfiguration, error) {
	if arn == "" {
		arn = m.defaultAutoScalingARN()
	}

	if cfg, ok := m.autoScaling.Get(arn); ok {
		return cfg, nil
	}

	if c, ok := m.findActiveByPartialARN(arn); ok {
		return c, nil
	}

	return driver.AutoScalingConfiguration{}, invalidRequest("auto scaling configuration " + arn + " does not exist")
}

// findActiveByPartialARN matches ".../name" (latest) or ".../name/revision" to an
// active configuration.
func (m *Mock) findActiveByPartialARN(arn string) (driver.AutoScalingConfiguration, bool) {
	name, revision := autoScalingRefFromARN(arn)
	if name == "" {
		return driver.AutoScalingConfiguration{}, false
	}

	all := m.autoScaling.SortedValues()
	for i := range all {
		c := all[i]
		if c.AutoScalingConfigurationName != name || c.Status != driver.AutoScalingStatusActive {
			continue
		}

		if (revision == 0 && c.Latest) || (revision != 0 && c.AutoScalingConfigurationRevision == revision) {
			return c, true
		}
	}

	return driver.AutoScalingConfiguration{}, false
}

// DescribeAutoScalingConfiguration returns the configuration by ARN (full, or
// partial name / name/revision).
func (m *Mock) DescribeAutoScalingConfiguration(
	_ context.Context, arn string,
) (*driver.AutoScalingConfiguration, error) {
	cfg, ok := m.findAutoScaling(arn)
	if !ok {
		return nil, notFound("auto scaling configuration %q does not exist", arn)
	}

	out := m.autoScalingView(&cfg)

	return &out, nil
}

// findAutoScaling resolves a full or partial ARN to a stored configuration; a
// miss is not an error so each operation can word its own exception.
func (m *Mock) findAutoScaling(arn string) (driver.AutoScalingConfiguration, bool) {
	cfg, err := m.resolveAutoScaling(arn)
	if err != nil || arn == "" {
		return driver.AutoScalingConfiguration{}, false
	}

	return cfg, true
}

func (m *Mock) DeleteAutoScalingConfiguration(
	_ context.Context, arn string, deleteAllRevisions bool,
) (*driver.AutoScalingConfiguration, error) {
	m.refMu.Lock()
	defer m.refMu.Unlock()

	cfg, ok := m.findAutoScaling(arn)
	if !ok {
		return nil, notFound("auto scaling configuration %q does not exist", arn)
	}

	targets := []driver.AutoScalingConfiguration{cfg}

	if deleteAllRevisions {
		if _, revision := autoScalingRefFromARN(arn); revision != 0 {
			return nil, invalidRequest("DeleteAllRevisions needs an ARN without a revision (.../name)")
		}

		targets = m.activeRevisionsOf(cfg.AutoScalingConfigurationName)
	}

	for i := range targets {
		if targets[i].IsDefault {
			return nil, invalidRequest("the default auto scaling configuration can't be deleted")
		}

		if m.autoScalingInUse(targets[i].AutoScalingConfigurationArn) {
			return nil, invalidRequest("auto scaling configuration is used by one or more App Runner services")
		}
	}

	for i := range targets {
		m.autoScaling.Delete(targets[i].AutoScalingConfigurationArn)
	}

	m.promoteAutoScalingLatest(cfg.AutoScalingConfigurationName)

	out := copyAutoScaling(&cfg)
	out.Status = driver.AutoScalingStatusInactive
	out.DeletedAt = m.now()

	return &out, nil
}

// activeRevisionsOf returns every stored revision of a configuration name.
func (m *Mock) activeRevisionsOf(name string) []driver.AutoScalingConfiguration {
	var out []driver.AutoScalingConfiguration

	all := m.autoScaling.SortedValues()
	for i := range all {
		if all[i].AutoScalingConfigurationName == name {
			out = append(out, all[i])
		}
	}

	return out
}

// promoteAutoScalingLatest makes the highest remaining revision of a name the
// latest one again after a delete (a name-only ARN means the latest active revision).
func (m *Mock) promoteAutoScalingLatest(name string) {
	var best *driver.AutoScalingConfiguration

	revs := m.activeRevisionsOf(name)
	for i := range revs {
		if best == nil || revs[i].AutoScalingConfigurationRevision > best.AutoScalingConfigurationRevision {
			best = &revs[i]
		}
	}

	if best == nil {
		return
	}

	cp := *best
	cp.Latest = true
	m.autoScaling.Set(cp.AutoScalingConfigurationArn, cp)
}

// ListAutoScalingConfigurations returns a page of active configurations,
// optionally narrowed to a name and to only the latest revision of each name.
func (m *Mock) ListAutoScalingConfigurations(
	_ context.Context, name string, latestOnly bool, page driver.Page,
) ([]*driver.AutoScalingConfiguration, string, error) {
	if err := validatePage(page); err != nil {
		return nil, "", err
	}

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

// UpdateDefaultAutoScalingConfiguration makes a configuration (full or partial
// ARN) the account's default; the previous default stops being the default and
// services created without an explicit configuration use the new one.
func (m *Mock) UpdateDefaultAutoScalingConfiguration(
	_ context.Context, arn string,
) (*driver.AutoScalingConfiguration, error) {
	if arn == "" {
		return nil, invalidRequest("AutoScalingConfigurationArn is required")
	}

	m.refMu.Lock()
	defer m.refMu.Unlock()

	target, ok := m.findAutoScaling(arn)
	if !ok {
		return nil, notFound("auto scaling configuration %q does not exist", arn)
	}

	for _, key := range m.autoScaling.Keys() {
		m.autoScaling.Update(key, func(c driver.AutoScalingConfiguration) driver.AutoScalingConfiguration {
			c.IsDefault = c.AutoScalingConfigurationArn == target.AutoScalingConfigurationArn

			return c
		})
	}

	updated, _ := m.autoScaling.Get(target.AutoScalingConfigurationArn)
	out := m.autoScalingView(&updated)

	return &out, nil
}

// ListServicesForAutoScalingConfiguration returns the ARNs of the services that
// use a configuration, ordered by ARN.
func (m *Mock) ListServicesForAutoScalingConfiguration(
	_ context.Context, arn string, page driver.Page,
) (serviceArns []string, nextToken string, err error) {
	if err := validatePage(page); err != nil {
		return nil, "", err
	}

	cfg, ok := m.findAutoScaling(arn)
	if !ok {
		return nil, "", notFound("auto scaling configuration %q does not exist", arn)
	}

	matched := []string{}

	svcs := m.services.SortedValues()
	for i := range svcs {
		if s := svcs[i].AutoScalingConfigurationSummary; s != nil && s.AutoScalingConfigurationArn == cfg.AutoScalingConfigurationArn {
			matched = append(matched, svcs[i].ServiceArn)
		}
	}

	start, end, next := paginate(len(matched), page)

	return matched[start:end], next, nil
}
