package apprunner

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// CreateObservabilityConfiguration provisions a new revision of a named
// observability configuration. Reusing a name mints the next incremental
// revision and demotes the previous latest revision.
func (m *Mock) CreateObservabilityConfiguration(
	_ context.Context, in *driver.CreateObservabilityConfigurationInput,
) (*driver.ObservabilityConfiguration, error) {
	if err := validateConfigName("ObservabilityConfigurationName", in.ObservabilityConfigurationName); err != nil {
		return nil, err
	}

	if t := in.TraceConfiguration; t != nil && t.Vendor != "AWSXRAY" {
		return nil, invalidRequest("TraceConfiguration.Vendor must be AWSXRAY")
	}

	m.refMu.Lock()
	defer m.refMu.Unlock()

	revision := m.nextObservabilityRevision(in.ObservabilityConfigurationName)
	m.demoteObservabilityLatest(in.ObservabilityConfigurationName)

	id := newID()
	arn := m.observabilityARN(in.ObservabilityConfigurationName, revision, id)

	cfg := driver.ObservabilityConfiguration{
		ObservabilityConfigurationArn:      arn,
		ObservabilityConfigurationName:     in.ObservabilityConfigurationName,
		ObservabilityConfigurationRevision: revision,
		Latest:                             true,
		Status:                             driver.ResourceStatusActive,
		TraceConfiguration:                 copyTraceConfig(in.TraceConfiguration),
		CreatedAt:                          m.now(),
		Tags:                               copyTags(in.Tags),
	}

	m.observability.Set(arn, cfg)

	out := copyObservability(&cfg)

	return &out, nil
}

func copyTraceConfig(in *driver.TraceConfiguration) *driver.TraceConfiguration {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

// nextObservabilityRevision returns one past the highest existing revision of name.
func (m *Mock) nextObservabilityRevision(name string) int32 {
	highest := int32(0)
	all := m.observability.All()

	for arn := range all {
		c := all[arn]
		if c.ObservabilityConfigurationName == name && c.ObservabilityConfigurationRevision > highest {
			highest = c.ObservabilityConfigurationRevision
		}
	}

	return m.nextRevision("observability", name, highest)
}

// demoteObservabilityLatest clears the Latest flag on every stored revision of name.
func (m *Mock) demoteObservabilityLatest(name string) {
	all := m.observability.All()

	for arn := range all {
		c := all[arn]
		if c.ObservabilityConfigurationName == name && c.Latest {
			c.Latest = false
			m.observability.Set(arn, c)
		}
	}
}

// observabilityInUse reports whether any service references the configuration.
func (m *Mock) observabilityInUse(arn string) bool {
	svcs := m.services.SortedValues()
	for i := range svcs {
		if o := svcs[i].ObservabilityConfiguration; o != nil && o.ObservabilityConfigurationArn == arn {
			return true
		}
	}

	return false
}

func (m *Mock) DescribeObservabilityConfiguration(
	_ context.Context, arn string,
) (*driver.ObservabilityConfiguration, error) {
	cfg, ok := m.findObservability(arn)
	if !ok {
		return nil, notFound("observability configuration %q does not exist", arn)
	}

	out := copyObservability(&cfg)

	return &out, nil
}

// findObservability resolves a full ARN, or a partial one: ".../name" is the
// latest revision and ".../name/revision" pins one.
func (m *Mock) findObservability(arn string) (driver.ObservabilityConfiguration, bool) {
	if cfg, ok := m.observability.Get(arn); ok {
		return cfg, true
	}

	name, revision := autoScalingRefFromARN(arn)
	if name == "" {
		return driver.ObservabilityConfiguration{}, false
	}

	all := m.observability.SortedValues()
	for i := range all {
		c := &all[i]
		if c.ObservabilityConfigurationName != name {
			continue
		}

		if (revision == 0 && c.Latest) || (revision != 0 && c.ObservabilityConfigurationRevision == revision) {
			return *c, true
		}
	}

	return driver.ObservabilityConfiguration{}, false
}

func (m *Mock) DeleteObservabilityConfiguration(
	_ context.Context, arn string,
) (*driver.ObservabilityConfiguration, error) {
	m.refMu.Lock()
	defer m.refMu.Unlock()

	cfg, ok := m.findObservability(arn)
	if !ok {
		return nil, notFound("observability configuration %q does not exist", arn)
	}

	if m.observabilityInUse(cfg.ObservabilityConfigurationArn) {
		return nil, invalidRequest("observability configuration is used by one or more App Runner services")
	}

	m.observability.Delete(cfg.ObservabilityConfigurationArn)
	m.promoteObservabilityLatest(cfg.ObservabilityConfigurationName)

	out := copyObservability(&cfg)
	out.Status = driver.ResourceStatusInactive
	out.DeletedAt = m.now()

	return &out, nil
}

// promoteObservabilityLatest makes the highest remaining revision of a name the
// latest one again after a delete.
func (m *Mock) promoteObservabilityLatest(name string) {
	var best *driver.ObservabilityConfiguration

	all := m.observability.SortedValues()
	for i := range all {
		if all[i].ObservabilityConfigurationName != name {
			continue
		}

		if best == nil || all[i].ObservabilityConfigurationRevision > best.ObservabilityConfigurationRevision {
			best = &all[i]
		}
	}

	if best != nil {
		cp := *best
		cp.Latest = true
		m.observability.Set(cp.ObservabilityConfigurationArn, cp)
	}
}

// ListObservabilityConfigurations returns a page of configurations, optionally
// narrowed to a name and to only the latest revision of each name.
func (m *Mock) ListObservabilityConfigurations(
	_ context.Context, name string, latestOnly bool, page driver.Page,
) ([]*driver.ObservabilityConfiguration, string, error) {
	if err := validatePage(page); err != nil {
		return nil, "", err
	}

	stored := m.observability.SortedValues()
	matched := make([]driver.ObservabilityConfiguration, 0, len(stored))

	for i := range stored {
		if name != "" && stored[i].ObservabilityConfigurationName != name {
			continue
		}

		if latestOnly && !stored[i].Latest {
			continue
		}

		matched = append(matched, stored[i])
	}

	return pageObservability(matched, page)
}

// pageObservability paginates a matched slice and returns alias-free copies.
func pageObservability(
	matched []driver.ObservabilityConfiguration, page driver.Page,
) ([]*driver.ObservabilityConfiguration, string, error) {
	start, end, next := paginate(len(matched), page)
	out := make([]*driver.ObservabilityConfiguration, 0, end-start)

	for i := start; i < end; i++ {
		c := copyObservability(&matched[i])
		out = append(out, &c)
	}

	return out, next, nil
}
