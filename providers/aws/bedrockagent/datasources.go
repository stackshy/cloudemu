package bedrockagent

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// CreateDataSource creates a data source under a knowledge base in the
// AVAILABLE state.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateDataSource(_ context.Context, cfg driver.DataSourceConfig) (*driver.DataSource, error) {
	switch {
	case cfg.Name == "":
		return nil, errors.New(errors.InvalidArgument, "name is required")
	case len(cfg.DataSourceConfiguration) == 0:
		return nil, errors.New(errors.InvalidArgument, "dataSourceConfiguration is required")
	}

	if !m.knowledge.Has(cfg.KnowledgeBaseID) {
		return nil, errors.Newf(errors.NotFound, "knowledge base %q not found", cfg.KnowledgeBaseID)
	}

	id := idgen.GenerateID("DS")
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

	result := *ds

	return &result, nil
}

// GetDataSource returns a data source by knowledge-base and data-source ID.
func (m *Mock) GetDataSource(_ context.Context, kbID, dsID string) (*driver.DataSource, error) {
	ds := m.findDataSource(kbID, dsID)
	if ds == nil {
		return nil, errors.Newf(errors.NotFound, "data source %q not found", dsID)
	}

	result := *ds

	return &result, nil
}

// ListDataSources lists all data sources under a knowledge base.
func (m *Mock) ListDataSources(_ context.Context, kbID string) ([]driver.DataSource, error) {
	all := m.dataSource.SortedValues()
	out := make([]driver.DataSource, 0, len(all))

	for _, ds := range all {
		if ds.KnowledgeBaseID == kbID {
			out = append(out, *ds)
		}
	}

	return out, nil
}

// UpdateDataSource updates a data source's mutable fields.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) UpdateDataSource(_ context.Context, cfg driver.DataSourceConfig, dsID string) (*driver.DataSource, error) {
	ds := m.findDataSource(cfg.KnowledgeBaseID, dsID)
	if ds == nil {
		return nil, errors.Newf(errors.NotFound, "data source %q not found", dsID)
	}

	updated := *ds
	updated.Name = orDefault(cfg.Name, ds.Name)
	updated.Description = cfg.Description
	updated.DataDeletionPolicy = orDefault(cfg.DataDeletionPolicy, ds.DataDeletionPolicy)
	updated.UpdatedAt = m.now()

	if len(cfg.DataSourceConfiguration) != 0 {
		updated.DataSourceConfiguration = copyRaw(cfg.DataSourceConfiguration)
	}

	m.dataSource.Set(dsID, &updated)

	result := updated

	return &result, nil
}

// DeleteDataSource deletes a data source and returns its terminal status.
func (m *Mock) DeleteDataSource(_ context.Context, kbID, dsID string) (string, error) {
	if m.findDataSource(kbID, dsID) == nil {
		return "", errors.Newf(errors.NotFound, "data source %q not found", dsID)
	}

	m.dataSource.Delete(dsID)

	return statusDeleting, nil
}

// StartIngestionJob starts an ingestion job that completes synchronously.
func (m *Mock) StartIngestionJob(_ context.Context, kbID, dsID, description string) (*driver.IngestionJob, error) {
	if m.findDataSource(kbID, dsID) == nil {
		return nil, errors.Newf(errors.NotFound, "data source %q not found", dsID)
	}

	id := idgen.GenerateID("JOB")
	now := m.now()
	job := &driver.IngestionJob{
		ID:              id,
		KnowledgeBaseID: kbID,
		DataSourceID:    dsID,
		Description:     description,
		Status:          driver.IngestionJobComplete,
		StartedAt:       now,
		UpdatedAt:       now,
	}
	m.jobs.Set(id, job)

	result := *job

	return &result, nil
}

// findDataSource returns the data source matching dsID scoped to kbID, or nil.
func (m *Mock) findDataSource(kbID, dsID string) *driver.DataSource {
	ds, ok := m.dataSource.Get(dsID)
	if !ok || ds.KnowledgeBaseID != kbID {
		return nil
	}

	return ds
}
