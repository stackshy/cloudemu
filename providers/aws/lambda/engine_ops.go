package lambda

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
	"github.com/stackshy/cloudemu/v2/services/serverless/funcengine"
)

// Function-engine calls (Deploy on create and code update, Remove on delete)
// can take seconds with a real engine, so they never run under m.mu. Each one
// reserves the function name in m.engineOps first, calls the engine unlocked,
// then applies its result to the entry as it is after the call. The release
// is deferred right after reserving, so an engine call that panics cannot
// leave the name reserved.
//
// While a name is reserved for a code update, UpdateFunctionCode,
// UpdateFunctionConfiguration, PublishVersion and TagResource are refused
// with ResourceConflictException, as real Lambda does while LastUpdateStatus
// is InProgress. DeleteFunction is allowed and wins: the update's finalize
// finds the entry gone and removes its deployment.

// reserve marks name as having an engine call in flight and returns the
// reservation's token. Callers hold m.mu.
func (m *Mock) reserve(name string) uint64 {
	m.engineSeq++
	m.engineOps[name] = m.engineSeq

	return m.engineSeq
}

// engineBusy reports whether name has an engine call in flight. Callers hold
// m.mu.
func (m *Mock) engineBusy(name string) bool {
	_, ok := m.engineOps[name]

	return ok
}

// release drops name's reservation if it is still the one token made; a
// Restore in between may have cleared it, and a later call may own the name.
func (m *Mock) release(name string, token uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.engineOps[name] == token {
		delete(m.engineOps, name)
	}
}

// removeDeployment tears down name's engine deployment on a cleanup path,
// where the caller has nothing better to do with a failure.
func (m *Mock) removeDeployment(ctx context.Context, name string) {
	_ = funcengine.Remove(ctx, m.opts.FunctionEngine, name)
}

// updateInProgress is the ResourceConflictException Lambda returns for a
// change to a function whose previous update has not finished.
func updateInProgress(arn string) error {
	return cerrors.Newf(cerrors.AlreadyExists,
		"The operation cannot be performed at this time. An update is in progress for resource: %s", arn)
}

// deployNewFunction reserves cfg.Name, deploys it to the engine unlocked and
// stores the new function. If a Restore put an entry under the name while
// the deploy ran, that entry is kept and the deployment is removed.
//
//nolint:gocritic // hugeParam: info is copied into the entry.
func (m *Mock) deployNewFunction(ctx context.Context, cfg *driver.FunctionConfig, info driver.FunctionInfo) (*driver.FunctionInfo, error) {
	m.mu.Lock()

	if _, ok := m.funcs.Get(cfg.Name); ok || m.engineBusy(cfg.Name) {
		m.mu.Unlock()
		return nil, cerrors.Newf(cerrors.AlreadyExists, "function %s already exists", cfg.Name)
	}

	token := m.reserve(cfg.Name)
	m.mu.Unlock()

	defer m.release(cfg.Name, token)

	backed, err := funcengine.Deploy(ctx, m.opts.FunctionEngine, cfg)
	if err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "deploy function %s: %v", cfg.Name, err)
	}

	m.mu.Lock()

	_, appeared := m.funcs.Get(cfg.Name)
	if !appeared {
		m.funcs.Set(cfg.Name, m.newFuncData(info, backed))
	}

	m.mu.Unlock()

	if appeared {
		if backed {
			m.removeDeployment(ctx, cfg.Name)
		}

		return nil, cerrors.Newf(cerrors.AlreadyExists, "function %s already exists", cfg.Name)
	}

	result := info

	return &result, nil
}

// DeleteFunction removes the function and its qualifier state, then tears
// down its engine deployment outside m.mu with the name still reserved, so a
// CreateFunction of the same name cannot deploy while the old one is removed.
// During an in-flight code update the delete wins and the update's finalize
// removes the deployment instead.
func (m *Mock) DeleteFunction(ctx context.Context, name string) error {
	m.mu.Lock()

	fd, ok := m.funcs.Get(name)
	if !ok {
		m.mu.Unlock()
		return cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	m.funcs.Delete(name)

	if m.engineBusy(name) || !fd.engineBacked {
		m.mu.Unlock()
		return nil
	}

	token := m.reserve(name)
	m.mu.Unlock()

	defer m.release(name, token)

	if err := funcengine.Remove(ctx, m.opts.FunctionEngine, name); err != nil {
		return cerrors.Newf(cerrors.Internal, "remove function %s: %v", name, err)
	}

	return nil
}

//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) UpdateFunction(ctx context.Context, name string, cfg driver.FunctionConfig) (*driver.FunctionInfo, error) {
	if len(cfg.Code) == 0 {
		m.mu.Lock()
		defer m.mu.Unlock()

		return m.commitUpdate(name, cfg, nil)
	}

	// A code update re-deploys to the engine using the post-merge runtime and
	// handler, so the function runs the new code, not the stale deployment.
	info, token, err := m.reserveCodeUpdate(name, cfg)
	if err != nil {
		return nil, err
	}

	defer m.release(name, token)

	backed, err := funcengine.Deploy(ctx, m.opts.FunctionEngine, &driver.FunctionConfig{
		Name: name, Runtime: info.Runtime, Handler: info.Handler,
		Code: cfg.Code, Environment: info.Environment, Timeout: info.Timeout,
	})

	m.mu.Lock()

	if _, ok := m.funcs.Get(name); !ok {
		m.mu.Unlock()
		// DeleteFunction ran during the deploy and left the engine cleanup
		// to this call.
		m.removeDeployment(ctx, name)

		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	defer m.mu.Unlock()

	if err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "deploy function %s: %v", name, err)
	}

	return m.commitUpdate(name, cfg, &backed)
}

// reserveCodeUpdate validates a code update against the current function and
// reserves the function for the engine deploy. It returns the merged
// configuration to deploy and the reservation token.
//
//nolint:gocritic // hugeParam: cfg is the by-value driver config.
func (m *Mock) reserveCodeUpdate(name string, cfg driver.FunctionConfig) (driver.FunctionInfo, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(name)
	if !ok {
		return driver.FunctionInfo{}, 0, cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	if m.engineBusy(name) {
		return driver.FunctionInfo{}, 0, updateInProgress(fd.info.ARN)
	}

	info, err := m.mergeUpdate(fd.info, cfg)
	if err != nil {
		return driver.FunctionInfo{}, 0, err
	}

	return info, m.reserve(name), nil
}
