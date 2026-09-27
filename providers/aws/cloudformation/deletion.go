package cloudformation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// Error texts of DeleteStack, CreateStack's failure options and the
// account limits.
const (
	msgTerminationProtected = "Stack [%s] cannot be deleted while TerminationProtection is enabled"
	msgRetainNotFailed      = "Invalid operation on stack [%s]. When you delete a stack, " +
		"specify which resources to retain only when the stack is in the DELETE_FAILED state."
	msgFailureAndRollback = "Either DisableRollback or OnFailure can be specified, not both."
	msgStackLimit         = "Limit for stacks has been exceeded"
)

// The account limits DescribeAccountLimits reports. They are the default
// CloudFormation quotas.
const (
	limitStacks              = 2000
	limitStackOutputs        = cfn.MaxOutputs
	limitConcurrentResources = 2500
)

// costCalculatorURL is the page EstimateTemplateCost links to.
const (
	costCalculatorURL = "https://calculator.aws/#/estimate?id="
	costIDLen         = 32
)

// DeleteStack deletes a stack. Each resource is deleted in reverse creation
// order unless its DeletionPolicy keeps it. When a resource fails to delete
// the stack ends DELETE_FAILED. A retry may then name the resources to keep
// in RetainResources, or use DeletionMode FORCE_DELETE_STACK to keep every
// one that still fails. A stack whose exports another stack imports ends
// DELETE_FAILED without deleting anything. Termination protection refuses
// the call. Deleting an absent or already deleted stack is a successful
// no-op, as in CloudFormation.
func (m *Mock) DeleteStack(ctx context.Context, in *cfn.DeleteStackInput) error {
	modes := []string{cfn.DeletionModeStandard, cfn.DeletionModeForceDelete}
	if in.DeletionMode != "" && !slices.Contains(modes, in.DeletionMode) {
		return cerrors.Newf(cerrors.InvalidArgument, msgFieldEnum, in.DeletionMode, "deletionMode", strings.Join(modes, ", "))
	}

	sd, _, ok := m.findStack(in.StackName)
	if !ok || sd.status() == cfn.StatusDeleteComplete {
		return nil
	}

	wasFailed, err := checkDeletable(sd, in)
	if err != nil {
		return err
	}

	m.exportMu.Lock()
	reason := m.exportInUse(sd)
	m.startDelete(sd, in.DeletionMode)
	m.exportMu.Unlock()

	if reason != "" {
		m.emitStackEvent(sd, cfn.StatusDeleteFailed, reason)
		return nil
	}

	retain := make(map[string]bool, len(in.RetainResources))
	for _, id := range in.RetainResources {
		retain[id] = true
	}

	m.finishDelete(ctx, sd, teardownOpts{retain: retain, force: wasFailed && in.DeletionMode == cfn.DeletionModeForceDelete})

	return nil
}

// checkDeletable refuses a protected stack, and RetainResources on a stack
// that is not DELETE_FAILED. It reports whether the stack is DELETE_FAILED.
func checkDeletable(sd *stackData, in *cfn.DeleteStackInput) (bool, error) {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if sd.stack.EnableTerminationProtection {
		return false, cerrors.Newf(cerrors.InvalidArgument, msgTerminationProtected, sd.stack.Name)
	}

	failed := sd.stack.Status == cfn.StatusDeleteFailed
	if len(in.RetainResources) > 0 && !failed {
		return false, cerrors.Newf(cerrors.InvalidArgument, msgRetainNotFailed, sd.stack.ID)
	}

	return failed, nil
}

// startDelete moves the stack to DELETE_IN_PROGRESS, which also withdraws
// its exports. The caller holds exportMu.
func (m *Mock) startDelete(sd *stackData, mode string) {
	if mode == "" {
		mode = cfn.DeletionModeStandard
	}

	sd.mu.Lock()
	sd.stack.DeletionMode = mode
	sd.mu.Unlock()

	m.emitStackEvent(sd, cfn.StatusDeleteInProgress, reasonUserInitiated)
}

// UpdateTerminationProtection turns a stack's termination protection on or
// off and returns the stack id.
func (m *Mock) UpdateTerminationProtection(_ context.Context, in *cfn.UpdateTerminationProtectionInput) (string, error) {
	sd, err := m.activeStack(in.StackName)
	if err != nil {
		return "", err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.stack.EnableTerminationProtection = in.Enable

	return sd.stack.ID, nil
}

// checkStackLimit refuses a new stack once the account holds StackLimit
// stacks that are not deleted.
func (m *Mock) checkStackLimit() error {
	n := 0

	for _, sd := range m.stacks.All() {
		if sd.status() != cfn.StatusDeleteComplete {
			n++
		}
	}

	if n >= limitStacks {
		return cfn.NewException(cfn.ExceptionLimitExceeded, cerrors.New(cerrors.FailedPrecondition, msgStackLimit))
	}

	return nil
}

// DescribeAccountLimits returns the account's CloudFormation quotas. They
// fit one page, so a token is ignored.
func (*Mock) DescribeAccountLimits(_ context.Context, _ string) ([]cfn.AccountLimit, error) {
	return []cfn.AccountLimit{
		{Name: "StackLimit", Value: limitStacks},
		{Name: "StackOutputsLimit", Value: limitStackOutputs},
		{Name: "ConcurrentResourcesLimit", Value: limitConcurrentResources},
	}, nil
}

// EstimateTemplateCost checks a template and its parameters and returns a
// cost calculator link. The link id is a hash of the template and
// parameters, so the same input gives the same link.
func (m *Mock) EstimateTemplateCost(ctx context.Context, in *cfn.EstimateTemplateCostInput) (string, error) {
	body, err := m.templateBody(ctx, in.TemplateBody, in.TemplateURL)
	if err != nil {
		return "", err
	}

	t, err := cfn.ParseTemplate(body)
	if err != nil {
		return "", err
	}

	for _, p := range in.Parameters {
		if _, ok := t.Parameters[p.Key]; !ok {
			return "", cerrors.Newf(cerrors.InvalidArgument, "Parameters: [%s] do not exist in the template", p.Key)
		}
	}

	params := make([]string, 0, len(in.Parameters))
	for _, p := range in.Parameters {
		params = append(params, p.Key+"="+p.Value)
	}

	sort.Strings(params)

	sum := sha256.Sum256([]byte(body + "\x00" + strings.Join(params, "\x00")))

	return costCalculatorURL + hex.EncodeToString(sum[:])[:costIDLen], nil
}
