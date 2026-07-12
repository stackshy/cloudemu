package azuresearch

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/azuresearch/driver"
)

func (a *AzureSearch) CreateOrUpdateIndex(ctx context.Context, service string, idx driver.Index) (*driver.Index, error) {
	return cast[*driver.Index](a.do(ctx, "CreateOrUpdateIndex", idx.Name, func() (any, error) {
		return a.drv.CreateOrUpdateIndex(ctx, service, idx)
	}))
}

func (a *AzureSearch) GetIndex(ctx context.Context, service, name string) (*driver.Index, error) {
	return cast[*driver.Index](a.do(ctx, "GetIndex", name, func() (any, error) { return a.drv.GetIndex(ctx, service, name) }))
}

func (a *AzureSearch) ListIndexes(ctx context.Context, service string) ([]driver.Index, error) {
	return cast[[]driver.Index](a.do(ctx, "ListIndexes", service, func() (any, error) { return a.drv.ListIndexes(ctx, service) }))
}

func (a *AzureSearch) DeleteIndex(ctx context.Context, service, name string) error {
	return a.act(ctx, "DeleteIndex", name, func() error { return a.drv.DeleteIndex(ctx, service, name) })
}

func (a *AzureSearch) IndexDocuments(
	ctx context.Context, service, index string, actions []driver.IndexAction,
) ([]driver.IndexResult, error) {
	return cast[[]driver.IndexResult](a.do(ctx, "IndexDocuments", index, func() (any, error) {
		return a.drv.IndexDocuments(ctx, service, index, actions)
	}))
}

//nolint:gocritic // req matches the driver signature; forwarded unchanged.
func (a *AzureSearch) SearchDocuments(
	ctx context.Context, service, index string, req driver.SearchRequest,
) (*driver.SearchResponse, error) {
	return cast[*driver.SearchResponse](a.do(ctx, "SearchDocuments", index, func() (any, error) {
		return a.drv.SearchDocuments(ctx, service, index, req)
	}))
}

func (a *AzureSearch) SuggestDocuments(
	ctx context.Context, service, index, searchText, suggester string, top int,
) ([]driver.SuggestResult, error) {
	return cast[[]driver.SuggestResult](a.do(ctx, "SuggestDocuments", index, func() (any, error) {
		return a.drv.SuggestDocuments(ctx, service, index, searchText, suggester, top)
	}))
}

func (a *AzureSearch) AutocompleteDocuments(
	ctx context.Context, service, index, searchText, suggester string, top int,
) ([]string, error) {
	return cast[[]string](a.do(ctx, "AutocompleteDocuments", index, func() (any, error) {
		return a.drv.AutocompleteDocuments(ctx, service, index, searchText, suggester, top)
	}))
}

func (a *AzureSearch) CountDocuments(ctx context.Context, service, index string) (int64, error) {
	return cast[int64](a.do(ctx, "CountDocuments", index, func() (any, error) { return a.drv.CountDocuments(ctx, service, index) }))
}

func (a *AzureSearch) GetDocument(ctx context.Context, service, index, key string) (map[string]any, error) {
	return cast[map[string]any](a.do(ctx, "GetDocument", key, func() (any, error) { return a.drv.GetDocument(ctx, service, index, key) }))
}

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (a *AzureSearch) CreateOrUpdateIndexer(ctx context.Context, service string, cfg driver.IndexerConfig) (*driver.Indexer, error) {
	return cast[*driver.Indexer](a.do(ctx, "CreateOrUpdateIndexer", cfg.Name, func() (any, error) {
		return a.drv.CreateOrUpdateIndexer(ctx, service, cfg)
	}))
}

func (a *AzureSearch) GetIndexer(ctx context.Context, service, name string) (*driver.Indexer, error) {
	return cast[*driver.Indexer](a.do(ctx, "GetIndexer", name, func() (any, error) { return a.drv.GetIndexer(ctx, service, name) }))
}

func (a *AzureSearch) ListIndexers(ctx context.Context, service string) ([]driver.Indexer, error) {
	return cast[[]driver.Indexer](a.do(ctx, "ListIndexers", service, func() (any, error) { return a.drv.ListIndexers(ctx, service) }))
}

func (a *AzureSearch) DeleteIndexer(ctx context.Context, service, name string) error {
	return a.act(ctx, "DeleteIndexer", name, func() error { return a.drv.DeleteIndexer(ctx, service, name) })
}

func (a *AzureSearch) RunIndexer(ctx context.Context, service, name string) error {
	return a.act(ctx, "RunIndexer", name, func() error { return a.drv.RunIndexer(ctx, service, name) })
}

func (a *AzureSearch) ResetIndexer(ctx context.Context, service, name string) error {
	return a.act(ctx, "ResetIndexer", name, func() error { return a.drv.ResetIndexer(ctx, service, name) })
}

func (a *AzureSearch) GetIndexerStatus(ctx context.Context, service, name string) (*driver.IndexerStatus, error) {
	return cast[*driver.IndexerStatus](a.do(ctx, "GetIndexerStatus", name, func() (any, error) {
		return a.drv.GetIndexerStatus(ctx, service, name)
	}))
}

//nolint:gocritic // ds matches the driver signature; forwarded unchanged.
func (a *AzureSearch) CreateOrUpdateDataSource(ctx context.Context, service string, ds driver.DataSource) (*driver.DataSource, error) {
	return cast[*driver.DataSource](a.do(ctx, "CreateOrUpdateDataSource", ds.Name, func() (any, error) {
		return a.drv.CreateOrUpdateDataSource(ctx, service, ds)
	}))
}

func (a *AzureSearch) GetDataSource(ctx context.Context, service, name string) (*driver.DataSource, error) {
	return cast[*driver.DataSource](a.do(ctx, "GetDataSource", name, func() (any, error) { return a.drv.GetDataSource(ctx, service, name) }))
}

func (a *AzureSearch) ListDataSources(ctx context.Context, service string) ([]driver.DataSource, error) {
	return cast[[]driver.DataSource](a.do(ctx, "ListDataSources", service, func() (any, error) { return a.drv.ListDataSources(ctx, service) }))
}

func (a *AzureSearch) DeleteDataSource(ctx context.Context, service, name string) error {
	return a.act(ctx, "DeleteDataSource", name, func() error { return a.drv.DeleteDataSource(ctx, service, name) })
}

func (a *AzureSearch) CreateOrUpdateSkillset(ctx context.Context, service string, sk driver.Skillset) (*driver.Skillset, error) {
	return cast[*driver.Skillset](a.do(ctx, "CreateOrUpdateSkillset", sk.Name, func() (any, error) {
		return a.drv.CreateOrUpdateSkillset(ctx, service, sk)
	}))
}

func (a *AzureSearch) GetSkillset(ctx context.Context, service, name string) (*driver.Skillset, error) {
	return cast[*driver.Skillset](a.do(ctx, "GetSkillset", name, func() (any, error) { return a.drv.GetSkillset(ctx, service, name) }))
}

func (a *AzureSearch) ListSkillsets(ctx context.Context, service string) ([]driver.Skillset, error) {
	return cast[[]driver.Skillset](a.do(ctx, "ListSkillsets", service, func() (any, error) { return a.drv.ListSkillsets(ctx, service) }))
}

func (a *AzureSearch) DeleteSkillset(ctx context.Context, service, name string) error {
	return a.act(ctx, "DeleteSkillset", name, func() error { return a.drv.DeleteSkillset(ctx, service, name) })
}

func (a *AzureSearch) CreateOrUpdateSynonymMap(ctx context.Context, service string, sm driver.SynonymMap) (*driver.SynonymMap, error) {
	return cast[*driver.SynonymMap](a.do(ctx, "CreateOrUpdateSynonymMap", sm.Name, func() (any, error) {
		return a.drv.CreateOrUpdateSynonymMap(ctx, service, sm)
	}))
}

func (a *AzureSearch) GetSynonymMap(ctx context.Context, service, name string) (*driver.SynonymMap, error) {
	return cast[*driver.SynonymMap](a.do(ctx, "GetSynonymMap", name, func() (any, error) { return a.drv.GetSynonymMap(ctx, service, name) }))
}

func (a *AzureSearch) ListSynonymMaps(ctx context.Context, service string) ([]driver.SynonymMap, error) {
	return cast[[]driver.SynonymMap](a.do(ctx, "ListSynonymMaps", service, func() (any, error) { return a.drv.ListSynonymMaps(ctx, service) }))
}

func (a *AzureSearch) DeleteSynonymMap(ctx context.Context, service, name string) error {
	return a.act(ctx, "DeleteSynonymMap", name, func() error { return a.drv.DeleteSynonymMap(ctx, service, name) })
}

func (a *AzureSearch) CreateOrUpdateAlias(ctx context.Context, service string, alias driver.Alias) (*driver.Alias, error) {
	return cast[*driver.Alias](a.do(ctx, "CreateOrUpdateAlias", alias.Name, func() (any, error) {
		return a.drv.CreateOrUpdateAlias(ctx, service, alias)
	}))
}

func (a *AzureSearch) GetAlias(ctx context.Context, service, name string) (*driver.Alias, error) {
	return cast[*driver.Alias](a.do(ctx, "GetAlias", name, func() (any, error) { return a.drv.GetAlias(ctx, service, name) }))
}

func (a *AzureSearch) ListAliases(ctx context.Context, service string) ([]driver.Alias, error) {
	return cast[[]driver.Alias](a.do(ctx, "ListAliases", service, func() (any, error) { return a.drv.ListAliases(ctx, service) }))
}

func (a *AzureSearch) DeleteAlias(ctx context.Context, service, name string) error {
	return a.act(ctx, "DeleteAlias", name, func() error { return a.drv.DeleteAlias(ctx, service, name) })
}

func (a *AzureSearch) GetServiceStatistics(ctx context.Context, service string) (*driver.ServiceStatistics, error) {
	return cast[*driver.ServiceStatistics](a.do(ctx, "GetServiceStatistics", service, func() (any, error) {
		return a.drv.GetServiceStatistics(ctx, service)
	}))
}
