package lambda

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/serverless/driver"
)

// PutFunctionConcurrency sets reserved concurrency for a function.
func (m *Mock) PutFunctionConcurrency(_ context.Context, cfg driver.ConcurrencyConfig) error {
	fd, ok := m.funcs.Get(cfg.FunctionName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", cfg.FunctionName)
	}

	fd.concurrency = &driver.ConcurrencyConfig{
		FunctionName:                 cfg.FunctionName,
		ReservedConcurrentExecutions: cfg.ReservedConcurrentExecutions,
	}
	m.funcs.Set(cfg.FunctionName, fd)

	return nil
}

// GetFunctionConcurrency retrieves the concurrency configuration for a function.
func (m *Mock) GetFunctionConcurrency(_ context.Context, functionName string) (*driver.ConcurrencyConfig, error) {
	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	if fd.concurrency == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "no concurrency config for function %s", functionName)
	}

	result := *fd.concurrency

	return &result, nil
}

// DeleteFunctionConcurrency removes the concurrency configuration for a function.
func (m *Mock) DeleteFunctionConcurrency(_ context.Context, functionName string) error {
	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	fd.concurrency = nil
	m.funcs.Set(functionName, fd)

	return nil
}
