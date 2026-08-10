package configservice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// --- Stored queries ---

// PutStoredQuery creates or updates a saved query (upsert on QueryName).
//
//nolint:gocritic // query taken by value to match the driver API.
func (m *Mock) PutStoredQuery(_ context.Context, query driver.StoredQuery, tags map[string]string) (string, error) {
	if query.QueryName == "" {
		return "", invalidParameter("QueryName is required")
	}

	if query.Expression == "" {
		return "", invalidParameter("Expression is required")
	}

	// Validate the expression up front so a stored query is always runnable.
	if _, err := parseSelect(query.Expression); err != nil {
		return "", err
	}

	if existing, ok := m.storedQuery.Get(query.QueryName); ok {
		query.QueryArn = existing.QueryArn
		query.QueryID = existing.QueryID
		query.Tags = copyTags(existing.Tags)
		m.storedQuery.Set(query.QueryName, &query)

		return query.QueryArn, nil
	}

	query.QueryID = idgen.GenerateID("stored-query-")
	query.QueryArn = m.arn("stored-query/" + query.QueryName + "/" + query.QueryID)
	query.Tags = copyTags(tags)
	m.storedQuery.Set(query.QueryName, &query)

	return query.QueryArn, nil
}

// GetStoredQuery returns a saved query by name.
func (m *Mock) GetStoredQuery(_ context.Context, name string) (*driver.StoredQuery, error) {
	q, ok := m.storedQuery.Get(name)
	if !ok {
		return nil, noSuchStoredQuery(name)
	}

	out := *q
	out.Tags = copyTags(q.Tags)

	return &out, nil
}

// ListStoredQueries lists saved queries, paginated.
func (m *Mock) ListStoredQueries(_ context.Context, page driver.Page) ([]driver.StoredQuery, string, error) {
	keys := sortedKeys(m.storedQuery.Keys())
	out := make([]driver.StoredQuery, 0, len(keys))

	for _, k := range keys {
		q, ok := m.storedQuery.Get(k)
		if ok {
			cp := *q
			cp.Tags = copyTags(q.Tags)
			out = append(out, cp)
		}
	}

	return paginate(out, page)
}

// DeleteStoredQuery removes a saved query.
func (m *Mock) DeleteStoredQuery(_ context.Context, name string) error {
	if !m.storedQuery.Delete(name) {
		return noSuchStoredQuery(name)
	}

	return nil
}

// --- Retention configurations ---

// PutRetentionConfiguration sets the account's retention period. Config uses a
// single fixed name "default" per account.
func (m *Mock) PutRetentionConfiguration(
	_ context.Context, retentionDays int32,
) (driver.RetentionConfiguration, error) {
	const (
		minDays = 30
		maxDays = 2557
	)

	if retentionDays < minDays || retentionDays > maxDays {
		return driver.RetentionConfiguration{},
			invalidParameter("RetentionPeriodInDays must be between %d and %d", minDays, maxDays)
	}

	rc := driver.RetentionConfiguration{Name: defaultName, RetentionPeriodInDays: retentionDays}
	m.retention.Set(rc.Name, &rc)

	return rc, nil
}

// DescribeRetentionConfigurations returns the named retention configs.
func (m *Mock) DescribeRetentionConfigurations(
	_ context.Context, names []string, page driver.Page,
) ([]driver.RetentionConfiguration, string, error) {
	for _, n := range names {
		if !m.retention.Has(n) {
			return nil, "", noSuchRetentionConfig(n)
		}
	}

	keys := sortedKeys(m.retention.Keys())
	all := make([]driver.RetentionConfiguration, 0, len(keys))

	for _, k := range keys {
		rc, ok := m.retention.Get(k)
		if ok {
			all = append(all, *rc)
		}
	}

	filtered := filterByNames(all, func(r driver.RetentionConfiguration) string { return r.Name }, names)

	return paginate(filtered, page)
}

// DeleteRetentionConfiguration removes a retention config.
func (m *Mock) DeleteRetentionConfiguration(_ context.Context, name string) error {
	if !m.retention.Delete(name) {
		return noSuchRetentionConfig(name)
	}

	return nil
}

// --- Connectors ---

// PutConnector creates or updates a third-party connector.
func (m *Mock) PutConnector(_ context.Context, name, connectorAgentArn string) (string, error) {
	if name == "" {
		return "", invalidParameter("Name is required")
	}

	arn := m.arn("connector/" + name)
	m.connectors.Set(name, &connectorData{name: name, arn: arn, connectorAgentArn: connectorAgentArn})

	return arn, nil
}

// GetConnector returns a connector by name.
func (m *Mock) GetConnector(_ context.Context, name string) (arn, agentArn string, err error) {
	c, ok := m.connectors.Get(name)
	if !ok {
		return "", "", tagged(driver.ExResourceNotFound, notFoundCode, "connector %q does not exist", name)
	}

	return c.arn, c.connectorAgentArn, nil
}

// ListConnectors lists connector names, paginated.
func (m *Mock) ListConnectors(_ context.Context, page driver.Page) (names []string, nextToken string, err error) {
	return paginate(sortedKeys(m.connectors.Keys()), page)
}

// DeleteConnector removes a connector.
func (m *Mock) DeleteConnector(_ context.Context, name string) error {
	if !m.connectors.Delete(name) {
		return tagged(driver.ExResourceNotFound, notFoundCode, "connector %q does not exist", name)
	}

	return nil
}
