package opensearch

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// copyDataSource returns a deep copy of a per-domain data source.
func copyDataSource(d driver.DataSource) driver.DataSource {
	out := d
	out.DataSourceType = copyRaw(d.DataSourceType)

	return out
}

// copyDQ returns a deep copy of a direct-query data source.
func copyDQ(d *driver.DirectQueryDataSource) driver.DirectQueryDataSource {
	out := *d
	out.DataSourceType = copyRaw(d.DataSourceType)
	out.OpenSearchArns = copyStrings(d.OpenSearchArns)
	out.TagList = copyTags(d.TagList)

	return out
}

// AddDataSource attaches an S3/Glue data source to a domain.
func (m *Mock) AddDataSource(_ context.Context, domainName string, ds driver.DataSource) (string, error) {
	dd, err := m.getDomain(domainName)
	if err != nil {
		return "", err
	}

	if ds.Name == "" {
		return "", validation("Name is required")
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	if _, exists := dd.dataSrcs[ds.Name]; exists {
		return "", alreadyExists("Data source already exists: %s", ds.Name)
	}

	stored := copyDataSource(ds)
	if stored.Status == "" {
		stored.Status = "ACTIVE"
	}

	dd.dataSrcs[ds.Name] = stored

	return "Data source [" + ds.Name + "] added to domain [" + domainName + "].", nil
}

// DeleteDataSource removes a data source from a domain.
func (m *Mock) DeleteDataSource(_ context.Context, domainName, name string) (string, error) {
	dd, err := m.getDomain(domainName)
	if err != nil {
		return "", err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	if _, ok := dd.dataSrcs[name]; !ok {
		return "", notFound("Data source not found: %s", name)
	}

	delete(dd.dataSrcs, name)

	return "Data source [" + name + "] deleted from domain [" + domainName + "].", nil
}

// GetDataSource returns a deep copy of a domain's data source.
func (m *Mock) GetDataSource(_ context.Context, domainName, name string) (*driver.DataSource, error) {
	dd, err := m.getDomain(domainName)
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()
	defer dd.mu.RUnlock()

	ds, ok := dd.dataSrcs[name]
	if !ok {
		return nil, notFound("Data source not found: %s", name)
	}

	out := copyDataSource(ds)

	return &out, nil
}

// UpdateDataSource updates a domain's data source in place.
func (m *Mock) UpdateDataSource(_ context.Context, domainName string, ds driver.DataSource) (string, error) {
	dd, err := m.getDomain(domainName)
	if err != nil {
		return "", err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	if _, ok := dd.dataSrcs[ds.Name]; !ok {
		return "", notFound("Data source not found: %s", ds.Name)
	}

	dd.dataSrcs[ds.Name] = copyDataSource(ds)

	return "Data source [" + ds.Name + "] updated on domain [" + domainName + "].", nil
}

// ListDataSources lists a domain's data sources, sorted by name.
func (m *Mock) ListDataSources(_ context.Context, domainName string) ([]driver.DataSource, error) {
	dd, err := m.getDomain(domainName)
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()
	defer dd.mu.RUnlock()

	names := make([]string, 0, len(dd.dataSrcs))
	for n := range dd.dataSrcs {
		names = append(names, n)
	}

	sort.Strings(names)

	out := make([]driver.DataSource, 0, len(names))
	for _, n := range names {
		out = append(out, copyDataSource(dd.dataSrcs[n]))
	}

	return out, nil
}

// AddDirectQueryDataSource creates a direct-query data source.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.OpenSearch interface (by-value input).
func (m *Mock) AddDirectQueryDataSource(_ context.Context, ds driver.DirectQueryDataSource) (string, error) {
	if ds.DataSourceName == "" {
		return "", validation("DataSourceName is required")
	}

	stored := copyDQ(&ds)
	stored.ARN = idgen.AWSARN("es", m.opts.Region, m.opts.AccountID, "directquerydatasource/"+ds.DataSourceName)

	if !m.dqDataSrcs.SetIfAbsent(ds.DataSourceName, &stored) {
		return "", alreadyExists("Direct query data source already exists: %s", ds.DataSourceName)
	}

	return stored.ARN, nil
}

// DeleteDirectQueryDataSource removes a direct-query data source.
func (m *Mock) DeleteDirectQueryDataSource(_ context.Context, name string) error {
	if !m.dqDataSrcs.Delete(name) {
		return notFound("Direct query data source not found: %s", name)
	}

	return nil
}

// GetDirectQueryDataSource returns a deep copy of a direct-query data source.
func (m *Mock) GetDirectQueryDataSource(_ context.Context, name string) (*driver.DirectQueryDataSource, error) {
	ds, ok := m.dqDataSrcs.Get(name)
	if !ok {
		return nil, notFound("Direct query data source not found: %s", name)
	}

	out := copyDQ(ds)

	return &out, nil
}

// UpdateDirectQueryDataSource updates a direct-query data source in place.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.OpenSearch interface (by-value input).
func (m *Mock) UpdateDirectQueryDataSource(_ context.Context, ds driver.DirectQueryDataSource) (string, error) {
	existing, ok := m.dqDataSrcs.Get(ds.DataSourceName)
	if !ok {
		return "", notFound("Direct query data source not found: %s", ds.DataSourceName)
	}

	stored := copyDQ(&ds)
	stored.ARN = existing.ARN
	m.dqDataSrcs.Set(ds.DataSourceName, &stored)

	return stored.ARN, nil
}

// ListDirectQueryDataSources lists all direct-query data sources, sorted.
func (m *Mock) ListDirectQueryDataSources(_ context.Context) ([]driver.DirectQueryDataSource, error) {
	names := m.dqDataSrcs.Keys()
	sort.Strings(names)

	out := make([]driver.DirectQueryDataSource, 0, len(names))

	for _, n := range names {
		if ds, ok := m.dqDataSrcs.Get(n); ok {
			out = append(out, copyDQ(ds))
		}
	}

	return out, nil
}
