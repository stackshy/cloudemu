package lambda

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// AWS async-invoke config bounds (PutFunctionEventInvokeConfig): retries are
// 0-2, and the maximum event age is 60-21600 seconds (1 minute to 6 hours). An
// out-of-range value is rejected with InvalidParameterValueException.
const (
	minRetryAttempts   = 0
	maxRetryAttempts   = 2
	minEventAgeSeconds = 60
	maxEventAgeSeconds = 21600
)

// PutFunctionEventInvokeConfig sets (replacing any existing) the asynchronous-
// invocation configuration for a function version/alias: MaximumRetryAttempts,
// MaximumEventAgeInSeconds and the OnSuccess/OnFailure DestinationConfig. It
// backs AWS PutFunctionEventInvokeConfig (Terraform
// aws_lambda_function_event_invoke_config).
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) PutFunctionEventInvokeConfig(
	_ context.Context, cfg driver.EventInvokeConfig,
) (*driver.EventInvokeConfig, error) {
	if err := validateEventInvokeConfig(cfg); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(cfg.FunctionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", cfg.FunctionName)
	}

	if _, err := m.resolveQualifier(&fd, cfg.Qualifier, nil); err != nil {
		return nil, err
	}

	stored := driver.EventInvokeConfig{
		FunctionName:             cfg.FunctionName,
		Qualifier:                cfg.Qualifier,
		FunctionArn:              qualifiedARN(fd.info.ARN, cfg.Qualifier),
		MaximumRetryAttempts:     cloneIntPtr(cfg.MaximumRetryAttempts),
		MaximumEventAgeInSeconds: cloneIntPtr(cfg.MaximumEventAgeInSeconds),
		DestinationConfig:        cloneDestinationConfig(cfg.DestinationConfig),
		LastModified:             m.opts.Clock.Now().UTC().Format(timeFormat),
	}

	setEventInvokeConfig(&fd, stored)
	m.funcs.Set(cfg.FunctionName, fd)

	result := cloneEventInvokeConfig(stored)

	return &result, nil
}

// UpdateFunctionEventInvokeConfig merges the supplied non-nil fields onto an
// existing async-invoke config (AWS UpdateFunctionEventInvokeConfig's PATCH
// semantics), leaving unset fields unchanged. An update with no prior config
// creates one, matching AWS.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) UpdateFunctionEventInvokeConfig(
	_ context.Context, cfg driver.EventInvokeConfig,
) (*driver.EventInvokeConfig, error) {
	if err := validateEventInvokeConfig(cfg); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(cfg.FunctionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", cfg.FunctionName)
	}

	if _, err := m.resolveQualifier(&fd, cfg.Qualifier, nil); err != nil {
		return nil, err
	}

	merged := lookupEventInvokeConfig(&fd, cfg.Qualifier)
	merged.FunctionName = cfg.FunctionName
	merged.Qualifier = cfg.Qualifier
	merged.FunctionArn = qualifiedARN(fd.info.ARN, cfg.Qualifier)

	if cfg.MaximumRetryAttempts != nil {
		merged.MaximumRetryAttempts = cloneIntPtr(cfg.MaximumRetryAttempts)
	}

	if cfg.MaximumEventAgeInSeconds != nil {
		merged.MaximumEventAgeInSeconds = cloneIntPtr(cfg.MaximumEventAgeInSeconds)
	}

	if cfg.DestinationConfig != nil {
		merged.DestinationConfig = cloneDestinationConfig(cfg.DestinationConfig)
	}

	merged.LastModified = m.opts.Clock.Now().UTC().Format(timeFormat)

	setEventInvokeConfig(&fd, merged)
	m.funcs.Set(cfg.FunctionName, fd)

	result := cloneEventInvokeConfig(merged)

	return &result, nil
}

// GetFunctionEventInvokeConfig returns the async-invoke config for a function
// version/alias, or NotFound (ResourceNotFoundException) when none is set.
func (m *Mock) GetFunctionEventInvokeConfig(
	_ context.Context, functionName, qualifier string,
) (*driver.EventInvokeConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	cfg, ok := fd.eventInvokeConfigs[policyKey(qualifier)]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound,
			"no event invoke config for function %s", functionName)
	}

	result := cloneEventInvokeConfig(cfg)

	return &result, nil
}

// DeleteFunctionEventInvokeConfig removes the async-invoke config for a function
// version/alias, reverting it to the default (2 retries, no destinations).
func (m *Mock) DeleteFunctionEventInvokeConfig(_ context.Context, functionName, qualifier string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	key := policyKey(qualifier)
	if _, exists := fd.eventInvokeConfigs[key]; !exists {
		return cerrors.Newf(cerrors.NotFound, "no event invoke config for function %s", functionName)
	}

	next := make(map[string]driver.EventInvokeConfig, len(fd.eventInvokeConfigs))

	for k, v := range fd.eventInvokeConfigs {
		if k != key {
			next[k] = v
		}
	}

	fd.eventInvokeConfigs = next
	m.funcs.Set(functionName, fd)

	return nil
}

// ListFunctionEventInvokeConfigs returns every async-invoke config set on a
// function (one per qualifier), backing AWS ListFunctionEventInvokeConfigs.
func (m *Mock) ListFunctionEventInvokeConfigs(
	_ context.Context, functionName string,
) ([]driver.EventInvokeConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	out := make([]driver.EventInvokeConfig, 0, len(fd.eventInvokeConfigs))
	for _, cfg := range fd.eventInvokeConfigs {
		out = append(out, cloneEventInvokeConfig(cfg))
	}

	return out, nil
}

// setEventInvokeConfig stores cfg under its qualifier key using copy-on-write: a
// fresh map is built so a concurrent Invoke reading an earlier funcData copy
// still sees an immutable snapshot (-race clean).
//
//nolint:gocritic // hugeParam: cfg mirrors the driver value type stored by value.
func setEventInvokeConfig(fd *funcData, cfg driver.EventInvokeConfig) {
	next := make(map[string]driver.EventInvokeConfig, len(fd.eventInvokeConfigs)+1)

	for k, v := range fd.eventInvokeConfigs {
		next[k] = v
	}

	next[policyKey(cfg.Qualifier)] = cfg
	fd.eventInvokeConfigs = next
}

// lookupEventInvokeConfig returns the async-invoke config for a qualifier, or a
// zero-value config (defaults) when none is set. Read-only; safe to call on a
// funcData copy without the mock lock.
func lookupEventInvokeConfig(fd *funcData, qualifier string) driver.EventInvokeConfig {
	return fd.eventInvokeConfigs[policyKey(qualifier)]
}

// validateEventInvokeConfig enforces the AWS bounds on retries and event age.
//
//nolint:gocritic // hugeParam: cfg mirrors the method value receiver.
func validateEventInvokeConfig(cfg driver.EventInvokeConfig) error {
	if r := cfg.MaximumRetryAttempts; r != nil && (*r < minRetryAttempts || *r > maxRetryAttempts) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"MaximumRetryAttempts %d must be >= %d and <= %d", *r, minRetryAttempts, maxRetryAttempts)
	}

	if a := cfg.MaximumEventAgeInSeconds; a != nil && (*a < minEventAgeSeconds || *a > maxEventAgeSeconds) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"MaximumEventAgeInSeconds %d must be >= %d and <= %d", *a, minEventAgeSeconds, maxEventAgeSeconds)
	}

	return nil
}

func cloneIntPtr(p *int) *int {
	if p == nil {
		return nil
	}

	v := *p

	return &v
}

func cloneDestination(d *driver.Destination) *driver.Destination {
	if d == nil {
		return nil
	}

	return &driver.Destination{Destination: d.Destination}
}

func cloneDestinationConfig(dc *driver.DestinationConfig) *driver.DestinationConfig {
	if dc == nil {
		return nil
	}

	return &driver.DestinationConfig{
		OnSuccess: cloneDestination(dc.OnSuccess),
		OnFailure: cloneDestination(dc.OnFailure),
	}
}

// cloneEventInvokeConfig deep-copies a config so stored state and returned/
// snapshot copies never share the pointer fields.
//
//nolint:gocritic // hugeParam: cfg mirrors the driver value type.
func cloneEventInvokeConfig(cfg driver.EventInvokeConfig) driver.EventInvokeConfig {
	cfg.MaximumRetryAttempts = cloneIntPtr(cfg.MaximumRetryAttempts)
	cfg.MaximumEventAgeInSeconds = cloneIntPtr(cfg.MaximumEventAgeInSeconds)
	cfg.DestinationConfig = cloneDestinationConfig(cfg.DestinationConfig)

	return cfg
}
