package serverless

import (
	"context"

	"github.com/stackshy/cloudemu/serverless/driver"
)

// PublishLayerVersion publishes a new version of a layer.
//
//nolint:gocritic // config passed by value to match driver.Serverless interface pattern
func (s *Serverless) PublishLayerVersion(ctx context.Context, config driver.LayerConfig) (*driver.LayerVersion, error) {
	out, err := s.do(ctx, "PublishLayerVersion", config, func() (any, error) {
		return s.driver.PublishLayerVersion(ctx, config)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.LayerVersion), nil
}

// GetLayerVersion retrieves a specific version of a layer.
func (s *Serverless) GetLayerVersion(ctx context.Context, name string, version int) (*driver.LayerVersion, error) {
	out, err := s.do(ctx, "GetLayerVersion", name, func() (any, error) {
		return s.driver.GetLayerVersion(ctx, name, version)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.LayerVersion), nil
}

// ListLayerVersions returns all versions of a layer.
func (s *Serverless) ListLayerVersions(ctx context.Context, name string) ([]driver.LayerVersion, error) {
	out, err := s.do(ctx, "ListLayerVersions", name, func() (any, error) {
		return s.driver.ListLayerVersions(ctx, name)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.LayerVersion), nil
}

// DeleteLayerVersion removes a specific version of a layer.
func (s *Serverless) DeleteLayerVersion(ctx context.Context, name string, version int) error {
	_, err := s.do(ctx, "DeleteLayerVersion", name, func() (any, error) {
		return nil, s.driver.DeleteLayerVersion(ctx, name, version)
	})

	return err
}

// ListLayers returns the latest version of each layer.
func (s *Serverless) ListLayers(ctx context.Context) ([]driver.LayerVersion, error) {
	out, err := s.do(ctx, "ListLayers", nil, func() (any, error) {
		return s.driver.ListLayers(ctx)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.LayerVersion), nil
}
