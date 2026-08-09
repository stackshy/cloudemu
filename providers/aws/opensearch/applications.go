package opensearch

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// copyApplication returns a deep copy of an application.
func copyApplication(a *driver.Application) driver.Application {
	out := *a
	out.DataSources = copyRawSlice(a.DataSources)
	out.AppConfigs = copyRawSlice(a.AppConfigs)
	out.IamIdentityCenterOptions = copyRaw(a.IamIdentityCenterOptions)
	out.TagList = copyTags(a.TagList)

	return out
}

// CreateApplication creates an OpenSearch application, immediately ACTIVE.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.OpenSearch interface (by-value input).
func (m *Mock) CreateApplication(_ context.Context, in driver.CreateApplicationInput) (*driver.Application, error) {
	if in.Name == "" {
		return nil, validation("Name is required")
	}

	id := idgen.GenerateID("")
	now := m.now()
	app := &driver.Application{
		ID:                       id,
		ARN:                      idgen.AWSARN("es", m.opts.Region, m.opts.AccountID, "application/"+id),
		Name:                     in.Name,
		Endpoint:                 "https://" + id + "." + m.opts.Region + ".opensearch.amazonaws.com",
		Status:                   "ACTIVE",
		CreatedAt:                now,
		LastUpdatedAt:            now,
		DataSources:              copyRawSlice(in.DataSources),
		IamIdentityCenterOptions: copyRaw(in.IamIdentityCenterOptions),
		AppConfigs:               copyRawSlice(in.AppConfigs),
		TagList:                  copyTags(in.TagList),
	}

	if !m.apps.SetIfAbsent(id, app) {
		return nil, alreadyExists("Application already exists: %s", id)
	}

	out := copyApplication(app)

	return &out, nil
}

// GetApplication returns a deep copy of an application.
func (m *Mock) GetApplication(_ context.Context, id string) (*driver.Application, error) {
	app, ok := m.apps.Get(id)
	if !ok {
		return nil, notFound("Application not found: %s", id)
	}

	out := copyApplication(app)

	return &out, nil
}

// UpdateApplication updates an application's data sources and app configs.
func (m *Mock) UpdateApplication(_ context.Context, id string,
	dataSources, appConfigs []map[string]json.RawMessage,
) (*driver.Application, error) {
	app, ok := m.apps.Get(id)
	if !ok {
		return nil, notFound("Application not found: %s", id)
	}

	out := copyApplication(app)
	if dataSources != nil {
		out.DataSources = copyRawSlice(dataSources)
	}

	if appConfigs != nil {
		out.AppConfigs = copyRawSlice(appConfigs)
	}

	out.LastUpdatedAt = m.now()
	m.apps.Set(id, &out)

	result := copyApplication(&out)

	return &result, nil
}

// DeleteApplication removes an application.
func (m *Mock) DeleteApplication(_ context.Context, id string) error {
	if !m.apps.Delete(id) {
		return notFound("Application not found: %s", id)
	}

	return nil
}

// ListApplications lists all applications, sorted by ID.
func (m *Mock) ListApplications(_ context.Context, page driver.Page) ([]driver.Application, string, error) {
	ids := m.apps.Keys()
	sort.Strings(ids)

	out := make([]driver.Application, 0, len(ids))

	for _, id := range ids {
		if app, ok := m.apps.Get(id); ok {
			out = append(out, copyApplication(app))
		}
	}

	start, end, next := paginate(len(out), page)

	return out[start:end], next, nil
}
