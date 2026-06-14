package chaos

import (
	"context"

	"github.com/stackshy/cloudemu/azuresearch/driver"
)

// chaosAzureSearch wraps an Azure AI Search service. It consults the engine on
// the calls most worth failing in tests — service and index creation, document
// indexing, and the query runtime — and delegates every other operation through
// the embedded driver.AzureSearch unchanged.
type chaosAzureSearch struct {
	driver.AzureSearch
	engine *Engine
}

// WrapAzureSearch returns an Azure AI Search service that injects chaos on
// creation and runtime calls. service+operation pairs use the "azuresearch"
// service name.
func WrapAzureSearch(inner driver.AzureSearch, engine *Engine) driver.AzureSearch {
	return &chaosAzureSearch{AzureSearch: inner, engine: engine}
}

//nolint:gocritic // cfg matches the driver signature; delegated unchanged on success.
func (c *chaosAzureSearch) CreateService(ctx context.Context, cfg driver.ServiceConfig) (*driver.Service, error) {
	if err := applyChaos(ctx, c.engine, "azuresearch", "CreateService"); err != nil {
		return nil, err
	}

	return c.AzureSearch.CreateService(ctx, cfg)
}

func (c *chaosAzureSearch) CreateOrUpdateIndex(ctx context.Context, service string, idx driver.Index) (*driver.Index, error) {
	if err := applyChaos(ctx, c.engine, "azuresearch", "CreateOrUpdateIndex"); err != nil {
		return nil, err
	}

	return c.AzureSearch.CreateOrUpdateIndex(ctx, service, idx)
}

func (c *chaosAzureSearch) IndexDocuments(
	ctx context.Context, service, index string, actions []driver.IndexAction,
) ([]driver.IndexResult, error) {
	if err := applyChaos(ctx, c.engine, "azuresearch", "IndexDocuments"); err != nil {
		return nil, err
	}

	return c.AzureSearch.IndexDocuments(ctx, service, index, actions)
}

//nolint:gocritic // req matches the driver signature; delegated unchanged on success.
func (c *chaosAzureSearch) SearchDocuments(
	ctx context.Context, service, index string, req driver.SearchRequest,
) (*driver.SearchResponse, error) {
	if err := applyChaos(ctx, c.engine, "azuresearch", "SearchDocuments"); err != nil {
		return nil, err
	}

	return c.AzureSearch.SearchDocuments(ctx, service, index, req)
}
