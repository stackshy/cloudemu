package lambda

import (
	"context"
	"errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// TestDeliverEventSourceBatch verifies DeliverEventSourceBatch invokes only the
// enabled mappings whose EventSourceArn matches, backing DynamoDB-stream/SQS/
// Kinesis event-source-mapping delivery.
func TestDeliverEventSourceBatch(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	var got []string
	m.RegisterHandler("my-func", func(_ context.Context, payload []byte) ([]byte, error) {
		got = append(got, string(payload))
		return nil, nil
	})

	const streamARN = "arn:aws:dynamodb:us-east-1:000000000000:table/orders/stream/2025-01-01T00:00:00.000"

	// Enabled mapping on the stream ARN -> invoked.
	if _, err := m.CreateEventSourceMapping(ctx, driver.EventSourceMappingConfig{
		EventSourceArn: streamARN, FunctionName: "my-func", Enabled: true,
	}); err != nil {
		t.Fatalf("CreateEventSourceMapping(enabled): %v", err)
	}

	// Disabled mapping on the same ARN -> skipped.
	if _, err := m.CreateEventSourceMapping(ctx, driver.EventSourceMappingConfig{
		EventSourceArn: streamARN, FunctionName: "my-func", Enabled: false,
	}); err != nil {
		t.Fatalf("CreateEventSourceMapping(disabled): %v", err)
	}

	// Enabled mapping on a different ARN -> not matched.
	if _, err := m.CreateEventSourceMapping(ctx, driver.EventSourceMappingConfig{
		EventSourceArn: "arn:aws:sqs:us-east-1:000000000000:other", FunctionName: "my-func", Enabled: true,
	}); err != nil {
		t.Fatalf("CreateEventSourceMapping(other): %v", err)
	}

	delivered, err := m.DeliverEventSourceBatch(ctx, streamARN, []byte(`{"Records":[]}`))
	if err != nil {
		t.Fatalf("DeliverEventSourceBatch: %v", err)
	}

	if !delivered {
		t.Fatal("delivered = false, want true (a mapping matched)")
	}

	if len(got) != 1 {
		t.Fatalf("invocations = %d, want exactly one (enabled + matching only)", len(got))
	}

	if got[0] != `{"Records":[]}` {
		t.Fatalf("delivered payload = %q", got[0])
	}

	// No mapping targets an unrelated ARN -> delivered reports false, distinct
	// from a mapping that ran and succeeded.
	delivered, err = m.DeliverEventSourceBatch(ctx, "arn:aws:sqs:us-east-1:000000000000:unmapped", []byte(`{}`))
	if err != nil {
		t.Fatalf("DeliverEventSourceBatch(unmapped): %v", err)
	}

	if delivered {
		t.Fatal("delivered = true for an ARN with no matching mapping")
	}
}

// TestDeliverEventSourceBatchDoesNotAbortSiblings verifies that when two enabled
// mappings target the same source, one whose function raises does not starve the
// other: both mappings run and the failure is surfaced after all have run (real
// AWS pollers are independent, so a sibling's error must not skip a mapping).
func TestDeliverEventSourceBatchDoesNotAbortSiblings(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	failCfg := defaultFuncConfig()
	failCfg.Name = "fail-func"

	okCfg := defaultFuncConfig()
	okCfg.Name = "ok-func"

	if _, err := m.CreateFunction(ctx, failCfg); err != nil {
		t.Fatalf("CreateFunction(fail): %v", err)
	}

	if _, err := m.CreateFunction(ctx, okCfg); err != nil {
		t.Fatalf("CreateFunction(ok): %v", err)
	}

	m.RegisterHandler("fail-func", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("boom")
	})

	var okRan bool
	m.RegisterHandler("ok-func", func(_ context.Context, _ []byte) ([]byte, error) {
		okRan = true
		return nil, nil
	})

	const streamARN = "arn:aws:dynamodb:us-east-1:000000000000:table/orders/stream/2025-01-01T00:00:00.000"

	for _, fn := range []string{"fail-func", "ok-func"} {
		if _, err := m.CreateEventSourceMapping(ctx, driver.EventSourceMappingConfig{
			EventSourceArn: streamARN, FunctionName: fn, Enabled: true,
		}); err != nil {
			t.Fatalf("CreateEventSourceMapping(%s): %v", fn, err)
		}
	}

	delivered, err := m.DeliverEventSourceBatch(ctx, streamARN, []byte(`{"Records":[]}`))
	if !delivered {
		t.Fatal("delivered = false, want true (mappings matched)")
	}

	if err == nil {
		t.Fatal("err = nil, want the failing mapping's error surfaced")
	}

	if !okRan {
		t.Fatal("ok-func did not run: a sibling mapping's failure aborted delivery")
	}
}
