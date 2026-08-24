package eventbridge_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	ebprovider "github.com/stackshy/cloudemu/v2/providers/aws/eventbridge"
	sqsprovider "github.com/stackshy/cloudemu/v2/providers/aws/sqs"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

// deliveredCount publishes one event and reports how many messages landed in the
// queue, exercising the full match -> deliver path.
func deliveredCount(t *testing.T, pattern, source, detail string) int {
	t.Helper()

	ctx := context.Background()
	opts := config.NewOptions()

	sqs := sqsprovider.New(opts)
	eb := ebprovider.New(opts)
	eb.SetSQSDeliverer(sqs)

	q, err := sqs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "target"})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if _, err := eb.PutRule(ctx, &ebdriver.RuleConfig{Name: "r", EventPattern: pattern}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if err := eb.PutTargets(ctx, "", "r", []ebdriver.Target{{ID: "1", ARN: q.ARN}}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	if _, err := eb.PutEvents(ctx, []ebdriver.Event{
		{Source: source, DetailType: "t", Detail: detail},
	}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	msgs, err := sqs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10})
	if err != nil {
		t.Fatalf("ReceiveMessages: %v", err)
	}

	return len(msgs)
}

func TestEventBridgeNestedDetailFilter(t *testing.T) {
	const pattern = `{"source":["my.app"],"detail":{"state":["running"]}}`

	if got := deliveredCount(t, pattern, "my.app", `{"state":"running"}`); got != 1 {
		t.Fatalf("matching detail: delivered %d, want 1", got)
	}

	if got := deliveredCount(t, pattern, "my.app", `{"state":"stopped"}`); got != 0 {
		t.Fatalf("non-matching detail: delivered %d, want 0", got)
	}
}

func TestEventBridgeContentOperatorFilter(t *testing.T) {
	if got := deliveredCount(t, `{"source":[{"prefix":"aws."}]}`, "aws.ec2", `{}`); got != 1 {
		t.Fatalf("prefix hit: delivered %d, want 1", got)
	}

	if got := deliveredCount(t, `{"source":[{"prefix":"aws."}]}`, "gcp.gce", `{}`); got != 0 {
		t.Fatalf("prefix miss: delivered %d, want 0", got)
	}

	if got := deliveredCount(t, `{"detail":{"count":[{"numeric":[">",10]}]}}`, "app", `{"count":42}`); got != 1 {
		t.Fatalf("numeric hit: delivered %d, want 1", got)
	}

	if got := deliveredCount(t, `{"detail":{"count":[{"numeric":[">",10]}]}}`, "app", `{"count":5}`); got != 0 {
		t.Fatalf("numeric miss: delivered %d, want 0", got)
	}

	if got := deliveredCount(t, `{"detail":{"state":[{"exists":true}]}}`, "app", `{"state":"x"}`); got != 1 {
		t.Fatalf("exists hit: delivered %d, want 1", got)
	}

	if got := deliveredCount(t, `{"detail":{"state":[{"exists":true}]}}`, "app", `{"other":"x"}`); got != 0 {
		t.Fatalf("exists miss: delivered %d, want 0", got)
	}
}
