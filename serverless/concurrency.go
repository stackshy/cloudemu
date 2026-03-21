package serverless

import (
	"context"

	"github.com/stackshy/cloudemu/serverless/driver"
)

// PutFunctionConcurrency sets reserved concurrency for a function.
func (s *Serverless) PutFunctionConcurrency(ctx context.Context, config driver.ConcurrencyConfig) error {
	_, err := s.do(ctx, "PutFunctionConcurrency", config, func() (any, error) {
		return nil, s.driver.PutFunctionConcurrency(ctx, config)
	})

	return err
}

// GetFunctionConcurrency retrieves the concurrency configuration for a function.
func (s *Serverless) GetFunctionConcurrency(
	ctx context.Context, functionName string,
) (*driver.ConcurrencyConfig, error) {
	out, err := s.do(ctx, "GetFunctionConcurrency", functionName, func() (any, error) {
		return s.driver.GetFunctionConcurrency(ctx, functionName)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.ConcurrencyConfig), nil
}

// DeleteFunctionConcurrency removes the concurrency configuration for a function.
func (s *Serverless) DeleteFunctionConcurrency(ctx context.Context, functionName string) error {
	_, err := s.do(ctx, "DeleteFunctionConcurrency", functionName, func() (any, error) {
		return nil, s.driver.DeleteFunctionConcurrency(ctx, functionName)
	})

	return err
}
