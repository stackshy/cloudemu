package ssm

import (
	"strconv"
	"strings"
	"time"

	ssmdriver "github.com/stackshy/cloudemu/v2/providers/aws/ssm/driver"
)

// Command lifecycle.
//
// A send stores a commandRecord and nothing else changes on its own: the
// status of every invocation is worked out from the clock at read time, so
// reads are stable for a given clock and survive snapshot/restore. Only
// CancelCommand writes a status (the cancel time).
//
// Invocations run in batches of MaxConcurrency. Each batch spends the delivery
// window Pending and the run window InProgress before it reports Success, and
// the next batch starts when it ends. Both windows are zero unless async settle
// is on, so by default a command is finished when SendCommand returns.
//
// An instance a tag Target selected while it was not running stays Delayed
// until TimeoutSeconds passes and then times out (DeliveryTimedOut). An
// executionTimeout parameter shorter than the run window makes the invocation
// time out (ExecutionTimedOut).

// StatusDetails values beyond the plain statuses.
const (
	detailsDeliveryTimedOut  = "DeliveryTimedOut"
	detailsExecutionTimedOut = "ExecutionTimedOut"
	detailsIncomplete        = "Incomplete"
	detailsNoInstancesInTag  = "NoInstancesInTag"
)

// noResponse is the ResponseCode of an invocation that has not finished.
const noResponse = -1

// commandRecord is one stored send. Fields are exported for the snapshot and
// guarded by Mock.cmdMu.
type commandRecord struct {
	// Command holds what the send recorded. Status and counts are filled in
	// on each read.
	Command ssmdriver.Command `json:"command"`
	Steps   []commandStep     `json:"steps,omitempty"`
	// ExecutionTimeout is the executionTimeout parameter in seconds, or 0.
	ExecutionTimeout int `json:"executionTimeout,omitempty"`
	// Delivery and Run are the settle windows in force when it was sent.
	Delivery    time.Duration       `json:"delivery,omitempty"`
	Run         time.Duration       `json:"run,omitempty"`
	Invocations []*invocationRecord `json:"invocations"`
}

// invocationRecord is the stored part of one invocation.
type invocationRecord struct {
	InstanceID string `json:"instanceId"`
	// Offline is set when the instance was not running at send time.
	Offline     bool      `json:"offline,omitempty"`
	CancelledAt time.Time `json:"cancelledAt,omitzero"` //nolint:misspell // SSM spells the status Cancelled.
	// OutputWritten is set once the output has gone to S3.
	OutputWritten bool `json:"outputWritten,omitempty"`
}

// invocationState is an invocation observed at one instant.
type invocationState struct {
	status  string
	details string
	code    int32
	start   time.Time
	end     time.Time
}

// terminal reports whether the invocation can no longer change.
func (s *invocationState) terminal() bool {
	switch s.status {
	case ssmdriver.CommandPending, ssmdriver.CommandInProgress, ssmdriver.CommandDelayed:
		return false
	default:
		return true
	}
}

// batchSize is how many invocations run at once under MaxConcurrency.
func (r *commandRecord) batchSize() int {
	n := len(r.Invocations)

	return max(1, countOrPercent(r.Command.MaxConcurrency, n, true))
}

// maxErrors is the error count the command tolerates before it fails.
func (r *commandRecord) maxErrors() int {
	return countOrPercent(r.Command.MaxErrors, len(r.Invocations), false)
}

// countOrPercent reads "10" or "10%" of total. A percentage of concurrency
// rounds up, one of errors rounds down.
func countOrPercent(v string, total int, roundUp bool) int {
	if p, ok := strings.CutSuffix(v, "%"); ok {
		n, _ := strconv.Atoi(p)
		if roundUp {
			return (n*total + percent - 1) / percent
		}

		return n * total / percent
	}

	n, _ := strconv.Atoi(v)

	return n
}

func stateOf(status, details string) invocationState {
	return invocationState{status: status, details: details, code: noResponse}
}

// invocation observes invocation i at now.
func (r *commandRecord) invocation(i int, now time.Time) invocationState {
	inv := r.Invocations[i]
	if inv.Offline {
		return r.offlineInvocation(inv, now)
	}

	batch := time.Duration(i / r.batchSize())
	start := r.Command.RequestedDateTime.Add(r.Delivery + batch*r.Run)
	finish := start.Add(r.Run)
	done := invocationState{status: ssmdriver.CommandSuccess, details: ssmdriver.CommandSuccess, start: start, end: finish}

	if limit := time.Duration(r.ExecutionTimeout) * time.Second; r.ExecutionTimeout > 0 && limit < r.Run {
		finish = start.Add(limit)
		done = stateOf(ssmdriver.CommandTimedOut, detailsExecutionTimedOut)
		done.start, done.end = start, finish
	}

	switch {
	case !inv.CancelledAt.IsZero() && inv.CancelledAt.Before(finish):
		s := stateOf(ssmdriver.CommandCancelled, ssmdriver.CommandCancelled)
		if !inv.CancelledAt.Before(start) {
			s.start, s.end = start, inv.CancelledAt
		}

		return s
	case now.Before(start):
		return stateOf(ssmdriver.CommandPending, ssmdriver.CommandPending)
	case now.Before(finish):
		s := stateOf(ssmdriver.CommandInProgress, ssmdriver.CommandInProgress)
		s.start = start

		return s
	default:
		return done
	}
}

// offlineInvocation observes an invocation whose instance was not running:
// it waits for delivery until TimeoutSeconds passes.
func (r *commandRecord) offlineInvocation(inv *invocationRecord, now time.Time) invocationState {
	expires := r.Command.RequestedDateTime.Add(time.Duration(r.Command.TimeoutSeconds) * time.Second)

	switch {
	case !inv.CancelledAt.IsZero() && inv.CancelledAt.Before(expires):
		return stateOf(ssmdriver.CommandCancelled, ssmdriver.CommandCancelled)
	case now.Before(expires):
		return stateOf(ssmdriver.CommandDelayed, ssmdriver.CommandDelayed)
	default:
		return stateOf(ssmdriver.CommandTimedOut, detailsDeliveryTimedOut)
	}
}

// tally counts the invocation outcomes a command status is built from.
type tally struct {
	total, completed, started                                      int
	succeeded, canceled, errs, deliveryTimeouts, executionTimeouts int
}

func (t *tally) add(s *invocationState) {
	t.total++

	switch {
	case s.status == ssmdriver.CommandSuccess:
		t.succeeded++
	case s.status == ssmdriver.CommandCancelled:
		t.canceled++
	case s.details == detailsDeliveryTimedOut:
		t.deliveryTimeouts++
	case s.details == detailsExecutionTimedOut:
		t.executionTimeouts++
		t.errs++
	case s.status == ssmdriver.CommandFailed:
		t.errs++
	}

	if s.terminal() {
		t.completed++
	}

	if s.status != ssmdriver.CommandPending && s.status != ssmdriver.CommandDelayed {
		t.started++
	}
}

// status is the command Status and StatusDetails for the tallied outcomes.
func (t *tally) status(maxErrors int) (status, details string) {
	switch {
	case t.total == 0:
		return ssmdriver.CommandSuccess, detailsNoInstancesInTag
	case t.completed < t.total && t.started > 0:
		return ssmdriver.CommandInProgress, ssmdriver.CommandInProgress
	case t.completed < t.total:
		return ssmdriver.CommandPending, ssmdriver.CommandPending
	default:
		return t.finalStatus(maxErrors)
	}
}

// finalStatus is the status of a command whose invocations have all finished.
func (t *tally) finalStatus(maxErrors int) (status, details string) {
	switch {
	case t.canceled > 0:
		return ssmdriver.CommandCancelled, ssmdriver.CommandCancelled
	case t.succeeded == t.total:
		return ssmdriver.CommandSuccess, ssmdriver.CommandSuccess
	case t.errs > maxErrors && t.executionTimeouts == t.errs:
		return ssmdriver.CommandTimedOut, detailsExecutionTimedOut
	case t.errs > maxErrors:
		return ssmdriver.CommandFailed, ssmdriver.CommandFailed
	case t.succeeded == 0 && t.deliveryTimeouts == t.total:
		return ssmdriver.CommandTimedOut, detailsDeliveryTimedOut
	default:
		return ssmdriver.CommandFailed, detailsIncomplete
	}
}

// observe returns the command with its status and counts at now, and every
// invocation's state.
func (r *commandRecord) observe(now time.Time) (ssmdriver.Command, []invocationState) {
	cmd := r.Command
	states := make([]invocationState, len(r.Invocations))

	var t tally

	for i := range r.Invocations {
		states[i] = r.invocation(i, now)
		t.add(&states[i])
	}

	// Counts are bounded by the target count: at most 50 explicit ids plus
	// the tag matches.
	cmd.TargetCount = int32(t.total)                      //nolint:gosec // bounded, see above.
	cmd.CompletedCount = int32(t.completed)               //nolint:gosec // bounded, see above.
	cmd.ErrorCount = int32(t.errs)                        //nolint:gosec // bounded, see above.
	cmd.DeliveryTimedOutCount = int32(t.deliveryTimeouts) //nolint:gosec // bounded, see above.
	cmd.Status, cmd.StatusDetails = t.status(r.maxErrors())

	return cmd, states
}
