package aws_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/providers/aws"
	"github.com/stackshy/cloudemu/v2/providers/aws/s3"
	sdriver "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// TestS3LambdaNotificationWriteBackDoesNotRecurseUnbounded is the S3 -> Lambda
// counterpart to the DynamoDB Streams regression test: it shares the exact
// same latent bug (a notified handler writing an object back into the same
// bucket re-enters PutObject -> notify -> deliver -> InvokeExternal -> handler
// -> PutObject -> ... synchronously) and is bounded by the same guard, since
// InvokeExternal is the single choke point both delivery paths funnel
// through.
func TestS3LambdaNotificationWriteBackDoesNotRecurseUnbounded(t *testing.T) {
	p := aws.New()
	ctx := context.Background()

	const bucket = "uploads"

	if err := p.S3.CreateBucket(ctx, bucket); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	const function = "on-upload"

	if _, err := p.Lambda.CreateFunction(ctx, sdriver.FunctionConfig{Name: function}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	var invocations atomic.Int64

	// A well-behaved handler forwards the ctx it was invoked with into its own
	// downstream calls, exactly as this test's handler does here — this is the
	// channel the recursive-loop guard rides on.
	p.Lambda.RegisterHandler(function, func(ctx context.Context, _ []byte) ([]byte, error) {
		invocations.Add(1)

		// The write-back: the handler re-uploads to the very key that
		// triggered it, staying inside the bucket/event selector it's mapped to.
		if err := p.S3.PutObject(ctx, bucket, "in.csv", []byte("data"), "text/csv", nil); err != nil {
			return nil, err
		}

		return nil, nil
	})

	if err := p.S3.PutBucketNotification(ctx, bucket, []s3.BucketNotification{{
		ID: "on-upload", Target: s3.NotifyLambda, ARN: "on-upload",
		Events: []string{"s3:ObjectCreated:*"},
	}}); err != nil {
		t.Fatalf("PutBucketNotification: %v", err)
	}

	// The single top-level write that starts the chain. If this returns at
	// all (rather than crashing the process with a stack overflow), the guard
	// held.
	if err := p.S3.PutObject(ctx, bucket, "in.csv", []byte("data"), "text/csv", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	// Each recursive PutObject trips one more level of the guard, so the chain
	// runs to exactly MaxDepth invocations before InvokeExternal starts
	// silently dropping delivery.
	if got := invocations.Load(); got != recursionguard.MaxDepth {
		t.Fatalf("handler invoked %d times, want exactly %d (recursive-loop guard did not bound the chain)",
			got, recursionguard.MaxDepth)
	}
}
