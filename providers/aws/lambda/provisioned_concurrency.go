package lambda

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// statusReady is the terminal Status a provisioned-concurrency allocation
// reports once it settles. The emulator has no real cold-start pool, so a Put
// allocates synchronously and is READY immediately — there is no IN_PROGRESS
// window to observe.
const statusReady = "READY"

// minProvisionedConcurrentExecutions is the minimum ProvisionedConcurrentExecutions
// PutProvisionedConcurrencyConfig accepts; a value below it is rejected with
// InvalidParameterValueException, matching real Lambda.
const minProvisionedConcurrentExecutions = 1

// PutFunctionProvisionedConcurrencyConfig sets (replacing any existing) the
// provisioned-concurrency configuration for a published version or alias
// qualifier. Real Lambda rejects an unqualified/$LATEST target — provisioned
// concurrency can only attach to an immutable qualifier, because $LATEST's
// code can change underneath it — and rejects a requested amount that exceeds
// the function's reserved concurrency (when reserved concurrency is
// configured), since provisioned concurrency is carved out of that budget.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) PutFunctionProvisionedConcurrencyConfig(
	_ context.Context, cfg driver.ProvisionedConcurrencyConfig,
) (*driver.ProvisionedConcurrencyConfig, error) {
	if cfg.RequestedProvisionedConcurrentExecutions < minProvisionedConcurrentExecutions {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"ProvisionedConcurrentExecutions %d must be >= %d",
			cfg.RequestedProvisionedConcurrentExecutions, minProvisionedConcurrentExecutions)
	}

	if cfg.Qualifier == "" || cfg.Qualifier == latestVersion {
		return nil, cerrors.New(cerrors.InvalidArgument,
			"ProvisionedConcurrencyConfig cannot be applied to $LATEST; specify a published version or alias")
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

	if fd.concurrency != nil && cfg.RequestedProvisionedConcurrentExecutions > fd.concurrency.ReservedConcurrentExecutions {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"ProvisionedConcurrentExecutions %d exceeds the reserved concurrency (%d) configured for function %s",
			cfg.RequestedProvisionedConcurrentExecutions, fd.concurrency.ReservedConcurrentExecutions, cfg.FunctionName)
	}

	stored := driver.ProvisionedConcurrencyConfig{
		FunctionName:                             cfg.FunctionName,
		Qualifier:                                cfg.Qualifier,
		FunctionArn:                              qualifiedARN(fd.info.ARN, cfg.Qualifier),
		RequestedProvisionedConcurrentExecutions: cfg.RequestedProvisionedConcurrentExecutions,
		AvailableProvisionedConcurrentExecutions: cfg.RequestedProvisionedConcurrentExecutions,
		AllocatedProvisionedConcurrentExecutions: cfg.RequestedProvisionedConcurrentExecutions,
		Status:                                   statusReady,
		LastModified:                             m.opts.Clock.Now().UTC().Format(timeFormat),
	}

	setProvisionedConcurrencyConfig(&fd, stored)
	m.funcs.Set(cfg.FunctionName, fd)

	result := stored

	return &result, nil
}

// GetFunctionProvisionedConcurrencyConfig returns the provisioned-concurrency
// config for a function's version/alias, or NotFound
// (ProvisionedConcurrencyConfigNotFoundException on the wire) when none is set.
func (m *Mock) GetFunctionProvisionedConcurrencyConfig(
	_ context.Context, functionName, qualifier string,
) (*driver.ProvisionedConcurrencyConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	cfg, ok := fd.provisionedConcurrencyConfigs[qualifier]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound,
			"no provisioned concurrency config for function %s qualifier %s", functionName, qualifier)
	}

	result := cfg

	return &result, nil
}

// DeleteFunctionProvisionedConcurrencyConfig removes the provisioned-concurrency
// config for a function's version/alias.
func (m *Mock) DeleteFunctionProvisionedConcurrencyConfig(_ context.Context, functionName, qualifier string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	if _, exists := fd.provisionedConcurrencyConfigs[qualifier]; !exists {
		return cerrors.Newf(cerrors.NotFound,
			"no provisioned concurrency config for function %s qualifier %s", functionName, qualifier)
	}

	next := make(map[string]driver.ProvisionedConcurrencyConfig, len(fd.provisionedConcurrencyConfigs))

	for k, v := range fd.provisionedConcurrencyConfigs {
		if k != qualifier {
			next[k] = v
		}
	}

	fd.provisionedConcurrencyConfigs = next
	m.funcs.Set(functionName, fd)

	return nil
}

// ListFunctionProvisionedConcurrencyConfigs returns every provisioned-concurrency
// config set on a function (one per qualifier), backing AWS
// ListProvisionedConcurrencyConfigs.
func (m *Mock) ListFunctionProvisionedConcurrencyConfigs(
	_ context.Context, functionName string,
) ([]driver.ProvisionedConcurrencyConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	out := make([]driver.ProvisionedConcurrencyConfig, 0, len(fd.provisionedConcurrencyConfigs))
	for _, cfg := range fd.provisionedConcurrencyConfigs {
		out = append(out, cfg)
	}

	return out, nil
}

// setProvisionedConcurrencyConfig stores cfg under its qualifier key using
// copy-on-write: a fresh map is built so a concurrent reader holding an
// earlier funcData copy still sees an immutable snapshot (-race clean).
//
//nolint:gocritic // hugeParam: cfg mirrors the driver value type stored by value.
func setProvisionedConcurrencyConfig(fd *funcData, cfg driver.ProvisionedConcurrencyConfig) {
	next := make(map[string]driver.ProvisionedConcurrencyConfig, len(fd.provisionedConcurrencyConfigs)+1)

	for k, v := range fd.provisionedConcurrencyConfigs {
		next[k] = v
	}

	next[cfg.Qualifier] = cfg
	fd.provisionedConcurrencyConfigs = next
}
