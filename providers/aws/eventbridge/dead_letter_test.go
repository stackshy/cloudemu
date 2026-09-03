// dead_letter_test.go verifies that a target whose dispatch fails is routed to
// its configured DeadLetterConfig queue, matching real EventBridge: a rule
// target that goes stale (its queue is deleted, or its function starts
// erroring) does not silently vanish when a DLQ is configured.
package eventbridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	ebprovider "github.com/stackshy/cloudemu/v2/providers/aws/eventbridge"
	lambdaprovider "github.com/stackshy/cloudemu/v2/providers/aws/lambda"
	sqsprovider "github.com/stackshy/cloudemu/v2/providers/aws/sqs"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	lambdadriver "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

var errHandlerFailed = errors.New("handler failed")

func deadLetterConfigJSON(t *testing.T, arn string) string {
	t.Helper()

	b, err := json.Marshal(map[string]string{"Arn": arn})
	if err != nil {
		t.Fatalf("marshal DeadLetterConfig: %v", err)
	}

	return string(b)
}

// TestDeadLetterOnStaleSQSTarget: a rule target ARN that no longer resolves to
// a live queue (deleted after PutTargets) fails dispatch; the original event is
// then routed to the target's configured DLQ.
func TestDeadLetterOnStaleSQSTarget(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()

	sqs := sqsprovider.New(opts)
	eb := ebprovider.New(opts)
	eb.SetSQSDeliverer(sqs)

	target, err := sqs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "eb-target"})
	if err != nil {
		t.Fatalf("CreateQueue(target): %v", err)
	}

	dlq, err := sqs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "eb-dlq"})
	if err != nil {
		t.Fatalf("CreateQueue(dlq): %v", err)
	}

	if _, err := eb.PutRule(ctx, &ebdriver.RuleConfig{
		Name: "r", EventPattern: `{"source":["myapp"]}`,
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if err := eb.PutTargets(ctx, "", "r", []ebdriver.Target{
		{ID: "1", ARN: target.ARN, DeadLetterConfig: deadLetterConfigJSON(t, dlq.ARN)},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	// The target queue goes stale after PutTargets — a real-world drift EventBridge
	// tolerates by DLQ'ing rather than dropping.
	if err := sqs.DeleteQueue(ctx, target.URL); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}

	if _, err := eb.PutEvents(ctx, []ebdriver.Event{
		{Source: "myapp", DetailType: "order.created", Detail: `{"orderId":"42"}`},
	}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	msgs, err := sqs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: dlq.URL, MaxMessages: 10})
	if err != nil {
		t.Fatalf("ReceiveMessages(dlq): %v", err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message on the DLQ, got %d", len(msgs))
	}

	var env map[string]any
	if err := json.Unmarshal([]byte(msgs[0].Body), &env); err != nil {
		t.Fatalf("DLQ body not JSON: %v (%s)", err, msgs[0].Body)
	}

	if env["source"] != "myapp" || env["detail-type"] != "order.created" {
		t.Fatalf("unexpected DLQ envelope: %+v", env)
	}
}

// TestNoDeadLetterConfigDropsFailedDelivery: without a DeadLetterConfig, a
// failed dispatch is dropped and PutEvents still succeeds — EventBridge
// publishing is fire-and-forget from the publisher's point of view.
func TestNoDeadLetterConfigDropsFailedDelivery(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()

	sqs := sqsprovider.New(opts)
	eb := ebprovider.New(opts)
	eb.SetSQSDeliverer(sqs)

	target, err := sqs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "eb-target"})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if _, err := eb.PutRule(ctx, &ebdriver.RuleConfig{
		Name: "r", EventPattern: `{"source":["myapp"]}`,
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if err := eb.PutTargets(ctx, "", "r", []ebdriver.Target{{ID: "1", ARN: target.ARN}}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	if err := sqs.DeleteQueue(ctx, target.URL); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}

	result, err := eb.PutEvents(ctx, []ebdriver.Event{
		{Source: "myapp", DetailType: "order.created", Detail: `{"orderId":"42"}`},
	})
	if err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	if result.SuccessCount != 1 || result.FailCount != 0 {
		t.Fatalf("PutEvents result = %+v, want SuccessCount=1 FailCount=0 (delivery failure never fails the publish)", result)
	}
}

// TestDeadLetterOnFailingLambdaTarget: a Lambda target whose handler raises is
// a genuine EventBridge invocation failure, distinct from a merely-unwired
// target — it must also be DLQ'd.
func TestDeadLetterOnFailingLambdaTarget(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()

	sqs := sqsprovider.New(opts)
	lam := lambdaprovider.New(opts)
	eb := ebprovider.New(opts)
	eb.SetSQSDeliverer(sqs)
	eb.SetLambdaInvoker(lam)

	fn, err := lam.CreateFunction(ctx, lambdadriver.FunctionConfig{
		Name: "eb-target-fn", Runtime: "go1.x", Handler: "main", Memory: 128, Timeout: 30,
	})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	lam.RegisterHandler("eb-target-fn", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errHandlerFailed
	})

	dlq, err := sqs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "eb-dlq"})
	if err != nil {
		t.Fatalf("CreateQueue(dlq): %v", err)
	}

	if _, err := eb.PutRule(ctx, &ebdriver.RuleConfig{
		Name: "r", EventPattern: `{"source":["myapp"]}`,
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if err := eb.PutTargets(ctx, "", "r", []ebdriver.Target{
		{ID: "1", ARN: fn.ARN, DeadLetterConfig: deadLetterConfigJSON(t, dlq.ARN)},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	if _, err := eb.PutEvents(ctx, []ebdriver.Event{
		{Source: "myapp", DetailType: "order.created", Detail: `{"orderId":"42"}`},
	}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	msgs, err := sqs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: dlq.URL, MaxMessages: 10})
	if err != nil {
		t.Fatalf("ReceiveMessages(dlq): %v", err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message on the DLQ, got %d", len(msgs))
	}
}

// TestSuccessfulDeliveryLeavesDLQEmpty guards against a DLQ getting a copy of
// every event regardless of outcome.
func TestSuccessfulDeliveryLeavesDLQEmpty(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()

	sqs := sqsprovider.New(opts)
	eb := ebprovider.New(opts)
	eb.SetSQSDeliverer(sqs)

	target, err := sqs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "eb-target"})
	if err != nil {
		t.Fatalf("CreateQueue(target): %v", err)
	}

	dlq, err := sqs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "eb-dlq"})
	if err != nil {
		t.Fatalf("CreateQueue(dlq): %v", err)
	}

	if _, err := eb.PutRule(ctx, &ebdriver.RuleConfig{
		Name: "r", EventPattern: `{"source":["myapp"]}`,
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if err := eb.PutTargets(ctx, "", "r", []ebdriver.Target{
		{ID: "1", ARN: target.ARN, DeadLetterConfig: deadLetterConfigJSON(t, dlq.ARN)},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	if _, err := eb.PutEvents(ctx, []ebdriver.Event{
		{Source: "myapp", DetailType: "order.created", Detail: `{"orderId":"42"}`},
	}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	msgs, err := sqs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: dlq.URL, MaxMessages: 10})
	if err != nil {
		t.Fatalf("ReceiveMessages(dlq): %v", err)
	}

	if len(msgs) != 0 {
		t.Fatalf("expected the DLQ to stay empty on successful delivery, got %d messages", len(msgs))
	}
}
