package lambda_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// invokeSync issues a synchronous invoke and returns the error (nil on success).
func invokeSync(t *testing.T, client *awslambda.Client, name string, optFns ...func(*awslambda.Options)) error {
	t.Helper()

	_, err := client.Invoke(context.Background(), &awslambda.InvokeInput{
		FunctionName:   aws.String(name),
		InvocationType: lambdatypes.InvocationTypeRequestResponse,
		Payload:        []byte(`{}`),
	}, optFns...)

	return err
}

// noRetry disables the SDK's automatic retry of throttling responses so a
// throttle assertion sees the single raw 429 immediately rather than after the
// default exponential backoff.
func noRetry(o *awslambda.Options) { o.Retryer = aws.NopRetryer{} }

// assertThrottled asserts err is the reserved-concurrency TooManyRequestsException:
// HTTP 429, error code TooManyRequestsException, and Reason
// ReservedFunctionConcurrentInvocationLimitExceeded.
func assertThrottled(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("Invoke over the reserved-concurrency limit: want error, got nil")
	}

	var tmr *lambdatypes.TooManyRequestsException
	if !errors.As(err, &tmr) {
		t.Fatalf("error = %T %v, want *TooManyRequestsException", err, err)
	}

	if tmr.Reason != lambdatypes.ThrottleReasonReservedFunctionConcurrentInvocationLimitExceeded {
		t.Errorf("Reason = %q, want ReservedFunctionConcurrentInvocationLimitExceeded", tmr.Reason)
	}

	var re *awshttp.ResponseError
	if !errors.As(err, &re) {
		t.Fatalf("error = %T %v, want *awshttp.ResponseError for status", err, err)
	}

	if re.HTTPStatusCode() != 429 {
		t.Errorf("HTTP status = %d, want 429", re.HTTPStatusCode())
	}
}

// TestSDKReservedConcurrencyLifecycle covers the Put/Get/Delete round-trip and,
// with reserved=0 (throttle everything), that Invoke is rejected with a 429
// TooManyRequestsException and works again once the limit is cleared.
func TestSDKReservedConcurrencyLifecycle(t *testing.T) {
	client, cloud := newSDKClient(t)
	ctx := context.Background()

	createBasicFunction(t, client, "resv")
	cloud.Lambda.RegisterHandler("resv", func(_ context.Context, p []byte) ([]byte, error) {
		return p, nil
	})

	// A limit is not enforced until PutFunctionConcurrency runs.
	if err := invokeSync(t, client, "resv"); err != nil {
		t.Fatalf("Invoke before any reserved concurrency: %v", err)
	}

	putOut, err := client.PutFunctionConcurrency(ctx, &awslambda.PutFunctionConcurrencyInput{
		FunctionName:                 aws.String("resv"),
		ReservedConcurrentExecutions: aws.Int32(0),
	})
	if err != nil {
		t.Fatalf("PutFunctionConcurrency: %v", err)
	}
	if putOut.ReservedConcurrentExecutions == nil || *putOut.ReservedConcurrentExecutions != 0 {
		t.Fatalf("Put reserved = %v, want 0", putOut.ReservedConcurrentExecutions)
	}

	getOut, err := client.GetFunctionConcurrency(ctx, &awslambda.GetFunctionConcurrencyInput{
		FunctionName: aws.String("resv"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConcurrency: %v", err)
	}
	if getOut.ReservedConcurrentExecutions == nil || *getOut.ReservedConcurrentExecutions != 0 {
		t.Fatalf("Get reserved = %v, want 0", getOut.ReservedConcurrentExecutions)
	}

	// reserved=0 throttles every invoke.
	assertThrottled(t, invokeSync(t, client, "resv", noRetry))

	if _, err = client.DeleteFunctionConcurrency(ctx, &awslambda.DeleteFunctionConcurrencyInput{
		FunctionName: aws.String("resv"),
	}); err != nil {
		t.Fatalf("DeleteFunctionConcurrency: %v", err)
	}

	// The limit is gone; invokes succeed again.
	if err = invokeSync(t, client, "resv"); err != nil {
		t.Fatalf("Invoke after DeleteFunctionConcurrency: %v", err)
	}
}

// TestSDKReservedConcurrencyEnforcedUnderConcurrency drives real concurrent
// invocations against a reserved limit: a blocking handler holds `limit` slots
// in flight, invokes beyond the limit are throttled with 429, and once the held
// invocations complete the accounting resets so a fresh invoke succeeds. Run
// under -race to guard the counter.
func TestSDKReservedConcurrencyEnforcedUnderConcurrency(t *testing.T) {
	client, cloud := newSDKClient(t)
	ctx := context.Background()

	const limit = 2

	createBasicFunction(t, client, "cc")

	started := make(chan struct{}, limit)
	release := make(chan struct{})
	cloud.Lambda.RegisterHandler("cc", func(_ context.Context, p []byte) ([]byte, error) {
		started <- struct{}{}
		<-release

		return p, nil
	})

	if _, err := client.PutFunctionConcurrency(ctx, &awslambda.PutFunctionConcurrencyInput{
		FunctionName:                 aws.String("cc"),
		ReservedConcurrentExecutions: aws.Int32(limit),
	}); err != nil {
		t.Fatalf("PutFunctionConcurrency: %v", err)
	}

	// Launch `limit` invokes that each acquire a slot and then block in the
	// handler, so both slots are held simultaneously.
	var wg sync.WaitGroup

	wg.Add(limit)

	for i := 0; i < limit; i++ {
		go func() {
			defer wg.Done()

			if err := invokeSync(t, client, "cc"); err != nil {
				t.Errorf("in-flight invoke %d: unexpected error %v", i, err)
			}
		}()
	}

	// Wait until both slots are held before probing the limit.
	for i := 0; i < limit; i++ {
		<-started
	}

	// With both slots held, further invokes are throttled immediately (the
	// handler is never entered for them).
	for i := 0; i < 3; i++ {
		assertThrottled(t, invokeSync(t, client, "cc", noRetry))
	}

	// Let the held invocations finish and drain the accounting.
	close(release)
	wg.Wait()

	// The counter is back to zero (release is closed, so the handler no longer
	// blocks), so a fresh invoke acquires a slot and succeeds. The started
	// channel has room (cap == limit) for its non-blocking send.
	if err := invokeSync(t, client, "cc"); err != nil {
		t.Fatalf("Invoke after held invocations drained: %v", err)
	}
}
