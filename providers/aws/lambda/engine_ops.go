package lambda

import (
	"context"
	"slices"

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

// removeDeployments tears down the engine deployments under keys on a cleanup
// path, where the caller has nothing better to do with a failure. The function
// no longer serves any of keys, so no new invocation pins them; each is removed
// once the invocations already running on it finish. Callers hold the
// function's engine reservation, not m.mu.
func (m *Mock) removeDeployments(ctx context.Context, keys []string) {
	for _, key := range keys {
		m.pins.wait(key)
		_ = funcengine.Remove(ctx, m.opts.FunctionEngine, key)
	}
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
		m.beginCreateSettle(cfg.Name)
	}

	m.mu.Unlock()

	if appeared {
		if backed {
			m.removeDeployments(ctx, []string{cfg.Name})
		}

		return nil, cerrors.Newf(cerrors.AlreadyExists, "function %s already exists", cfg.Name)
	}

	result := info
	m.observe(&result)

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
		return m.errFunctionNotFound(ctx, name, "")
	}

	m.funcs.Delete(name)
	m.clearSettle(name)

	if m.engineBusy(name) || !fd.engineBacked {
		m.mu.Unlock()
		return nil
	}

	token := m.reserve(name)
	m.mu.Unlock()

	defer m.release(name, token)

	// Besides the live deployment, a function updated under AsyncSettle may
	// hold its prior revision's and retired deployments.
	for _, key := range fd.engineKeys() {
		if err := funcengine.Remove(ctx, m.opts.FunctionEngine, key); err != nil {
			return cerrors.Newf(cerrors.Internal, "remove function %s: %v", name, err)
		}
	}

	return nil
}

//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) UpdateFunction(ctx context.Context, name string, cfg driver.FunctionConfig) (*driver.FunctionInfo, error) {
	if len(cfg.Code) == 0 {
		m.mu.Lock()
		defer m.mu.Unlock()

		return m.commitUpdate(ctx, name, cfg, nil)
	}

	// A code update re-deploys to the engine using the post-merge runtime and
	// handler, so the function runs the new code, not the stale deployment.
	plan, err := m.reserveCodeUpdate(ctx, name, cfg)
	if err != nil {
		return nil, err
	}

	defer m.release(name, plan.token)

	backed, err := funcengine.Deploy(ctx, m.opts.FunctionEngine, &driver.FunctionConfig{
		Name: plan.key, Runtime: plan.info.Runtime, Handler: plan.info.Handler,
		Code: cfg.Code, Environment: plan.info.Environment, Timeout: plan.info.Timeout,
	})

	info, stale, err := m.finalizeCodeUpdate(ctx, name, cfg, plan, backed, err)

	// The reservation is still held, so no other engine call for name runs
	// while the deployments that no longer serve anything are removed.
	m.removeDeployments(ctx, stale)

	return info, err
}

// finalizeCodeUpdate applies a code update's deploy result to the function as
// it is now, under m.mu. It returns the updated function and the engine keys
// to remove once m.mu is released: the deployments the function retired, or,
// when DeleteFunction ran during the deploy, every deployment the deleted
// function held plus the new one.
//
//nolint:gocritic // hugeParam: cfg is the by-value driver config.
func (m *Mock) finalizeCodeUpdate(
	ctx context.Context, name string, cfg driver.FunctionConfig, plan *codeUpdatePlan, backed bool, deployErr error,
) (*driver.FunctionInfo, []string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.funcs.Get(name); !ok {
		// DeleteFunction ran during the deploy and left the engine cleanup
		// to this call.
		stale := plan.held
		if !slices.Contains(stale, plan.key) {
			stale = append(slices.Clone(stale), plan.key)
		}

		return nil, stale, m.errFunctionNotFound(ctx, name, "")
	}

	if deployErr != nil {
		// A failed deploy to a fresh AsyncSettle key may have left a partial
		// deployment; one to the live key is the function's current code.
		var partial []string
		if !slices.Contains(plan.held, plan.key) {
			partial = []string{plan.key}
		}

		return nil, partial, cerrors.Newf(cerrors.InvalidArgument, "deploy function %s: %v", name, deployErr)
	}

	info, err := m.commitUpdate(ctx, name, cfg, &codeDeploy{backed: backed, key: plan.key})
	if err != nil {
		return nil, nil, err
	}

	return info, m.takeRetired(name), nil
}

// takeRetired clears and returns function name's retired engine keys, which the
// caller removes while it holds the function's engine reservation. Callers
// hold m.mu.
func (m *Mock) takeRetired(name string) []string {
	fd, ok := m.funcs.Get(name)
	if !ok || len(fd.retired) == 0 {
		return nil
	}

	retired := fd.retired
	fd.retired = nil
	m.funcs.Set(name, fd)

	return retired
}

// codeUpdatePlan is a reserved code update: the merged configuration to deploy,
// the engine key to deploy it to, the engine keys the function held when it
// was reserved, and the reservation token.
type codeUpdatePlan struct {
	info  driver.FunctionInfo
	key   string
	held  []string
	token uint64
}

// reserveCodeUpdate validates a code update against the current function and
// reserves the function for the engine deploy.
//
//nolint:gocritic // hugeParam: cfg is the by-value driver config.
func (m *Mock) reserveCodeUpdate(
	ctx context.Context, name string, cfg driver.FunctionConfig,
) (*codeUpdatePlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(name)
	if !ok {
		return nil, m.errFunctionNotFound(ctx, name, "")
	}

	// Refused while Pending creation or while an update (an AsyncSettle window
	// or an engine deploy) is InProgress.
	if err := m.checkUpdatable(&fd); err != nil {
		return nil, err
	}

	info, err := m.mergeUpdate(fd.info, cfg)
	if err != nil {
		return nil, err
	}

	return &codeUpdatePlan{
		info: info, key: m.updateEngineKey(&fd), held: fd.engineKeys(), token: m.reserve(name),
	}, nil
}
