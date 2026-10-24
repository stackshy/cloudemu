package lambda

import (
	"context"
	"maps"
	"slices"

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
// completes, and returns it. A no-op returning the inactive window unless
// AsyncSettle is on.
func (m *Mock) beginUpdateSettle(name string) settle.Window {
	m.updateSettle.Begin(name, lastUpdateInProgress, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultLambdaUpdateSettle))

	return m.updateSettle.Window(name)
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
// LastUpdateStatus InProgress while an update settles, else Successful. While
// an update is InProgress, real Lambda gives LastUpdateStatusReason "The
// function is being created." and LastUpdateStatusReasonCode "Creating", the
// same text as a Pending function's StateReason.
func (m *Mock) observe(info *driver.FunctionInfo) {
	info.LastUpdateStatusReason = ""
	info.LastUpdateStatusReasonCode = ""

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

	if info.LastUpdateStatus == lastUpdateInProgress {
		info.LastUpdateStatusReason = stateReasonCreating
		info.LastUpdateStatusReasonCode = stateReasonCodeCreating
	}
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

// priorRevision is what Invoke of $LATEST runs while an update is InProgress:
// the function's configuration, engine deployment and AWS-only settings as they
// were before the update. Real Lambda documents that "invocations continue to
// use the function's previous code and configuration until the update
// completes". See
// https://docs.aws.amazon.com/lambda/latest/dg/functions-states.html
type priorRevision struct {
	info         driver.FunctionInfo
	engineBacked bool
	engineName   string
	awsConfig    driver.AWSFunctionConfig
	// window is the InProgress window of the update that replaced this
	// revision. Keeping it here, rather than asking the shared settle set,
	// makes a funcData copy self-consistent: a copy taken before a later update
	// was committed never serves a revision that update retired.
	window settle.Window
}

// engineKey is the FunctionEngine key fd's current code is deployed under.
func (fd *funcData) engineKey() string {
	if fd.engineName != "" {
		return fd.engineName
	}

	return fd.info.Name
}

// engineNameFor is the funcData.engineName value for engine key key of function
// name: "" when the code is deployed under the function name itself.
func engineNameFor(name, key string) string {
	if key == name {
		return ""
	}

	return key
}

// updatesSettle reports whether an update opens an InProgress window, i.e.
// whether AsyncSettle is on.
func (m *Mock) updatesSettle() bool {
	return m.opts.SettleDuration(settle.DefaultLambdaUpdateSettle) > 0
}

// updateEngineKey is the engine key a code update of fd deploys to. By default
// it replaces the current deployment in place. Under AsyncSettle it is a fresh
// key, so the current deployment keeps serving invocations until the update
// completes. The suffix uses '.', which a Lambda function name cannot contain,
// so the key cannot collide with another function's.
func (m *Mock) updateEngineKey(fd *funcData) string {
	if !m.updatesSettle() {
		return fd.engineKey()
	}

	return fd.info.Name + ".update-" + newRevisionID()
}

// keepPrior records fd as it is now as the revision Invoke serves until the
// update being committed completes. A no-op unless AsyncSettle is on. The
// previous prior's update has completed (an update is refused while another is
// InProgress), so its engine deployment is retired unless it is still the live
// one. window is the InProgress window of the update being committed. Callers
// hold m.mu and write fd back.
func (m *Mock) keepPrior(fd *funcData, window settle.Window) {
	if !m.updatesSettle() {
		return
	}

	if p := fd.prior; p != nil {
		fd.retired = retireEngineKey(fd.retired, p.engineKey(fd.info.Name), fd.engineKey())
	}

	info := fd.info
	info.Environment = maps.Clone(info.Environment)
	info.Tags = maps.Clone(info.Tags)

	fd.prior = &priorRevision{
		info: info, engineBacked: fd.engineBacked, engineName: fd.engineName,
		awsConfig: cloneAWSConfig(&fd.awsConfig), window: window,
	}
}

// engineKey is the engine key the prior revision of function name runs under.
func (p *priorRevision) engineKey(name string) string {
	if p.engineName != "" {
		return p.engineName
	}

	return name
}

// retireEngineKey returns retired with key added, unless key is the live key or
// already listed. It never appends in place, so a reader holding an earlier
// funcData copy keeps its own slice.
func retireEngineKey(retired []string, key, live string) []string {
	if key == live || slices.Contains(retired, key) {
		return retired
	}

	return append(slices.Clone(retired), key)
}

// retiredWithPrior is fd.retired with the prior revision's engine key added,
// for a snapshot: the prior revision is not persisted, so after a restore its
// deployment no longer serves anything and must be removed like a retired one.
func (fd *funcData) retiredWithPrior() []string {
	if fd.prior == nil {
		return fd.retired
	}

	return retireEngineKey(fd.retired, fd.prior.engineKey(fd.info.Name), fd.engineKey())
}

// engineKeys lists every engine key fd holds a deployment under: the live one,
// the prior revision's and the retired ones, without duplicates.
func (fd *funcData) engineKeys() []string {
	keys := []string{fd.engineKey()}

	if fd.prior != nil {
		keys = append(keys, fd.prior.engineKey(fd.info.Name))
	}

	keys = append(keys, fd.retired...)
	slices.Sort(keys)

	return slices.Compact(keys)
}

// servePrior turns fd, a caller's copy, into what an invocation of
// executedVersion runs: for $LATEST while an update is InProgress, the code and
// configuration from before the update. Published versions are immutable and
// never affected. It reads only fd, so a copy taken at any moment serves a
// revision that copy still holds.
func (m *Mock) servePrior(fd *funcData, executedVersion string) {
	if executedVersion != latestVersion || fd.prior == nil || fd.prior.window.Settled(m.opts.Clock.Now()) {
		return
	}

	p := fd.prior
	fd.info = p.info
	fd.engineBacked = p.engineBacked
	fd.engineName = p.engineName
	fd.awsConfig = p.awsConfig
}
