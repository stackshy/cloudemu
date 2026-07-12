package azuresearch

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/azuresearch/driver"
)

// childList collects values whose store key is under service+"/".
func childKeysUnder[V any](store interface{ All() map[string]V }, service string) []V {
	prefix := service + "/"
	out := make([]V, 0)

	for k, v := range store.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, v)
		}
	}

	return out
}

// --- Indexers ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateOrUpdateIndexer(_ context.Context, service string, cfg driver.IndexerConfig) (*driver.Indexer, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "indexer name is required")
	}

	ix := &driver.Indexer{
		Name: cfg.Name, DataSourceName: cfg.DataSourceName, TargetIndex: cfg.TargetIndex,
		SkillsetName: cfg.SkillsetName, Schedule: cfg.Schedule, ETag: m.etag(),
	}
	m.indexers.Set(key(service, cfg.Name), ix)
	m.indexerRuns.Set(key(service, cfg.Name), &driver.IndexerStatus{Name: cfg.Name, Status: "success", LastResult: "success"})

	out := *ix

	return &out, nil
}

func (m *Mock) GetIndexer(_ context.Context, service, name string) (*driver.Indexer, error) {
	ix, ok := m.indexers.Get(key(service, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "indexer %q not found", name)
	}

	out := *ix

	return &out, nil
}

func (m *Mock) ListIndexers(_ context.Context, service string) ([]driver.Indexer, error) {
	out := make([]driver.Indexer, 0)
	for _, ix := range childKeysUnder[*driver.Indexer](m.indexers, service) {
		out = append(out, *ix)
	}

	return out, nil
}

func (m *Mock) DeleteIndexer(_ context.Context, service, name string) error {
	if !m.indexers.Delete(key(service, name)) {
		return errors.Newf(errors.NotFound, "indexer %q not found", name)
	}

	m.indexerRuns.Delete(key(service, name))

	return nil
}

func (m *Mock) RunIndexer(_ context.Context, service, name string) error {
	return m.setIndexerStatus(service, name, "running")
}

func (m *Mock) ResetIndexer(_ context.Context, service, name string) error {
	return m.setIndexerStatus(service, name, "reset")
}

func (m *Mock) setIndexerStatus(service, name, status string) error {
	if !m.indexers.Has(key(service, name)) {
		return errors.Newf(errors.NotFound, "indexer %q not found", name)
	}

	m.indexerRuns.Set(key(service, name), &driver.IndexerStatus{Name: name, Status: status, LastResult: status})

	return nil
}

func (m *Mock) GetIndexerStatus(_ context.Context, service, name string) (*driver.IndexerStatus, error) {
	st, ok := m.indexerRuns.Get(key(service, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "indexer %q not found", name)
	}

	out := *st

	return &out, nil
}

// --- Data sources ---

//nolint:gocritic // ds matches the driver signature; copied on entry.
func (m *Mock) CreateOrUpdateDataSource(_ context.Context, service string, ds driver.DataSource) (*driver.DataSource, error) {
	if ds.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "data source name is required")
	}

	stored := ds
	stored.ETag = m.etag()
	m.dataSources.Set(key(service, ds.Name), &stored)

	out := stored

	return &out, nil
}

func (m *Mock) GetDataSource(_ context.Context, service, name string) (*driver.DataSource, error) {
	ds, ok := m.dataSources.Get(key(service, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "data source %q not found", name)
	}

	out := *ds

	return &out, nil
}

func (m *Mock) ListDataSources(_ context.Context, service string) ([]driver.DataSource, error) {
	out := make([]driver.DataSource, 0)
	for _, ds := range childKeysUnder[*driver.DataSource](m.dataSources, service) {
		out = append(out, *ds)
	}

	return out, nil
}

func (m *Mock) DeleteDataSource(_ context.Context, service, name string) error {
	if !m.dataSources.Delete(key(service, name)) {
		return errors.Newf(errors.NotFound, "data source %q not found", name)
	}

	return nil
}

// --- Skillsets ---

func (m *Mock) CreateOrUpdateSkillset(_ context.Context, service string, sk driver.Skillset) (*driver.Skillset, error) {
	if sk.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "skillset name is required")
	}

	stored := sk
	stored.ETag = m.etag()
	m.skillsets.Set(key(service, sk.Name), &stored)

	out := stored

	return &out, nil
}

func (m *Mock) GetSkillset(_ context.Context, service, name string) (*driver.Skillset, error) {
	sk, ok := m.skillsets.Get(key(service, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "skillset %q not found", name)
	}

	out := *sk

	return &out, nil
}

func (m *Mock) ListSkillsets(_ context.Context, service string) ([]driver.Skillset, error) {
	out := make([]driver.Skillset, 0)
	for _, sk := range childKeysUnder[*driver.Skillset](m.skillsets, service) {
		out = append(out, *sk)
	}

	return out, nil
}

func (m *Mock) DeleteSkillset(_ context.Context, service, name string) error {
	if !m.skillsets.Delete(key(service, name)) {
		return errors.Newf(errors.NotFound, "skillset %q not found", name)
	}

	return nil
}

// --- Synonym maps ---

func (m *Mock) CreateOrUpdateSynonymMap(_ context.Context, service string, sm driver.SynonymMap) (*driver.SynonymMap, error) {
	if sm.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "synonym map name is required")
	}

	stored := sm
	if stored.Format == "" {
		stored.Format = "solr"
	}

	stored.ETag = m.etag()
	m.synonymMaps.Set(key(service, sm.Name), &stored)

	out := stored

	return &out, nil
}

func (m *Mock) GetSynonymMap(_ context.Context, service, name string) (*driver.SynonymMap, error) {
	sm, ok := m.synonymMaps.Get(key(service, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "synonym map %q not found", name)
	}

	out := *sm

	return &out, nil
}

func (m *Mock) ListSynonymMaps(_ context.Context, service string) ([]driver.SynonymMap, error) {
	out := make([]driver.SynonymMap, 0)
	for _, sm := range childKeysUnder[*driver.SynonymMap](m.synonymMaps, service) {
		out = append(out, *sm)
	}

	return out, nil
}

func (m *Mock) DeleteSynonymMap(_ context.Context, service, name string) error {
	if !m.synonymMaps.Delete(key(service, name)) {
		return errors.Newf(errors.NotFound, "synonym map %q not found", name)
	}

	return nil
}

// --- Aliases ---

func (m *Mock) CreateOrUpdateAlias(_ context.Context, service string, alias driver.Alias) (*driver.Alias, error) {
	if alias.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "alias name is required")
	}

	stored := alias
	stored.Indexes = append([]string(nil), alias.Indexes...)
	stored.ETag = m.etag()
	m.aliases.Set(key(service, alias.Name), &stored)

	out := stored
	out.Indexes = append([]string(nil), stored.Indexes...)

	return &out, nil
}

func (m *Mock) GetAlias(_ context.Context, service, name string) (*driver.Alias, error) {
	a, ok := m.aliases.Get(key(service, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "alias %q not found", name)
	}

	out := *a
	out.Indexes = append([]string(nil), a.Indexes...)

	return &out, nil
}

func (m *Mock) ListAliases(_ context.Context, service string) ([]driver.Alias, error) {
	out := make([]driver.Alias, 0)

	for _, a := range childKeysUnder[*driver.Alias](m.aliases, service) {
		c := *a
		c.Indexes = append([]string(nil), a.Indexes...)
		out = append(out, c)
	}

	return out, nil
}

func (m *Mock) DeleteAlias(_ context.Context, service, name string) error {
	if !m.aliases.Delete(key(service, name)) {
		return errors.Newf(errors.NotFound, "alias %q not found", name)
	}

	return nil
}

// --- Service statistics ---

func (m *Mock) GetServiceStatistics(_ context.Context, service string) (*driver.ServiceStatistics, error) {
	stats := &driver.ServiceStatistics{}

	idxPrefix := service + "/"
	for k := range m.indexes.All() {
		if strings.HasPrefix(k, idxPrefix) {
			stats.IndexCount++
		}
	}

	for k := range m.documents.All() {
		if strings.HasPrefix(k, idxPrefix) {
			stats.DocumentCount++
		}
	}

	stats.IndexerCount = len(childKeysUnder[*driver.Indexer](m.indexers, service))
	stats.DataSourceCount = len(childKeysUnder[*driver.DataSource](m.dataSources, service))
	stats.StorageBytes = stats.DocumentCount * 1024

	return stats, nil
}
