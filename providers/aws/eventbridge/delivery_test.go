package eventbridge_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	ebprovider "github.com/stackshy/cloudemu/v2/providers/aws/eventbridge"
	sqsprovider "github.com/stackshy/cloudemu/v2/providers/aws/sqs"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

func TestEventBridgeToSQSDelivery(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()

	sqs := sqsprovider.New(opts)
	eb := ebprovider.New(opts)
	eb.SetSQSDeliverer(sqs)

	q, err := sqs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "eb-target"})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if _, err := eb.PutRule(ctx, &ebdriver.RuleConfig{
		Name: "r-all", EventPattern: `{"source":["myapp"]}`,
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if err := eb.PutTargets(ctx, "", "r-all", []ebdriver.Target{
		{ID: "1", ARN: q.ARN},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	if _, err := eb.PutEvents(ctx, []ebdriver.Event{
		{Source: "myapp", DetailType: "order.created", Detail: `{"orderId":"42"}`},
	}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	msgs, err := sqs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10})
	if err != nil {
		t.Fatalf("ReceiveMessages: %v", err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected 1 delivered event, got %d", len(msgs))
	}

	var env map[string]any
	if err := json.Unmarshal([]byte(msgs[0].Body), &env); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, msgs[0].Body)
	}

	if env["detail-type"] != "order.created" || env["source"] != "myapp" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
}
