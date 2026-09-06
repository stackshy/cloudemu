package appsync

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

//nolint:gochecknoglobals // immutable validation set, read-only after init.
var validDataSourceTypes = map[string]bool{
	driver.DataSourceLambda:        true,
	driver.DataSourceDynamoDB:      true,
	driver.DataSourceElasticsearch: true,
	driver.DataSourceOpenSearch:    true,
	driver.DataSourceHTTP:          true,
	driver.DataSourceNone:          true,
	driver.DataSourceRelational:    true,
	driver.DataSourceEventBridge:   true,
}

// CreateDataSource attaches a data source to an API, computing its stable
// dataSourceArn.
func (m *Mock) CreateDataSource(_ context.Context, in *driver.CreateDataSourceInput) (*driver.DataSource, error) {
	if in.Name == "" {
		return nil, badRequest("name is required")
	}

	if !validDataSourceTypes[in.Type] {
		return nil, badRequest("data source type %q is not valid", in.Type)
	}

	ad, err := m.getAPI(in.APIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, exists := ad.dataSrcs[in.Name]; exists {
		return nil, badRequest("data source %q already exists", in.Name)
	}

	ds := driver.DataSource{
		APIID:          in.APIID,
		Name:           in.Name,
		Type:           in.Type,
		Description:    in.Description,
		ServiceRoleArn: in.ServiceRoleArn,
		DataSourceArn:  m.dataSourceARN(in.APIID, in.Name),
		Extra:          copyExtra(in.Extra),
	}
	ad.dataSrcs[in.Name] = ds
	out := copyDataSource(&ds)

	return &out, nil
}

// GetDataSource returns a copy of a data source.
func (m *Mock) GetDataSource(_ context.Context, apiID, name string) (*driver.DataSource, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	ds, ok := ad.dataSrcs[name]
	if !ok {
		return nil, notFound("data source %q not found", name)
	}

	out := copyDataSource(&ds)

	return &out, nil
}

// UpdateDataSource replaces the mutable fields of a data source while keeping
// its computed dataSourceArn.
func (m *Mock) UpdateDataSource(_ context.Context, in *driver.UpdateDataSourceInput) (*driver.DataSource, error) {
	if !validDataSourceTypes[in.Type] {
		return nil, badRequest("data source type %q is not valid", in.Type)
	}

	ad, err := m.getAPI(in.APIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	ds, ok := ad.dataSrcs[in.Name]
	if !ok {
		return nil, notFound("data source %q not found", in.Name)
	}

	ds.Type = in.Type
	ds.Description = in.Description
	ds.ServiceRoleArn = in.ServiceRoleArn
	ds.Extra = copyExtra(in.Extra)
	ad.dataSrcs[in.Name] = ds
	out := copyDataSource(&ds)

	return &out, nil
}

// DeleteDataSource removes a data source from an API.
func (m *Mock) DeleteDataSource(_ context.Context, apiID, name string) error {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, ok := ad.dataSrcs[name]; !ok {
		return notFound("data source %q not found", name)
	}

	delete(ad.dataSrcs, name)

	return nil
}

// ListDataSources returns a deterministic, deep-copied page of an API's data
// sources, ordered by name.
func (m *Mock) ListDataSources(_ context.Context, apiID string, page driver.Page) ([]driver.DataSource, string, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, "", err
	}

	ad.mu.RLock()
	all := sortedDataSources(ad.dataSrcs)
	ad.mu.RUnlock()

	start, end, next := paginate(len(all), page)

	return all[start:end], next, nil
}

func sortedDataSources(m map[string]driver.DataSource) []driver.DataSource {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}

	sort.Strings(names)

	out := make([]driver.DataSource, 0, len(m))

	for _, name := range names {
		ds := m[name]
		out = append(out, copyDataSource(&ds))
	}

	return out
}

func copyDataSource(ds *driver.DataSource) driver.DataSource {
	out := *ds
	out.Extra = copyExtra(ds.Extra)

	return out
}
