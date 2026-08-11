package kafka

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// copyRevision deep-copies a configuration revision (its ServerProperties blob).
func copyRevision(r driver.ConfigurationRevision) driver.ConfigurationRevision {
	out := r
	out.ServerProperties = copyBytes(r.ServerProperties)

	return out
}

// snapshotConfig returns a deep copy of a stored configuration, including its
// full revision history and every revision's ServerProperties blob.
//
//nolint:gocritic // hugeParam: takes a value by design to snapshot stored state.
func snapshotConfig(c driver.Configuration) driver.Configuration {
	out := c
	out.KafkaVersions = copyStrings(c.KafkaVersions)
	out.LatestRevision = copyRevision(c.LatestRevision)

	if c.Revisions != nil {
		out.Revisions = make([]driver.ConfigurationRevision, len(c.Revisions))
		for i := range c.Revisions {
			out.Revisions[i] = copyRevision(c.Revisions[i])
		}
	}

	return out
}

// getConfig resolves a configuration by ARN, NotFoundException when absent.
func (m *Mock) getConfig(arn string) (*configData, error) {
	cd, ok := m.configs.Get(arn)
	if !ok {
		return nil, notFound("configuration not found: %s", arn)
	}

	return cd, nil
}

// CreateConfiguration creates a configuration with its first revision (1).
//
//nolint:gocritic // hugeParam: signature fixed by driver.Kafka (by-value input).
func (m *Mock) CreateConfiguration(_ context.Context, in driver.CreateConfigurationInput) (*driver.Configuration, error) {
	if in.Name == "" {
		return nil, badRequest("configuration name is required")
	}

	now := m.now()
	rev := driver.ConfigurationRevision{
		Revision:         1,
		Description:      in.Description,
		CreationTime:     now,
		ServerProperties: copyBytes(in.ServerProperties),
	}

	cfg := driver.Configuration{
		ARN:            m.configARN(in.Name),
		Name:           in.Name,
		Description:    in.Description,
		State:          driver.ConfigurationStateActive,
		KafkaVersions:  copyStrings(in.KafkaVersions),
		CreationTime:   now,
		LatestRevision: rev,
		Revisions:      []driver.ConfigurationRevision{rev},
	}

	// ARNs embed a fresh UUID, so a collision cannot occur; SetIfAbsent guards
	// the (impossible-but-cheap) race and keeps the create atomic.
	if !m.configs.SetIfAbsent(cfg.ARN, &configData{config: cfg}) {
		return nil, conflict("configuration already exists: %s", cfg.ARN)
	}

	out := snapshotConfig(cfg)

	return &out, nil
}

// DescribeConfiguration returns a deep copy of the stored configuration.
func (m *Mock) DescribeConfiguration(_ context.Context, arn string) (*driver.Configuration, error) {
	cd, err := m.getConfig(arn)
	if err != nil {
		return nil, err
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	out := snapshotConfig(cd.config)

	return &out, nil
}

// ListConfigurations lists all configurations sorted by name.
func (m *Mock) ListConfigurations(
	_ context.Context, page driver.Page,
) (configs []driver.Configuration, next string, err error) {
	vals := m.configs.SortedValues()

	all := make([]driver.Configuration, 0, len(vals))

	for _, cd := range vals {
		cd.mu.RLock()
		all = append(all, snapshotConfig(cd.config))
		cd.mu.RUnlock()
	}

	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })

	start, end, nextTok, err := m.paginate(len(all), page)
	if err != nil {
		return nil, "", err
	}

	return all[start:end], nextTok, nil
}

// UpdateConfiguration appends a new revision to a configuration and returns the
// updated configuration with the new latest revision.
func (m *Mock) UpdateConfiguration(
	_ context.Context, arn, description string, serverProperties []byte,
) (*driver.Configuration, error) {
	cd, err := m.getConfig(arn)
	if err != nil {
		return nil, err
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	next := cd.config.LatestRevision.Revision + 1
	rev := driver.ConfigurationRevision{
		Revision:         next,
		Description:      description,
		CreationTime:     m.now(),
		ServerProperties: copyBytes(serverProperties),
	}

	cd.config.Revisions = append(cd.config.Revisions, rev)
	cd.config.LatestRevision = rev

	out := snapshotConfig(cd.config)

	return &out, nil
}

// DeleteConfiguration removes a configuration.
func (m *Mock) DeleteConfiguration(_ context.Context, arn string) (arnOut, state string, err error) {
	if _, err := m.getConfig(arn); err != nil {
		return "", "", err
	}

	m.configs.Delete(arn)

	return arn, driver.ConfigurationStateDeleting, nil
}

// ListConfigurationRevisions lists a configuration's revisions, newest last,
// paginated.
func (m *Mock) ListConfigurationRevisions(
	_ context.Context, arn string, page driver.Page,
) (revisions []driver.ConfigurationRevision, next string, err error) {
	cd, err := m.getConfig(arn)
	if err != nil {
		return nil, "", err
	}

	cd.mu.RLock()
	all := make([]driver.ConfigurationRevision, len(cd.config.Revisions))

	for i := range cd.config.Revisions {
		all[i] = copyRevision(cd.config.Revisions[i])
	}
	cd.mu.RUnlock()

	start, end, nextTok, err := m.paginate(len(all), page)
	if err != nil {
		return nil, "", err
	}

	return all[start:end], nextTok, nil
}

// DescribeConfigurationRevision returns a configuration and one of its
// revisions (including that revision's ServerProperties).
func (m *Mock) DescribeConfigurationRevision(
	_ context.Context, arn string, revision int64,
) (*driver.Configuration, *driver.ConfigurationRevision, error) {
	cd, err := m.getConfig(arn)
	if err != nil {
		return nil, nil, err
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	for i := range cd.config.Revisions {
		if cd.config.Revisions[i].Revision == revision {
			cfg := snapshotConfig(cd.config)
			rev := copyRevision(cd.config.Revisions[i])

			return &cfg, &rev, nil
		}
	}

	return nil, nil, notFound("configuration revision %d not found for %s", revision, arn)
}
