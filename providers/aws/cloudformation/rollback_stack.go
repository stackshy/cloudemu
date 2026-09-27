package cloudformation

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

const msgRollbackBadStatus = "Stack:%s is in %s state and can not be rolled back."

// RollbackStack rolls a stack whose last operation failed without a
// rollback back to its last stable state. A CREATE_FAILED stack has none,
// so its resources are deleted and it ends ROLLBACK_COMPLETE. An
// UPDATE_FAILED stack goes back to the template, parameters and resources
// it had before the failed update, and ends UPDATE_ROLLBACK_COMPLETE.
func (m *Mock) RollbackStack(ctx context.Context, in *cfn.RollbackStackInput) (string, error) {
	m.settle(ctx)

	sd, err := m.activeStack(in.StackName)
	if err != nil {
		return "", err
	}

	_, id := sd.identity()

	retry, err := sd.checkToken(in.ClientRequestToken, actionRollbackStack)
	if err != nil || retry {
		return id, err
	}

	op := &pendingOp{Kind: opRollbackCreate}
	next := cfn.StatusRollbackInProgress

	st := sd.status()

	switch st {
	case cfn.StatusCreateFailed:
	case cfn.StatusUpdateFailed:
		op = &pendingOp{Kind: opRollback, Prior: sd.stableState()}
		next = cfn.StatusUpdateRollbackInProgress
	default:
		return "", cerrors.Newf(cerrors.InvalidArgument, msgRollbackBadStatus, id, st)
	}

	unchanged := func(s string) bool { return s == st }

	if berr := m.beginOperation(sd, unchanged, next, in.ClientRequestToken, actionRollbackStack); berr != nil {
		return "", berr
	}

	sd.mu.Lock()
	sd.stack.RetainExceptOnCreate = in.RetainExceptOnCreate
	sd.mu.Unlock()

	if op.Kind == opRollback {
		op.Replaced = sd.takeAllRetained()
	}

	m.finish(ctx, sd, op)

	return id, nil
}

// completeRollbackCreate deletes what a failed create made.
func (m *Mock) completeRollbackCreate(ctx context.Context, sd *stackData) {
	if tf := m.teardown(ctx, sd, teardownOpts{rollbackOfCreate: true}); len(tf) > 0 {
		m.emitStackEvent(sd, cfn.StatusRollbackFailed, failureSummary(tf))
		return
	}

	m.emitStackEvent(sd, cfn.StatusRollbackComplete, "")
}

// stableState returns the stack's last stable state, or its current one
// when none was recorded, such as after a restore of an older snapshot.
func (sd *stackData) stableState() *storedPrior {
	sd.mu.RLock()
	stable := sd.stable
	sd.mu.RUnlock()

	if stable != nil {
		return stable
	}

	cur := sd.priorState()

	return storePrior(&cur)
}

// keepStable records the state before the first of a run of failed updates
// that were not rolled back.
func (sd *stackData) keepStable(p *storedPrior) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.stable == nil {
		sd.stable = p
	}
}

// clearStable forgets the stable state once the stack reaches a new one.
func (sd *stackData) clearStable() {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.stable = nil
}

// takeAllRetained removes and returns every retained old resource, which a
// rollback puts back in place.
func (sd *stackData) takeAllRetained() []retainedResource {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	out := sd.retained
	sd.retained = nil

	return out
}
