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
	if in.ObservabilityConfigurationName == "" {
		return nil, invalidRequest("ObservabilityConfigurationName is required")
	}

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

	return highest + 1
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

// DescribeObservabilityConfiguration returns the configuration by ARN.
func (m *Mock) DescribeObservabilityConfiguration(
	_ context.Context, arn string,
) (*driver.ObservabilityConfiguration, error) {
	cfg, ok := m.observability.Get(arn)
	if !ok {
		return nil, notFound("observability configuration %q does not exist", arn)
	}

	out := copyObservability(&cfg)

	return &out, nil
}

// DeleteObservabilityConfiguration removes a configuration and returns it with an
// INACTIVE status, so a subsequent describe 404s.
func (m *Mock) DeleteObservabilityConfiguration(
	_ context.Context, arn string,
) (*driver.ObservabilityConfiguration, error) {
	cfg, ok := m.observability.Get(arn)
	if !ok {
		return nil, notFound("observability configuration %q does not exist", arn)
	}

	m.observability.Delete(arn)

	out := copyObservability(&cfg)
	out.Status = driver.ResourceStatusInactive
	out.DeletedAt = m.now()

	return &out, nil
}

// ListObservabilityConfigurations returns a page of configurations, optionally
// narrowed to a name and to only the latest revision of each name.
func (m *Mock) ListObservabilityConfigurations(
	_ context.Context, name string, latestOnly bool, page driver.Page,
) ([]*driver.ObservabilityConfiguration, string, error) {
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

// ListObservabilityConfigurationRevisions returns every stored revision of a named
// configuration.
func (m *Mock) ListObservabilityConfigurationRevisions(
	_ context.Context, name string, page driver.Page,
) ([]*driver.ObservabilityConfiguration, string, error) {
	stored := m.observability.SortedValues()
	matched := make([]driver.ObservabilityConfiguration, 0, len(stored))

	for i := range stored {
		if name == "" || stored[i].ObservabilityConfigurationName == name {
			matched = append(matched, stored[i])
		}
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
