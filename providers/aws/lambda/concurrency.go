package lambda

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
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

// reserveInvocationSlot enforces reserved concurrency for an invoke. A function
// with no reserved limit is unbounded, so it returns a no-op release. When the
// reserved limit is exhausted it returns a Throttled error the wire layer maps
// to a 429 TooManyRequestsException. Otherwise it reserves a slot and returns
// the release the caller defers to return it on completion.
func (m *Mock) reserveInvocationSlot(fd *funcData, functionName string) (func(), error) {
	if fd.concurrency == nil {
		return func() {}, nil
	}

	release, ok := m.acquireConcurrency(functionName, fd.concurrency.ReservedConcurrentExecutions)
	if !ok {
		return nil, errReservedConcurrencyExceeded(functionName)
	}

	return release, nil
}

// acquireConcurrency reserves an execution slot for a function that has reserved
// concurrency configured. It returns false when granting the slot would push the
// in-flight count past reserved (the caller must throttle the invoke); otherwise
// it increments the count and returns a release func the caller defers to return
// the slot when the invocation completes. A reserved value of 0 always fails,
// throttling every invoke. The map read-modify-write is guarded by inflightMu so
// concurrent invokes account correctly.
func (m *Mock) acquireConcurrency(functionName string, reserved int) (func(), bool) {
	m.inflightMu.Lock()
	defer m.inflightMu.Unlock()

	if m.inflight[functionName] >= reserved {
		return nil, false
	}

	m.inflight[functionName]++

	return func() {
		m.inflightMu.Lock()
		defer m.inflightMu.Unlock()

		m.inflight[functionName]--
		if m.inflight[functionName] <= 0 {
			delete(m.inflight, functionName)
		}
	}, true
}

// errReservedConcurrencyExceeded is the throttling error Invoke returns when a
// function's reserved-concurrency limit is exhausted. The wire layer maps a
// Throttled Lambda error to a 429 TooManyRequestsException whose Reason is
// ReservedFunctionConcurrentInvocationLimitExceeded, matching real Lambda.
func errReservedConcurrencyExceeded(functionName string) error {
	return cerrors.Newf(cerrors.Throttled,
		"rate exceeded for function %s: reserved concurrency limit reached", functionName)
}
