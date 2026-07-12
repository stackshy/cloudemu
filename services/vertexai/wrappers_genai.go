package vertexai

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

func (v *VertexAI) GenerateContent(
	ctx context.Context, model string, req driver.GenerateContentRequest,
) (*driver.GenerateContentResponse, error) {
	return cast[*driver.GenerateContentResponse](v.do(ctx, "GenerateContent", model, func() (any, error) {
		return v.drv.GenerateContent(ctx, model, req)
	}))
}

func (v *VertexAI) CountTokens(
	ctx context.Context, model string, req driver.GenerateContentRequest,
) (*driver.CountTokensResponse, error) {
	return cast[*driver.CountTokensResponse](v.do(ctx, "CountTokens", model, func() (any, error) {
		return v.drv.CountTokens(ctx, model, req)
	}))
}

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (v *VertexAI) CreateCachedContent(ctx context.Context, cfg driver.CachedContentConfig) (*driver.CachedContent, error) {
	return cast[*driver.CachedContent](v.do(ctx, "CreateCachedContent", cfg, func() (any, error) {
		return v.drv.CreateCachedContent(ctx, cfg)
	}))
}

func (v *VertexAI) GetCachedContent(ctx context.Context, name string) (*driver.CachedContent, error) {
	return cast[*driver.CachedContent](v.do(ctx, "GetCachedContent", name, func() (any, error) {
		return v.drv.GetCachedContent(ctx, name)
	}))
}

func (v *VertexAI) ListCachedContents(ctx context.Context, location string) ([]driver.CachedContent, error) {
	return cast[[]driver.CachedContent](v.do(ctx, "ListCachedContents", location, func() (any, error) {
		return v.drv.ListCachedContents(ctx, location)
	}))
}

func (v *VertexAI) DeleteCachedContent(ctx context.Context, name string) error {
	_, err := v.do(ctx, "DeleteCachedContent", name, func() (any, error) { return nil, v.drv.DeleteCachedContent(ctx, name) })

	return err
}
