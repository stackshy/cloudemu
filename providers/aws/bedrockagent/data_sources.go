package bedrockagent

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// CreateDataSource creates a data source under a knowledge base in the
// AVAILABLE state.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateDataSource(_ context.Context, cfg driver.DataSourceConfig) (*driver.DataSource, error) {
	if err := validateDataSource(cfg); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.knowledge.Has(cfg.KnowledgeBaseID) {
		return nil, errors.Newf(errors.NotFound, "knowledge base %q not found", cfg.KnowledgeBaseID)
	}

	id, err := newID(m.dataSource)
	if err != nil {
		return nil, err
	}

	now := m.now()
	ds := &driver.DataSource{
		ID:                      id,
		KnowledgeBaseID:         cfg.KnowledgeBaseID,
		Name:                    cfg.Name,
		Description:             cfg.Description,
		Status:                  driver.DataSourceAvailable,
		DataDeletionPolicy:      cfg.DataDeletionPolicy,
		DataSourceConfiguration: copyRaw(cfg.DataSourceConfiguration),
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	m.dataSource.Set(id, ds)

	result := cloneDataSource(ds)

	return &result, nil
}

// GetDataSource returns a data source by knowledge-base and data-source ID.
func (m *Mock) GetDataSource(_ context.Context, kbID, dsID string) (*driver.DataSource, error) {
	ds := m.findDataSource(kbID, dsID)
	if ds == nil {
		return nil, errors.Newf(errors.NotFound, "data source %q not found", dsID)
	}

	result := cloneDataSource(ds)

	return &result, nil
}

// ListDataSources lists one page of the data sources under a knowledge base.
func (m *Mock) ListDataSources(_ context.Context, kbID string, page driver.Page) ([]driver.DataSource, string, error) {
	if !m.knowledge.Has(kbID) {
		return nil, "", errors.Newf(errors.NotFound, "knowledge base %q not found", kbID)
	}

	all := m.dataSource.SortedValues()
	out := make([]driver.DataSource, 0, len(all))

	for _, ds := range all {
		if ds.KnowledgeBaseID == kbID {
			out = append(out, cloneDataSource(ds))
		}
	}

	return paginate(out, page)
}

// UpdateDataSource updates a data source's mutable fields.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) UpdateDataSource(_ context.Context, cfg driver.DataSourceConfig, dsID string) (*driver.DataSource, error) {
	if err := validateDataSource(cfg); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	ds := m.findDataSource(cfg.KnowledgeBaseID, dsID)
	if ds == nil {
		return nil, errors.Newf(errors.NotFound, "data source %q not found", dsID)
	}

	updated := *ds
	updated.Name = cfg.Name
	updated.Description = cfg.Description
	updated.DataDeletionPolicy = orDefault(cfg.DataDeletionPolicy, ds.DataDeletionPolicy)
	updated.UpdatedAt = m.now()
	updated.DataSourceConfiguration = copyRaw(cfg.DataSourceConfiguration)

	m.dataSource.Set(dsID, &updated)

	result := cloneDataSource(&updated)

	return &result, nil
}

// DeleteDataSource deletes a data source and, cascading like real AWS, every
// ingestion job and document that belongs to it.
func (m *Mock) DeleteDataSource(_ context.Context, kbID, dsID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.findDataSource(kbID, dsID) == nil {
		return "", errors.Newf(errors.NotFound, "data source %q not found", dsID)
	}

	m.dataSource.Delete(dsID)
	m.deleteJobsForDataSource(dsID)
	deleteByPrefix(m.documents, versionKey(kbID, dsID)+"/")

	return statusDeleting, nil
}

// validateDataSource checks the members CreateDataSource and UpdateDataSource
// require.
//
//nolint:gocritic // cfg matches the driver interface signature.
func validateDataSource(cfg driver.DataSourceConfig) error {
	var v violations

	v.required("name", cfg.Name == "")
	v.required("dataSourceConfiguration", isNull(cfg.DataSourceConfiguration))

	return v.err()
}

// findDataSource returns the data source matching dsID scoped to kbID, or nil.
func (m *Mock) findDataSource(kbID, dsID string) *driver.DataSource {
	ds, ok := m.dataSource.Get(dsID)
	if !ok || ds.KnowledgeBaseID != kbID {
		return nil
	}

	return ds
}

// deleteDataSourcesForKnowledgeBase removes every data source belonging to kbID.
// All() returns a snapshot, so deleting while ranging is safe.
func (m *Mock) deleteDataSourcesForKnowledgeBase(kbID string) {
	for id, ds := range m.dataSource.All() {
		if ds.KnowledgeBaseID == kbID {
			m.dataSource.Delete(id)
		}
	}
}

// deleteJobsForKnowledgeBase removes every ingestion job belonging to kbID.
func (m *Mock) deleteJobsForKnowledgeBase(kbID string) {
	for id, job := range m.jobs.All() {
		if job.KnowledgeBaseID == kbID {
			m.jobs.Delete(id)
			m.jobSettle.Clear(id)
		}
	}
}

// deleteJobsForDataSource removes every ingestion job belonging to dsID.
func (m *Mock) deleteJobsForDataSource(dsID string) {
	for id, job := range m.jobs.All() {
		if job.DataSourceID == dsID {
			m.jobs.Delete(id)
			m.jobSettle.Clear(id)
		}
	}
}

// cloneDataSource returns a value copy whose RawMessage config field does not
// alias the stored data source, so callers can't mutate internal state.
func cloneDataSource(ds *driver.DataSource) driver.DataSource {
	out := *ds
	out.DataSourceConfiguration = copyRaw(ds.DataSourceConfiguration)

	return out
}
