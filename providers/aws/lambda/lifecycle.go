package lambda

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// Function lifecycle values real Lambda reports on FunctionConfiguration. See
// https://docs.aws.amazon.com/lambda/latest/dg/functions-states.html and
// https://docs.aws.amazon.com/lambda/latest/api/API_FunctionConfiguration.html
const (
	stateActive  = "Active"
	statePending = "Pending"

	// stateReasonCreating / stateReasonCodeCreating are the StateReason and
	// StateReasonCode a function reports while it is Pending creation.
	stateReasonCreating     = "The function is being created."
	stateReasonCodeCreating = "Creating"

	lastUpdateSuccessful = "Successful"
	lastUpdateInProgress = "InProgress"
)

// errFunctionNotFound is the ResourceNotFoundException real Lambda returns for a
// function (or function version/alias) that does not exist. Its message names
// the full function ARN, qualified when the request named a version or alias:
// "Function not found: arn:aws:lambda:<region>:<account>:function:<name>[:<qualifier>]".
func (m *Mock) errFunctionNotFound(ctx context.Context, name, qualifier string) error {
	arn := m.regionalFunctionARN(ctx, name)
	if qualifier != "" {
		arn += ":" + qualifier
	}

	return cerrors.Newf(cerrors.NotFound, "Function not found: %s", arn)
}

// regionalFunctionARN is the unqualified ARN of function name in the request's region.
func (m *Mock) regionalFunctionARN(ctx context.Context, name string) string {
	return idgen.AWSARN("lambda", regionctx.RegionOr(ctx, m.opts.Region), m.opts.AccountID, "function:"+name)
}

// beginCreateSettle opens the Pending window a freshly created function reports
// until creation completes. With config.Options.AsyncSettle off (the default)
// SettleDuration is 0 and the window is inactive, so the function is Active
// immediately (the historical behavior).
func (m *Mock) beginCreateSettle(name string) {
	m.updateSettle.Clear(name)
	m.createSettle.Begin(name, statePending, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultLambdaCreateSettle))
}

// beginUpdateSettle opens the LastUpdateStatus=InProgress window an
// UpdateFunctionCode/UpdateFunctionConfiguration reports until the update
// completes. A no-op unless AsyncSettle is on.
func (m *Mock) beginUpdateSettle(name string) {
	m.updateSettle.Begin(name, lastUpdateInProgress, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultLambdaUpdateSettle))
}

// clearSettle drops a deleted function's settle windows.
func (m *Mock) clearSettle(name string) {
	m.createSettle.Clear(name)
	m.updateSettle.Clear(name)
}

// isPending reports whether function name is still Pending creation.
func (m *Mock) isPending(name string) bool {
	return !m.createSettle.Settled(name, m.opts.Clock.Now())
}

// isUpdating reports whether an update of function name is still InProgress.
func (m *Mock) isUpdating(name string) bool {
	return !m.updateSettle.Settled(name, m.opts.Clock.Now())
}

// observe overlays the function's lifecycle onto info: State Pending with the
// Creating reason (and no LastUpdateStatus, which AWS first sets once creation
// completes) while creation settles; otherwise the stored State with
// LastUpdateStatus InProgress while an update settles, else Successful.
func (m *Mock) observe(info *driver.FunctionInfo) {
	if m.isPending(info.Name) {
		info.State = statePending
		info.StateReason = stateReasonCreating
		info.StateReasonCode = stateReasonCodeCreating
		info.LastUpdateStatus = ""

		return
	}

	info.StateReason = ""
	info.StateReasonCode = ""
	info.LastUpdateStatus = m.updateSettle.State(info.Name, m.opts.Clock.Now(), lastUpdateSuccessful)
}

// errPending is the ResourceConflictException real Lambda returns for an
// operation on a function that is still Pending creation.
func errPending(arn string) error {
	return cerrors.Newf(cerrors.AlreadyExists,
		"The operation cannot be performed at this time. The resource %s is currently in the following state: "+
			"'%s'. StateReasonCode: '%s'", arn, statePending, stateReasonCodeCreating)
}

// checkUpdatable rejects UpdateFunctionCode, UpdateFunctionConfiguration and
// PublishVersion while the function is Pending creation or an update is still
// InProgress, with the ResourceConflictException real Lambda returns. See
// https://docs.aws.amazon.com/lambda/latest/dg/functions-states.html
func (m *Mock) checkUpdatable(fd *funcData) error {
	if m.isPending(fd.info.Name) {
		return errPending(fd.info.ARN)
	}

	return m.checkNotUpdating(fd)
}

// checkNotUpdating rejects an operation AWS blocks while an update is
// InProgress (it also blocks TagResource): either the AsyncSettle update window
// is still open or a code deploy is still running in the function engine.
// Callers hold m.mu (engineBusy reads engineOps).
func (m *Mock) checkNotUpdating(fd *funcData) error {
	if m.isUpdating(fd.info.Name) || m.engineBusy(fd.info.Name) {
		return updateInProgress(fd.info.ARN)
	}

	return nil
}

// checkInvocable rejects an invocation of a function that is still Pending
// creation. An in-progress update does not block invocations (AWS keeps
// serving them while the update settles).
func (m *Mock) checkInvocable(fd *funcData) error {
	if m.isPending(fd.info.Name) {
		return errPending(fd.info.ARN)
	}

	return nil
}
