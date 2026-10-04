package cloudformation

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// settled is a clock step past any stack settle window.
const settled = time.Minute

// newAsyncParamMock is newParamMock with AsyncSettle on, and its clock.
func newAsyncParamMock(p paramProv) (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"),
		config.WithAccountID("123456789012"), config.WithAsyncSettle()))
	m.SetRegistry(cfn.Registry{"Test::Param": p, "Test::Boom": failProv{}})

	return m, fc
}

func visibleEvents(t *testing.T, m *Mock, stack string) int {
	t.Helper()

	events, err := m.DescribeStackEvents(context.Background(), stack)
	requireNoError(t, err)

	return len(events)
}

func TestAsyncCreateStaysInProgressUntilSettled(t *testing.T) {
	p := newParamProv()
	m, fc := newAsyncParamMock(p)
	ctx := context.Background()

	st, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusCreateInProgress, "returned status")
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusCreateInProgress, "described status")
	assertEqual(t, visibleEvents(t, m, "s"), 1, "only the stack event is visible at once")

	summaries, err := m.ListStacks(ctx, nil)
	requireNoError(t, err)
	assertEqual(t, summaries[0].Status, cfn.StatusCreateInProgress, "listed status")

	fc.Advance(stackEventStep)
	assertEqual(t, visibleEvents(t, m, "s"), 2, "events arrive one by one")

	fc.Advance(settled)
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusCreateComplete, "settled status")
	assertEqual(t, eventStatuses(t, m, "s", "Old")[1], cfn.ResourceCreateComplete, "resource events")
	assertEqual(t, p.values["/p"], "v1", "resource created")
}

func TestAsyncStackRowsFollowVisibleEvents(t *testing.T) {
	m, fc := newAsyncParamMock(newParamProv())
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)

	rows, err := m.DescribeStackResources(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, len(rows), 0, "no resource is visible before its first event")

	fc.Advance(stackEventStep)

	rows, err = m.DescribeStackResources(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, len(rows), 1, "one row")
	assertEqual(t, rows[0].Status, cfn.ResourceCreateInProgress, "row shows the visible status")

	fc.Advance(settled)

	rows, err = m.DescribeStackResources(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, rows[0].Status, cfn.ResourceCreateComplete, "settled row")
}

func TestAsyncUpdateBlocksAnotherUpdate(t *testing.T) {
	p := newParamProv()
	m, fc := newAsyncParamMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)
	fc.Advance(settled)

	st, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramModified})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateInProgress, "update in progress")

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramV1})
	assertValidation(t, err, "")

	fc.Advance(settled)
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusUpdateComplete, "settled")
	assertEqual(t, p.values["/p"], "v2", "updated")
}

func TestCancelUpdateStackRollsBackAReplacement(t *testing.T) {
	p := newParamProv()
	m, fc := newAsyncParamMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)
	fc.Advance(settled)

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramRenamed})
	requireNoError(t, err)

	requireNoError(t, m.CancelUpdateStack(ctx, &cfn.CancelUpdateStackInput{StackName: "s", ClientRequestToken: "cancel-1"}))

	st := stackStatus(t, m, "s")
	assertEqual(t, st.Status, cfn.StatusUpdateRollbackInProgress, "rolling back")
	assertEqual(t, st.StatusReason, reasonUpdateCancelled, "reason")

	events, err := m.DescribeStackEvents(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, events[0].ClientRequestToken, "cancel-1", "cancel events carry the token")

	fc.Advance(settled)

	st = stackStatus(t, m, "s")
	assertEqual(t, st.Status, cfn.StatusUpdateRollbackComplete, "rolled back")
	assertEqual(t, p.values["/p"], "v1", "the old resource is intact")

	if _, ok := p.values["/p2"]; ok {
		t.Fatal("the replacement must be deleted by the rollback")
	}

	body, err := m.GetTemplate(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, body, paramV1, "template reverted")
}

func TestCancelUpdateStackOutsideAnUpdate(t *testing.T) {
	ctx := context.Background()

	sync := newParamMock(newParamProv())
	_, err := sync.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)
	_, err = sync.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramModified})
	requireNoError(t, err)
	assertValidation(t, sync.CancelUpdateStack(ctx, &cfn.CancelUpdateStackInput{StackName: "s"}), msgCancelBadStatus)
	assertValidation(t, sync.CancelUpdateStack(ctx, &cfn.CancelUpdateStackInput{StackName: "nope"}), "")

	async, fc := newAsyncParamMock(newParamProv())
	_, err = async.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)
	assertValidation(t, async.CancelUpdateStack(ctx, &cfn.CancelUpdateStackInput{StackName: "s"}), msgCancelBadStatus)

	fc.Advance(settled)
	_, err = async.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramModified})
	requireNoError(t, err)
	fc.Advance(settled)
	assertValidation(t, async.CancelUpdateStack(ctx, &cfn.CancelUpdateStackInput{StackName: "s"}), msgCancelBadStatus)
}

func TestAsyncDeniedUpdateRollsBackAfterSettle(t *testing.T) {
	p := newParamProv()
	m, fc := newAsyncParamMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1, StackPolicyBody: denyReplaceOld})
	requireNoError(t, err)
	fc.Advance(settled)

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramRenamed})
	requireNoError(t, err)
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusUpdateInProgress, "in progress")

	fc.Advance(settled)
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusUpdateRollbackInProgress, "rolling back")

	fc.Advance(settled)
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusUpdateRollbackComplete, "rolled back")
	assertEqual(t, p.values["/p"], "v1", "intact")
}

func TestAsyncDeleteSettles(t *testing.T) {
	p := newParamProv()
	m, fc := newAsyncParamMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)
	fc.Advance(settled)

	requireNoError(t, m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s"}))
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusDeleteInProgress, "deleting")

	// The serve ticker completes the delete without any read.
	if !m.Tick(fc.Now().Add(settled)) {
		t.Fatal("Tick should settle the delete")
	}

	fc.Advance(settled)

	if _, err = m.DescribeStacks(ctx, "s"); err == nil {
		t.Fatal("a deleted stack is not described by name")
	}

	if _, ok := p.values["/p"]; ok {
		t.Fatal("resource should be deleted")
	}

	if m.Tick(fc.Now()) {
		t.Fatal("nothing left to settle")
	}
}

func TestAsyncPendingUpdateSurvivesSnapshot(t *testing.T) {
	p := newParamProv()
	m, fc := newAsyncParamMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)
	fc.Advance(settled)

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramRenamed})
	requireNoError(t, err)

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored, rfc := newAsyncParamMock(p)
	rfc.Set(fc.Now())
	requireNoError(t, restored.Restore(ctx, data))
	assertEqual(t, stackStatus(t, restored, "s").Status, cfn.StatusUpdateInProgress, "still in progress")

	requireNoError(t, restored.CancelUpdateStack(ctx, &cfn.CancelUpdateStackInput{StackName: "s"}))
	rfc.Advance(settled)
	assertEqual(t, stackStatus(t, restored, "s").Status, cfn.StatusUpdateRollbackComplete, "rolled back after restore")
	assertEqual(t, p.values["/p"], "v1", "old resource intact")

	if _, ok := p.values["/p2"]; ok {
		t.Fatal("replacement deleted")
	}
}

func TestSyncStacksStaySynchronous(t *testing.T) {
	m := newParamMock(newParamProv())
	ctx := context.Background()

	st, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusCreateComplete, "create")

	st, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramModified})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateComplete, "update")

	if m.Tick(time.Now()) {
		t.Fatal("a synchronous mock has nothing to settle")
	}
}
