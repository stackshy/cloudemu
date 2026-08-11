package apprunner

import (
	"context"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

const (
	defaultASCMaxConcurrency = 100
	defaultASCMaxSize        = 25
	defaultASCMinSize        = 1
	// defaultASCName is the App Runner-managed default configuration name.
	defaultASCName = "DefaultConfiguration"
)

// ascInUse returns the ARN of a service that references the given auto-scaling
// configuration, or "". When specificArn is set only that exact revision counts;
// otherwise any revision sharing name counts (for a delete-all-revisions). The
// caller must hold ascMu.
func (m *Mock) ascInUse(name, specificArn string) string {
	for _, sd := range m.services.SortedValues() {
		sd.mu.RLock()
		ref := sd.svc.AutoScalingConfigArn
		svcArn := sd.svc.ServiceArn
		sd.mu.RUnlock()

		if ref == "" {
			continue
		}

		if specificArn != "" {
			if ref == specificArn {
				return svcArn
			}

			continue
		}

		if c, ok := m.ascByArn[ref]; ok && c.Name == name {
			return svcArn
		}
	}

	return ""
}

// copyASC returns a deep copy of a stored auto-scaling configuration so a reader
// cannot alias its Tags map.
func copyASC(c *driver.AutoScalingConfiguration) *driver.AutoScalingConfiguration {
	out := *c
	out.Tags = copyTags(c.Tags)

	return &out
}

// CreateAutoScalingConfiguration mints a new revision. Revisions sharing a Name
// increment atomically under ascMu, and the new revision becomes the Latest for
// its name (the prior Latest is cleared). This mirrors App Runner's revision
// model where multiple revisions share a name and exactly one is Latest.
func (m *Mock) CreateAutoScalingConfiguration(
	_ context.Context, name string, maxConcurrency, maxSize, minSize int32,
) (*driver.AutoScalingConfiguration, error) {
	if name == "" {
		return nil, invalidRequest("AutoScalingConfigurationName is required")
	}

	if minSize > 0 && maxSize > 0 && minSize > maxSize {
		return nil, invalidRequest("MinSize %d must not exceed MaxSize %d", minSize, maxSize)
	}

	m.ascMu.Lock()
	defer m.ascMu.Unlock()

	// Clear the prior Latest for this name; the newest revision becomes Latest.
	for _, c := range m.ascByArn {
		if c.Name == name && c.Latest {
			c.Latest = false
		}
	}

	revision := m.ascLatestRev[name] + 1
	m.ascLatestRev[name] = revision

	cfg := &driver.AutoScalingConfiguration{
		Arn: m.ascArn(name, revision), Name: name, Revision: revision, Status: driver.StatusActive,
		MaxConcurrency: orDefault(maxConcurrency, defaultASCMaxConcurrency),
		MaxSize:        orDefault(maxSize, defaultASCMaxSize),
		MinSize:        orDefault(minSize, defaultASCMinSize),
		Latest:         true, CreatedAt: m.now(),
	}
	m.ascByArn[cfg.Arn] = cfg

	return copyASC(cfg), nil
}

func orDefault(v, def int32) int32 {
	if v <= 0 {
		return def
	}

	return v
}

func (m *Mock) DescribeAutoScalingConfiguration(
	_ context.Context, arn string,
) (*driver.AutoScalingConfiguration, error) {
	m.ascMu.Lock()
	defer m.ascMu.Unlock()

	cfg, ok := m.ascByArn[arn]
	if !ok {
		return nil, notFound("no auto scaling configuration found for ARN %q", arn)
	}

	return copyASC(cfg), nil
}

// DeleteAutoScalingConfiguration marks a revision INACTIVE (or, when
// deleteAllRevisions is set, every revision sharing its name). It returns the
// (highest) affected revision.
func (m *Mock) DeleteAutoScalingConfiguration(
	_ context.Context, arn string, deleteAllRevisions bool,
) (*driver.AutoScalingConfiguration, error) {
	m.ascMu.Lock()
	defer m.ascMu.Unlock()

	cfg, ok := m.ascByArn[arn]
	if !ok {
		return nil, notFound("no auto scaling configuration found for ARN %q", arn)
	}

	// Reject deleting a configuration a service still uses. DeleteAutoScalingConfiguration
	// does not model InvalidStateException, so an in-use config is an InvalidRequestException.
	inUse := arn
	if deleteAllRevisions {
		inUse = "" // any revision of cfg.Name counts as in use.
	}

	if used := m.ascInUse(cfg.Name, inUse); used != "" {
		return nil, invalidRequest("auto scaling configuration is in use by service %q", used)
	}

	now := m.now()
	deactivate(cfg, now)

	if deleteAllRevisions {
		for _, c := range m.ascByArn {
			if c.Name == cfg.Name {
				deactivate(c, now)
			}
		}
	}

	return copyASC(cfg), nil
}

// deactivate marks a revision INACTIVE and stamps its deletion time. Deleted
// (INACTIVE) revisions can't be used but remain queryable for a time.
func deactivate(c *driver.AutoScalingConfiguration, now time.Time) {
	c.Status = driver.StatusInactive
	c.Latest = false

	if c.DeletedAt.IsZero() {
		c.DeletedAt = now
	}
}

// ListAutoScalingConfigurations lists ACTIVE revisions, optionally filtered by
// name and to the latest revision per name, paginated by ARN.
//
//nolint:dupl // near-identical to ListObservabilityConfigurations by API shape (both revision-managed).
func (m *Mock) ListAutoScalingConfigurations(
	_ context.Context, name string, latestOnly bool, nextToken string, maxResults int32,
) ([]driver.AutoScalingConfiguration, string, error) {
	m.ascMu.Lock()
	all := make([]driver.AutoScalingConfiguration, 0, len(m.ascByArn))

	for _, c := range m.ascByArn {
		if name != "" && c.Name != name {
			continue
		}

		if latestOnly && !c.Latest {
			continue
		}

		all = append(all, *copyASC(c))
	}
	m.ascMu.Unlock()

	sort.Slice(all, func(i, j int) bool { return all[i].Arn < all[j].Arn })

	return paginate(all, nextToken, maxResults, func(c driver.AutoScalingConfiguration) string { return c.Arn })
}

// UpdateDefaultAutoScalingConfiguration marks one revision as the account
// default for its region, clearing the prior default.
func (m *Mock) UpdateDefaultAutoScalingConfiguration(
	_ context.Context, arn string,
) (*driver.AutoScalingConfiguration, error) {
	m.ascMu.Lock()
	defer m.ascMu.Unlock()

	target, ok := m.ascByArn[arn]
	if !ok {
		return nil, notFound("no auto scaling configuration found for ARN %q", arn)
	}

	for _, c := range m.ascByArn {
		c.IsDefault = false
	}

	target.IsDefault = true

	return copyASC(target), nil
}

// ListServicesForAutoScalingConfiguration returns the ARNs of services using
// the given ASC revision, paginated by ARN.
func (m *Mock) ListServicesForAutoScalingConfiguration(
	_ context.Context, arn, nextToken string, maxResults int32,
) (serviceArns []string, next string, err error) {
	m.ascMu.Lock()
	_, ok := m.ascByArn[arn]
	m.ascMu.Unlock()

	if !ok {
		return nil, "", notFound("no auto scaling configuration found for ARN %q", arn)
	}

	var arns []string

	for _, sd := range m.services.SortedValues() {
		sd.mu.RLock()
		if sd.svc.AutoScalingConfigArn == arn {
			arns = append(arns, sd.svc.ServiceArn)
		}
		sd.mu.RUnlock()
	}

	sort.Strings(arns)

	return paginate(arns, nextToken, maxResults, func(s string) string { return s })
}

// attachDefaultASC fills a service's auto scaling configuration summary. If the
// service specified an ASC ARN, its name/revision are resolved; otherwise the
// account default (or a synthesized DefaultConfiguration) is attached.
func (m *Mock) attachDefaultASC(svc *driver.Service) {
	m.ascMu.Lock()
	defer m.ascMu.Unlock()

	if svc.AutoScalingConfigArn != "" {
		if cfg, ok := m.ascByArn[svc.AutoScalingConfigArn]; ok {
			svc.AutoScalingConfigName = cfg.Name
			svc.AutoScalingConfigRevision = cfg.Revision
		}

		return
	}

	for _, c := range m.ascByArn {
		if c.IsDefault {
			svc.AutoScalingConfigArn = c.Arn
			svc.AutoScalingConfigName = c.Name
			svc.AutoScalingConfigRevision = c.Revision

			return
		}
	}

	svc.AutoScalingConfigName = defaultASCName
	svc.AutoScalingConfigRevision = 1
}
