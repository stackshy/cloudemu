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

// deliverWithTarget publishes one event to a rule with the given target and
// returns the single delivered body.
func deliverWithTarget(t *testing.T, target ebdriver.Target, source, detail string) string {
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

	target.ARN = q.ARN

	if _, err := eb.PutRule(ctx, &ebdriver.RuleConfig{Name: "r", EventPattern: `{"source":["` + source + `"]}`}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if err := eb.PutTargets(ctx, "", "r", []ebdriver.Target{target}); err != nil {
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

	if len(msgs) != 1 {
		t.Fatalf("delivered %d messages, want 1", len(msgs))
	}

	return msgs[0].Body
}

func TestEventBridgeInputTransformer(t *testing.T) {
	it, _ := json.Marshal(map[string]any{
		"InputPathsMap": map[string]string{"st": "$.detail.state"},
		"InputTemplate": `"state is <st>"`,
	})

	body := deliverWithTarget(t, ebdriver.Target{ID: "1", InputTransformer: string(it)}, "app", `{"state":"ok"}`)

	if body != "state is ok" {
		t.Fatalf("InputTransformer body = %q, want %q", body, "state is ok")
	}
}

func TestEventBridgeConstantInput(t *testing.T) {
	body := deliverWithTarget(t, ebdriver.Target{ID: "1", Input: `{"hello":"world"}`}, "app", `{"state":"ok"}`)

	if body != `{"hello":"world"}` {
		t.Fatalf("constant Input body = %q, want the constant", body)
	}
}

func TestEventBridgeInputPath(t *testing.T) {
	body := deliverWithTarget(t, ebdriver.Target{ID: "1", InputPath: "$.detail"}, "app", `{"state":"ok"}`)

	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("InputPath body not JSON: %v (%s)", err, body)
	}

	if got["state"] != "ok" || len(got) != 1 {
		t.Fatalf("InputPath body = %s, want just the detail subtree", body)
	}
}

func TestEventBridgeDefaultEnvelopeUnchanged(t *testing.T) {
	body := deliverWithTarget(t, ebdriver.Target{ID: "1"}, "app", `{"state":"ok"}`)

	var env map[string]any
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("envelope not JSON: %v", err)
	}

	if env["source"] != "app" || env["detail-type"] != "t" {
		t.Fatalf("default envelope malformed: %s", body)
	}
}
