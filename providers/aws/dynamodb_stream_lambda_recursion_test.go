package aws_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/providers/aws"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
	sdriver "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// TestDynamoDBStreamLambdaWriteBackDoesNotRecurseUnbounded reproduces a real
// pattern: a Lambda mapped to a DynamoDB Stream writes back into its own
// source table (mark-processed / audit-append / status-bump). Because ESM
// delivery runs synchronously on the same goroutine unwinding from
// PutItem/UpdateItem/DeleteItem, an unguarded handler recurses
// PutItem -> deliver -> Invoke -> handler -> PutItem -> ... without bound,
// which previously blew the goroutine stack and crashed the whole process
// with an unrecoverable "fatal error: stack overflow" (recover() cannot catch
// it). The recursive-loop guard in lambda.Mock.InvokeExternal must cap the
// chain, matching AWS Lambda's own recursive-loop detection of ~16
// invocations per chain of requests, so a single top-level PutItem returns
// normally instead of taking the process down with it.
func TestDynamoDBStreamLambdaWriteBackDoesNotRecurseUnbounded(t *testing.T) {
	p := aws.New()
	ctx := context.Background()

	const table = "orders"

	if err := p.DynamoDB.CreateTable(ctx, driver.TableConfig{
		Name: table, PartitionKey: "Id", StreamEnabled: true, StreamViewType: "NEW_IMAGE",
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	cfg, err := p.DynamoDB.DescribeTable(ctx, table)
	if err != nil {
		t.Fatalf("DescribeTable: %v", err)
	}

	if cfg.StreamArn == "" {
		t.Fatal("table has no StreamArn")
	}

	const function = "mark-processed"

	if _, err := p.Lambda.CreateFunction(ctx, sdriver.FunctionConfig{Name: function}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	var invocations atomic.Int64

	// A well-behaved handler forwards the ctx it was invoked with into its own
	// downstream calls, exactly as this test's handler does here — this is the
	// channel the recursive-loop guard rides on.
	p.Lambda.RegisterHandler(function, func(ctx context.Context, _ []byte) ([]byte, error) {
		invocations.Add(1)

		// The write-back: the handler marks the very item it was triggered by
		// as processed, in its own source table.
		if err := p.DynamoDB.PutItem(ctx, table, map[string]any{"Id": "1", "Processed": true}); err != nil {
			return nil, err
		}

		return nil, nil
	})

	if _, err := p.Lambda.CreateEventSourceMapping(ctx, sdriver.EventSourceMappingConfig{
		EventSourceArn: cfg.StreamArn, FunctionName: function, Enabled: true,
	}); err != nil {
		t.Fatalf("CreateEventSourceMapping: %v", err)
	}

	// The single top-level write that starts the chain. If this returns at
	// all (rather than crashing the process with a stack overflow), the guard
	// held.
	if err := p.DynamoDB.PutItem(ctx, table, map[string]any{"Id": "1"}); err != nil {
		t.Fatalf("PutItem: %v", err)
	}

	// Each recursive PutItem trips one more level of the guard, so the chain
	// runs to exactly MaxDepth invocations before InvokeExternal starts
	// silently dropping delivery.
	if got := invocations.Load(); got != recursionguard.MaxDepth {
		t.Fatalf("handler invoked %d times, want exactly %d (recursive-loop guard did not bound the chain)",
			got, recursionguard.MaxDepth)
	}
}
