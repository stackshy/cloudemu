package serverless

import (
	"context"

	"github.com/stackshy/cloudemu/serverless/driver"
)

// PublishVersion publishes a new immutable version of a function.
func (s *Serverless) PublishVersion(ctx context.Context, functionName, description string) (*driver.FunctionVersion, error) {
	out, err := s.do(ctx, "PublishVersion", functionName, func() (any, error) {
		return s.driver.PublishVersion(ctx, functionName, description)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.FunctionVersion), nil
}

// ListVersions returns all published versions for a function.
func (s *Serverless) ListVersions(ctx context.Context, functionName string) ([]driver.FunctionVersion, error) {
	out, err := s.do(ctx, "ListVersions", functionName, func() (any, error) {
		return s.driver.ListVersions(ctx, functionName)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.FunctionVersion), nil
}
