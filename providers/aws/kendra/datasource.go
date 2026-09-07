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

// CreateDataSource provisions a data source connector under an existing index,
// directly in the ACTIVE state with stable computed fields (id, status,
// createdAt). The parent index must exist (404 otherwise). A CUSTOM data source
// must not carry Configuration/RoleArn/Schedule; every other type requires a
// Configuration and RoleArn, mirroring the real API's ValidationException rules.
func (m *Mock) CreateDataSource(_ context.Context, in *driver.CreateDataSourceInput) (*driver.DataSource, error) {
	if in.Name == "" {
		return nil, validation("Name is required")
	}

	if !validDataSourceTypes[in.Type] {
		return nil, validation("invalid data source Type: %q", in.Type)
	}

	if _, ok := m.indexes.Get(in.IndexID); !ok {
		return nil, notFound("index with id %q does not exist", in.IndexID)
	}

	if err := validateDataSourceType(in); err != nil {
		return nil, err
	}

	id := newDataSourceID()
	now := m.now()

	ds := driver.DataSource{
		ID:                                    id,
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

	m.dataSources.Set(dataSourceKey(in.IndexID, id), ds)

	out := copyDataSource(&ds)

	return &out, nil
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
	ds, ok := m.dataSources.Get(dataSourceKey(indexID, id))
	if !ok {
		return nil, notFound("data source %q does not exist for index %q", id, indexID)
	}

	out := copyDataSource(&ds)

	return &out, nil
}

// UpdateDataSource applies the supplied fields, leaving omitted parameters
// unchanged. The computed id, status and createdAt are preserved; updatedAt is
// bumped.
func (m *Mock) UpdateDataSource(_ context.Context, in *driver.UpdateDataSourceInput) error {
	key := dataSourceKey(in.IndexID, in.ID)

	ok := m.dataSources.Update(key, func(d driver.DataSource) driver.DataSource {
		if in.Name != nil {
			d.Name = *in.Name
		}

		if in.RoleArn != nil {
			d.RoleArn = *in.RoleArn
		}

		if in.Description != nil {
			d.Description = *in.Description
		}

		if in.Schedule != nil {
			d.Schedule = *in.Schedule
		}

		if in.LanguageCode != nil {
			d.LanguageCode = *in.LanguageCode
		}

		if in.Configuration != nil {
			d.Configuration = copyRaw(in.Configuration)
		}

		if in.VpcConfiguration != nil {
			d.VpcConfiguration = copyRaw(in.VpcConfiguration)
		}

		if in.CustomDocumentEnrichmentConfiguration != nil {
			d.CustomDocumentEnrichmentConfiguration = copyRaw(in.CustomDocumentEnrichmentConfiguration)
		}

		d.UpdatedAt = m.now()

		return d
	})
	if !ok {
		return notFound("data source %q does not exist for index %q", in.ID, in.IndexID)
	}

	return nil
}

// DeleteDataSource removes a data source from its index.
func (m *Mock) DeleteDataSource(_ context.Context, indexID, id string) error {
	key := dataSourceKey(indexID, id)

	if _, ok := m.dataSources.Get(key); !ok {
		return notFound("data source %q does not exist for index %q", id, indexID)
	}

	m.dataSources.Delete(key)

	return nil
}

// ListDataSources returns a deterministic page of the data sources belonging to
// an index, ordered by id.
func (m *Mock) ListDataSources(
	_ context.Context, indexID string, page driver.Page,
) (dataSources []driver.DataSource, nextToken string, err error) {
	stored := m.dataSources.SortedValues()

	filtered := make([]driver.DataSource, 0, len(stored))

	for i := range stored {
		if stored[i].IndexID == indexID {
			filtered = append(filtered, stored[i])
		}
	}

	start, end, next := paginate(len(filtered), page)

	out := make([]driver.DataSource, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, copyDataSource(&filtered[i]))
	}

	return out, next, nil
}
