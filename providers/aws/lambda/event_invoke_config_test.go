package lambda

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// fakeSQS / fakeSNS record cross-service async-failure deliveries so tests can
// assert that a failed asynchronous invoke routed its event to the DLQ /
// destination.
type fakeSQS struct {
	mu   sync.Mutex
	msgs map[string][]string
}

func (f *fakeSQS) DeliverExternal(_ context.Context, arn, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.msgs == nil {
		f.msgs = map[string][]string{}
	}

	f.msgs[arn] = append(f.msgs[arn], body)

	return nil
}

func (f *fakeSQS) get(arn string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.msgs[arn]
}

type fakeSNS struct {
	mu   sync.Mutex
	msgs map[string][]string
	// onPublish, when set, is called on every publish — used to simulate an
	// SNS -> Lambda fan-out that re-invokes the failing function (recursion test).
	onPublish func(ctx context.Context, arn, msg string)
}

func (f *fakeSNS) PublishExternal(ctx context.Context, arn, message string) error {
	f.mu.Lock()
	if f.msgs == nil {
		f.msgs = map[string][]string{}
	}

	f.msgs[arn] = append(f.msgs[arn], message)
	cb := f.onPublish
	f.mu.Unlock()

	if cb != nil {
		cb(ctx, arn, message)
	}

	return nil
}

func (f *fakeSNS) get(arn string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.msgs[arn]
}

const (
	dlqSQSARN     = "arn:aws:sqs:us-east-1:000000000000:my-dlq"
	failSNSARN    = "arn:aws:sns:us-east-1:000000000000:on-failure"
	successSNSARN = "arn:aws:sns:us-east-1:000000000000:on-success"
)

func intPtr(v int) *int { return &v }

// createFailingFunc creates my-func with a handler that always errors, so an
// asynchronous invoke fails and routes to its DLQ / destinations.
func createFailingFunc(t *testing.T, m *Mock) {
	t.Helper()

	if _, err := m.CreateFunction(context.Background(), defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	m.RegisterHandler("my-func", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("boom")
	})
}

func TestEventInvokeConfigRoundTrip(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	put, err := m.PutFunctionEventInvokeConfig(ctx, driver.EventInvokeConfig{
		FunctionName:             "my-func",
		MaximumRetryAttempts:     intPtr(1),
		MaximumEventAgeInSeconds: intPtr(3600),
		DestinationConfig: &driver.DestinationConfig{
			OnFailure: &driver.Destination{Destination: failSNSARN},
		},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if put.MaximumRetryAttempts == nil || *put.MaximumRetryAttempts != 1 {
		t.Fatalf("MaximumRetryAttempts = %v, want 1", put.MaximumRetryAttempts)
	}

	if !strings.HasSuffix(put.FunctionArn, ":function:my-func") {
		t.Fatalf("FunctionArn = %q, want unqualified function arn", put.FunctionArn)
	}

	// Update merges: only change the retries, destinations stay.
	upd, err := m.UpdateFunctionEventInvokeConfig(ctx, driver.EventInvokeConfig{
		FunctionName:         "my-func",
		MaximumRetryAttempts: intPtr(0),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if *upd.MaximumRetryAttempts != 0 {
		t.Fatalf("merged MaximumRetryAttempts = %d, want 0", *upd.MaximumRetryAttempts)
	}

	if upd.DestinationConfig == nil || upd.DestinationConfig.OnFailure == nil {
		t.Fatal("Update dropped the OnFailure destination")
	}

	got, err := m.GetFunctionEventInvokeConfig(ctx, "my-func", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if *got.MaximumEventAgeInSeconds != 3600 {
		t.Fatalf("MaximumEventAgeInSeconds = %d, want 3600", *got.MaximumEventAgeInSeconds)
	}

	list, err := m.ListFunctionEventInvokeConfigs(ctx, "my-func")
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v (err %v), want 1 config", list, err)
	}

	if err := m.DeleteFunctionEventInvokeConfig(ctx, "my-func", ""); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := m.GetFunctionEventInvokeConfig(ctx, "my-func", ""); err == nil {
		t.Fatal("Get after Delete: want NotFound")
	}
}

func TestEventInvokeConfigValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if _, err := m.PutFunctionEventInvokeConfig(ctx, driver.EventInvokeConfig{
		FunctionName: "my-func", MaximumRetryAttempts: intPtr(3),
	}); err == nil {
		t.Fatal("retries=3: want InvalidArgument")
	}

	if _, err := m.PutFunctionEventInvokeConfig(ctx, driver.EventInvokeConfig{
		FunctionName: "my-func", MaximumEventAgeInSeconds: intPtr(30),
	}); err == nil {
		t.Fatal("eventAge=30: want InvalidArgument")
	}

	if _, err := m.PutFunctionEventInvokeConfig(ctx, driver.EventInvokeConfig{
		FunctionName: "absent",
	}); err == nil {
		t.Fatal("unknown function: want NotFound")
	}
}

// TestAsyncFailureDeliversToDLQ covers the headline behavior: a failing async
// (Event) invoke routes the original event to the DeadLetterConfig SQS queue and
// the OnFailure destination (with the error), while a successful async invoke
// does not.
func TestAsyncFailureDeliversToDLQ(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	sqs, sns := &fakeSQS{}, &fakeSNS{}
	m.SetAsyncDestinationTargets(sqs, sns)

	createFailingFunc(t, m)

	if err := m.SetFunctionAWSConfig(ctx, "my-func", driver.AWSFunctionConfig{
		DeadLetterConfig: &driver.DeadLetterConfig{TargetArn: dlqSQSARN},
	}, true); err != nil {
		t.Fatalf("SetFunctionAWSConfig: %v", err)
	}

	if _, err := m.PutFunctionEventInvokeConfig(ctx, driver.EventInvokeConfig{
		FunctionName: "my-func",
		DestinationConfig: &driver.DestinationConfig{
			OnFailure: &driver.Destination{Destination: failSNSARN},
		},
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Async invoke: the failing handler makes InvokeExternal report an error, but
	// the DLQ/destination delivery has already happened inside Invoke.
	arn := "arn:aws:lambda:us-east-1:000000000000:function:my-func"
	_ = m.InvokeExternal(ctx, arn, []byte(`{"event":"payload"}`))

	dlq := sqs.get(dlqSQSARN)
	if len(dlq) != 1 || dlq[0] != `{"event":"payload"}` {
		t.Fatalf("DLQ messages = %v, want the original event", dlq)
	}

	failMsgs := sns.get(failSNSARN)
	if len(failMsgs) != 1 {
		t.Fatalf("OnFailure messages = %d, want 1", len(failMsgs))
	}

	if !strings.Contains(failMsgs[0], "RetriesExhausted") || !strings.Contains(failMsgs[0], "boom") {
		t.Fatalf("OnFailure envelope = %q, want RetriesExhausted + error", failMsgs[0])
	}
}

func TestAsyncSuccessSkipsDLQButHitsOnSuccess(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	sqs, sns := &fakeSQS{}, &fakeSNS{}
	m.SetAsyncDestinationTargets(sqs, sns)

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}
	// Succeeding handler.
	m.RegisterHandler("my-func", func(_ context.Context, p []byte) ([]byte, error) {
		return p, nil
	})

	if err := m.SetFunctionAWSConfig(ctx, "my-func", driver.AWSFunctionConfig{
		DeadLetterConfig: &driver.DeadLetterConfig{TargetArn: dlqSQSARN},
	}, true); err != nil {
		t.Fatalf("SetFunctionAWSConfig: %v", err)
	}

	if _, err := m.PutFunctionEventInvokeConfig(ctx, driver.EventInvokeConfig{
		FunctionName: "my-func",
		DestinationConfig: &driver.DestinationConfig{
			OnSuccess: &driver.Destination{Destination: successSNSARN},
		},
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	arn := "arn:aws:lambda:us-east-1:000000000000:function:my-func"
	if err := m.InvokeExternal(ctx, arn, []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("InvokeExternal: %v", err)
	}

	if got := sqs.get(dlqSQSARN); len(got) != 0 {
		t.Fatalf("DLQ received %v on success, want none", got)
	}

	if got := sns.get(successSNSARN); len(got) != 1 || !strings.Contains(got[0], "Success") {
		t.Fatalf("OnSuccess messages = %v, want 1 Success envelope", got)
	}
}

func TestSyncFailureDoesNotDeliver(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	sqs, sns := &fakeSQS{}, &fakeSNS{}
	m.SetAsyncDestinationTargets(sqs, sns)

	createFailingFunc(t, m)

	if err := m.SetFunctionAWSConfig(ctx, "my-func", driver.AWSFunctionConfig{
		DeadLetterConfig: &driver.DeadLetterConfig{TargetArn: dlqSQSARN},
	}, true); err != nil {
		t.Fatalf("SetFunctionAWSConfig: %v", err)
	}

	// A synchronous (RequestResponse) invoke never routes to the DLQ.
	if _, err := m.Invoke(ctx, driver.InvokeInput{
		FunctionName: "my-func", InvokeType: "RequestResponse", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if got := sqs.get(dlqSQSARN); len(got) != 0 {
		t.Fatalf("DLQ received %v on sync failure, want none", got)
	}
}

// TestAsyncFailureDLQRecursionBounded proves the DLQ-that-re-invokes-its-own-
// function loop terminates at the recursion guard instead of overflowing: the
// OnFailure SNS "topic" re-invokes the failing function via InvokeExternal on
// every publish, so each failure re-delivers and re-invokes.
func TestAsyncFailureDLQRecursionBounded(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	sqs, sns := &fakeSQS{}, &fakeSNS{}
	arn := "arn:aws:lambda:us-east-1:000000000000:function:my-func"
	sns.onPublish = func(c context.Context, _, _ string) {
		_ = m.InvokeExternal(c, arn, []byte(`{"loop":true}`))
	}

	m.SetAsyncDestinationTargets(sqs, sns)
	createFailingFunc(t, m)

	if _, err := m.PutFunctionEventInvokeConfig(ctx, driver.EventInvokeConfig{
		FunctionName: "my-func",
		DestinationConfig: &driver.DestinationConfig{
			OnFailure: &driver.Destination{Destination: failSNSARN},
		},
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Must return (not stack-overflow); delivery count is bounded by the guard.
	_ = m.InvokeExternal(ctx, arn, []byte(`{"start":true}`))

	if n := len(sns.get(failSNSARN)); n == 0 || n > recursionguard.MaxDepth+1 {
		t.Fatalf("OnFailure deliveries = %d, want 1..%d (bounded by recursion guard)", n, recursionguard.MaxDepth+1)
	}
}
