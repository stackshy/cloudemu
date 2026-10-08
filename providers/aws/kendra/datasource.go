package kendra

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// validDataSourceTypes is the set of data source connector types the API
// accepts.
//
//nolint:gochecknoglobals // static validation set
var validDataSourceTypes = map[string]bool{
	"S3": true, "SHAREPOINT": true, "DATABASE": true, "SALESFORCE": true,
	"ONEDRIVE": true, "SERVICENOW": true, "CUSTOM": true, "CONFLUENCE": true,
	"GOOGLEDRIVE": true, "WEBCRAWLER": true, "WORKDOCS": true, "FSX": true,
	"SLACK": true, "BOX": true, "QUIP": true, "JIRA": true, "GITHUB": true,
	"ALFRESCO": true, "TEMPLATE": true,
}

// CreateDataSource provisions a data source connector under an existing, ACTIVE
// index, with stable computed fields (id, status, createdAt); it is ACTIVE at
// once by default and reports CREATING first under async settling. A CUSTOM data
// source must not carry Configuration/RoleArn/Schedule; every other type
// requires a Configuration and RoleArn, mirroring the real API's
// ValidationException rules. A repeated ClientToken under the same index returns
// the data source the first call created.
func (m *Mock) CreateDataSource(_ context.Context, in *driver.CreateDataSourceInput) (*driver.DataSource, error) {
	if err := validateIndexID(in.IndexID); err != nil {
		return nil, err
	}

	if err := validateName(in.Name, maxIndexNameLen); err != nil {
		return nil, err
	}

	if !validDataSourceTypes[in.Type] {
		return nil, validation("invalid data source Type: %q", in.Type)
	}

	if err := validateDataSourceType(in); err != nil {
		return nil, err
	}

	if in.RoleArn != "" {
		if err := validateRoleArn(in.RoleArn); err != nil {
			return nil, err
		}
	}

	if err := validateCommon(in.ClientToken, in.Description, in.Tags); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveIndex(in.IndexID); err != nil {
		return nil, err
	}

	if existing, ok := m.dataSourceByToken(in.IndexID, in.ClientToken); ok {
		out := m.viewDataSource(&existing)

		return &out, nil
	}

	id := newDataSourceID()
	now := m.now()

	ds := driver.DataSource{
		ID:                                    id,
		ClientToken:                           in.ClientToken,
		IndexID:                               in.IndexID,
		Name:                                  in.Name,
		Type:                                  in.Type,
		RoleArn:                               in.RoleArn,
		Description:                           in.Description,
		Schedule:                              in.Schedule,
		LanguageCode:                          in.LanguageCode,
		Status:                                driver.DataSourceStatusActive,
		Configuration:                         copyRaw(in.Configuration),
		VpcConfiguration:                      copyRaw(in.VpcConfiguration),
		CustomDocumentEnrichmentConfiguration: copyRaw(in.CustomDocumentEnrichmentConfiguration),
		CreatedAt:                             now,
		UpdatedAt:                             now,
		Tags:                                  copyTags(in.Tags),
	}

	key := dataSourceKey(in.IndexID, id)
	m.dataSources.Set(key, ds)
	m.beginSettle(key, driver.DataSourceStatusCreating)

	out := m.viewDataSource(&ds)

	return &out, nil
}

// viewDataSource returns an alias-free copy of a data source with its status
// overlaid by any settle window.
func (m *Mock) viewDataSource(d *driver.DataSource) driver.DataSource {
	out := copyDataSource(d)
	out.Status = m.settleStatus(dataSourceKey(d.IndexID, d.ID), d.Status)

	return out
}

// getDataSource returns the stored data source, or the exception for a
// malformed id or an unknown index or data source.
func (m *Mock) getDataSource(indexID, id string) (driver.DataSource, error) {
	if err := validateIndexID(indexID); err != nil {
		return driver.DataSource{}, err
	}

	if err := validateChildID("id", id, true); err != nil {
		return driver.DataSource{}, err
	}

	if _, ok := m.indexes.Get(indexID); !ok {
		return driver.DataSource{}, notFound("index with id %q does not exist", indexID)
	}

	ds, ok := m.dataSources.Get(dataSourceKey(indexID, id))
	if !ok {
		return driver.DataSource{}, notFound("data source %q does not exist for index %q", id, indexID)
	}

	return ds, nil
}

// requireActiveDataSource returns the data source when it is ACTIVE and a
// ConflictException while it is still settling.
func (m *Mock) requireActiveDataSource(indexID, id string) error {
	ds, err := m.getDataSource(indexID, id)
	if err != nil {
		return err
	}

	if status := m.settleStatus(dataSourceKey(indexID, id), ds.Status); status != driver.DataSourceStatusActive {
		return conflict("data source %q is %s; try again when it is ACTIVE", id, status)
	}

	return nil
}

// dataSourceByToken returns the data source a previous create under the same
// index made with the same client token; an empty token never matches.
func (m *Mock) dataSourceByToken(indexID, token string) (driver.DataSource, bool) {
	if token == "" {
		return driver.DataSource{}, false
	}

	stored := m.dataSources.SortedValues()
	for i := range stored {
		if stored[i].IndexID == indexID && stored[i].ClientToken == token {
			return stored[i], true
		}
	}

	return driver.DataSource{}, false
}

// validateDataSourceType enforces the CUSTOM-vs-other constraints the real API
// rejects with a ValidationException.
func validateDataSourceType(in *driver.CreateDataSourceInput) error {
	if in.Type == driver.DataSourceTypeCustom {
		if in.Configuration != nil || in.RoleArn != "" || in.Schedule != "" {
			return validation("a CUSTOM data source must not specify Configuration, RoleArn or Schedule")
		}

		return nil
	}

	if in.Configuration == nil {
		return validation("Configuration is required for a %s data source", in.Type)
	}

	if in.RoleArn == "" {
		return validation("RoleArn is required for a %s data source", in.Type)
	}

	return nil
}

// DescribeDataSource returns a data source by index id and its own id.
func (m *Mock) DescribeDataSource(_ context.Context, indexID, id string) (*driver.DataSource, error) {
	ds, err := m.getDataSource(indexID, id)
	if err != nil {
		return nil, err
	}

	out := m.viewDataSource(&ds)

	return &out, nil
}

// UpdateDataSource applies the supplied fields, leaving omitted parameters
// unchanged. The computed id, status and createdAt are preserved; updatedAt is
// bumped. A data source that is not ACTIVE cannot be updated (ConflictException).
func (m *Mock) UpdateDataSource(_ context.Context, in *driver.UpdateDataSourceInput) error {
	if err := validateUpdateDataSource(in); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveDataSource(in.IndexID, in.ID); err != nil {
		return err
	}

	key := dataSourceKey(in.IndexID, in.ID)

	m.dataSources.Update(key, func(d driver.DataSource) driver.DataSource {
		applyDataSourceUpdate(&d, in)
		d.UpdatedAt = m.now()

		return d
	})

	m.beginSettle(key, driver.DataSourceStatusUpdating)

	return nil
}

// validateUpdateDataSource applies UpdateDataSource's input rules.
func validateUpdateDataSource(in *driver.UpdateDataSourceInput) error {
	if in.Name != nil {
		if err := validateName(*in.Name, maxIndexNameLen); err != nil {
			return err
		}
	}

	if in.RoleArn != nil && *in.RoleArn != "" {
		if err := validateRoleArn(*in.RoleArn); err != nil {
			return err
		}
	}

	if in.Description != nil && len(*in.Description) > maxDescriptionLen {
		return validation("Description must have length between 0 and %d", maxDescriptionLen)
	}

	return nil
}

// applyDataSourceUpdate overlays the supplied members of an update onto a data source.
func applyDataSourceUpdate(d *driver.DataSource, in *driver.UpdateDataSourceInput) {
	setIfSet(&d.Name, in.Name)
	setIfSet(&d.RoleArn, in.RoleArn)
	setIfSet(&d.Description, in.Description)
	setIfSet(&d.Schedule, in.Schedule)
	setIfSet(&d.LanguageCode, in.LanguageCode)
	setRawIfSet(&d.Configuration, in.Configuration)
	setRawIfSet(&d.VpcConfiguration, in.VpcConfiguration)
	setRawIfSet(&d.CustomDocumentEnrichmentConfiguration, in.CustomDocumentEnrichmentConfiguration)
}

// DeleteDataSource removes a data source from its index together with its sync
// history. A data source that is syncing is in use (ResourceInUseException); one
// that is not ACTIVE conflicts.
func (m *Mock) DeleteDataSource(_ context.Context, indexID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveDataSource(indexID, id); err != nil {
		return err
	}

	key := dataSourceKey(indexID, id)

	if m.syncRunning(key) {
		return resourceInUse("data source %q has a synchronization job in progress", id)
	}

	m.dataSources.Delete(key)
	m.settling.Clear(key)
	deleteWithPrefix(m.syncJobs, key+"/", m.settling.Clear)
	deleteWithPrefix(m.mappings, key+"/", m.settling.Clear)

	return nil
}

// ListDataSources returns a deterministic page of the data sources belonging to
// an index, ordered by id. An index that does not exist is a
// ResourceNotFoundException, as in real Kendra, not an empty page.
func (m *Mock) ListDataSources(
	_ context.Context, indexID string, page driver.Page,
) (dataSources []driver.DataSource, nextToken string, err error) {
	if _, err = m.getIndex(indexID); err != nil {
		return nil, "", err
	}

	stored := m.dataSources.SortedValues()

	filtered := make([]driver.DataSource, 0, len(stored))

	for i := range stored {
		if stored[i].IndexID == indexID {
			filtered = append(filtered, stored[i])
		}
	}

	start, end, next, err := m.paginate("datasources/"+indexID, len(filtered), page, maxPageSize)
	if err != nil {
		return nil, "", err
	}

	out := make([]driver.DataSource, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, m.viewDataSource(&filtered[i]))
	}

	return out, next, nil
}
