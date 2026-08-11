package apprunner

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// reservedObsName is the App Runner-managed default name that can't be created.
const reservedObsName = "DefaultConfiguration"

// CreateObservabilityConfiguration mints a new revision, mirroring the ASC
// revision model: revisions sharing a Name increment atomically under obsMu and
// the newest becomes Latest (the prior Latest is cleared).
func (m *Mock) CreateObservabilityConfiguration(
	_ context.Context, name string, trace json.RawMessage, tags map[string]string,
) (*driver.ObservabilityConfiguration, error) {
	if name == "" {
		return nil, invalidRequest("ObservabilityConfigurationName is required")
	}

	if name == reservedObsName {
		return nil, invalidRequest("ObservabilityConfigurationName %q is reserved", name)
	}

	m.obsMu.Lock()
	defer m.obsMu.Unlock()

	for _, c := range m.obsByArn {
		if c.Name == name && c.Latest {
			c.Latest = false
		}
	}

	revision := m.obsLatestRev[name] + 1
	m.obsLatestRev[name] = revision

	cfg := &driver.ObservabilityConfiguration{
		Arn: m.obsArn(name, revision), Name: name, Revision: revision,
		Status: driver.StatusActive, Latest: true,
		TraceConfiguration: copyRaw(trace), CreatedAt: m.now(), Tags: copyTags(tags),
	}
	m.obsByArn[cfg.Arn] = cfg

	return copyObs(cfg), nil
}

func (m *Mock) DescribeObservabilityConfiguration(
	_ context.Context, arn string,
) (*driver.ObservabilityConfiguration, error) {
	m.obsMu.Lock()
	defer m.obsMu.Unlock()

	cfg, ok := m.obsByArn[arn]
	if !ok {
		return nil, notFound("no observability configuration found for ARN %q", arn)
	}

	return copyObs(cfg), nil
}

// DeleteObservabilityConfiguration marks a revision INACTIVE and stamps its
// deletion time.
func (m *Mock) DeleteObservabilityConfiguration(
	_ context.Context, arn string,
) (*driver.ObservabilityConfiguration, error) {
	m.obsMu.Lock()
	defer m.obsMu.Unlock()

	cfg, ok := m.obsByArn[arn]
	if !ok {
		return nil, notFound("no observability configuration found for ARN %q", arn)
	}

	deactivateObs(cfg, m.now())

	return copyObs(cfg), nil
}

func deactivateObs(c *driver.ObservabilityConfiguration, now time.Time) {
	c.Status = driver.StatusInactive
	c.Latest = false

	if c.DeletedAt.IsZero() {
		c.DeletedAt = now
	}
}

// ListObservabilityConfigurations lists ACTIVE revisions, optionally filtered by
// name and to the latest revision per name, paginated by ARN.
//
//nolint:dupl // near-identical to ListAutoScalingConfigurations by API shape (both revision-managed).
func (m *Mock) ListObservabilityConfigurations(
	_ context.Context, name string, latestOnly bool, nextToken string, maxResults int32,
) ([]driver.ObservabilityConfiguration, string, error) {
	m.obsMu.Lock()
	all := make([]driver.ObservabilityConfiguration, 0, len(m.obsByArn))

	for _, c := range m.obsByArn {
		if name != "" && c.Name != name {
			continue
		}

		if latestOnly && !c.Latest {
			continue
		}

		all = append(all, *copyObs(c))
	}
	m.obsMu.Unlock()

	sort.Slice(all, func(i, j int) bool { return all[i].Arn < all[j].Arn })

	return paginate(all, nextToken, maxResults, func(c driver.ObservabilityConfiguration) string { return c.Arn })
}

// copyObs deep-copies an observability configuration, including its trace
// configuration bytes.
func copyObs(c *driver.ObservabilityConfiguration) *driver.ObservabilityConfiguration {
	out := *c
	out.TraceConfiguration = copyRaw(c.TraceConfiguration)
	out.Tags = copyTags(c.Tags)

	return &out
}
