package cloudformation

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// panicProv wraps slowProv with a Delete that panics while armed.
type panicProv struct {
	slowProv
	armed *atomic.Bool
}

func (p panicProv) Delete(ctx context.Context, physicalID string, props map[string]any) error {
	if p.armed.Load() {
		panic("provisioner blew up")
	}

	return p.slowProv.Delete(ctx, physicalID, props)
}

// A stack saved in an *_IN_PROGRESS status with nothing pending, as an
// older snapshot could hold, is not stranded: the gate looks at the running
// operation, and a restore moves it to the outcome of that operation.
func TestStrandedInProgressStackIsNotStuck(t *testing.T) {
	ctx := context.Background()
	p := newSlowProv()
	m, fc := newSlowAsyncMock(p)

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: slowTemplate("a")})
	requireNoError(t, err)
	fc.Advance(settled)
	m.Tick(fc.Now())

	sd := m.mustData(t, "s")
	sd.mu.Lock()
	sd.stack.Status = cfn.StatusUpdateCompleteCleanupInProgress
	sd.mu.Unlock()

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored, _ := newSlowAsyncMock(p)
	requireNoError(t, restored.Restore(ctx, data))

	st := stackStatus(t, restored, "s")
	assertEqual(t, st.Status, cfn.StatusUpdateComplete, "restored status")
	assertEqual(t, st.StatusReason, reasonInternalFailure, "restored reason")

	_, err = restored.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: slowTemplate("a2")})
	requireNoError(t, err)

	// Without a restore, the stranded status alone does not block a delete.
	requireNoError(t, m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s"}))
}

// Snapshot waits for a phase that is still running instead of capturing it
// halfway.
func TestSnapshotWaitsForARunningPhase(t *testing.T) {
	ctx := context.Background()
	m, _ := newSlowAsyncMock(newSlowProv())

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: slowTemplate("a")})
	requireNoError(t, err)

	sd := m.mustData(t, "s")
	sd.opMu.Lock()

	done := make(chan struct{})

	go func() {
		_, _ = m.Snapshot(ctx, false)

		close(done)
	}()

	select {
	case <-done:
		t.Fatal("Snapshot ran while the stack's phase held opMu")
	case <-time.After(50 * time.Millisecond):
	}

	sd.opMu.Unlock()
	<-done
}

// A provisioner that panics while a phase settles fails the stack instead
// of crashing the ticker and leaving it in progress forever.
func TestPanicWhileSettlingFailsTheStack(t *testing.T) {
	ctx := context.Background()
	p := panicProv{slowProv: newSlowProv(), armed: &atomic.Bool{}}
	m, fc := newSlowAsyncMock(p.slowProv)
	m.registry["Test::Slow"] = p

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: slowTemplate("a")})
	requireNoError(t, err)
	fc.Advance(settled)

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: slowTemplate("a2")})
	requireNoError(t, err)

	p.armed.Store(true)
	fc.Advance(settled)

	if !m.Tick(fc.Now()) {
		t.Fatal("Tick should run the cleanup phase")
	}

	st := stackStatus(t, m, "s")
	assertEqual(t, st.Status, cfn.StatusUpdateComplete, "status after the panic")

	if !strings.HasPrefix(st.StatusReason, reasonInternalFailure) {
		t.Fatalf("reason = %q", st.StatusReason)
	}

	p.armed.Store(false)
	requireNoError(t, m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s"}))
	fc.Advance(settled)
	m.Tick(fc.Now())

	if live, _ := p.state(); live["a2"] != 0 {
		t.Fatalf("a2 should be deleted: %v", live)
	}
}

func TestStrandedOutcomes(t *testing.T) {
	cases := map[string]string{
		cfn.StatusCreateInProgress:                        cfn.StatusCreateFailed,
		cfn.StatusRollbackInProgress:                      cfn.StatusRollbackFailed,
		cfn.StatusUpdateInProgress:                        cfn.StatusUpdateRollbackFailed,
		cfn.StatusUpdateRollbackInProgress:                cfn.StatusUpdateRollbackFailed,
		cfn.StatusUpdateCompleteCleanupInProgress:         cfn.StatusUpdateComplete,
		cfn.StatusUpdateRollbackCompleteCleanupInProgress: cfn.StatusUpdateRollbackComplete,
		cfn.StatusDeleteInProgress:                        cfn.StatusDeleteFailed,
	}

	for from, want := range cases {
		got, ok := strandedOutcome(from)
		assertEqual(t, ok, true, from)
		assertEqual(t, got, want, from)
	}

	if _, ok := strandedOutcome(cfn.StatusReviewInProgress); ok {
		t.Fatal("REVIEW_IN_PROGRESS is a resting status")
	}
}
