package resourcediscovery

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/sqs"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

func TestWalkMessageQueue(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(0, 0))))
	q := sqs.New(opts)

	if _, err := q.CreateQueue(ctx, mqdriver.QueueConfig{Name: "orders"}); err != nil {
		t.Fatalf("create queue: %v", err)
	}

	eng := New(ProviderAWS, "123456789012", "us-east-1", &Drivers{MessageQueue: q})

	res, err := eng.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	var found int
	for i := range res {
		if res[i].Service == ServiceQueue && res[i].Type == TypeQueue {
			found++
			if res[i].ID != "orders" {
				t.Fatalf("queue ID = %q, want orders", res[i].ID)
			}
		}
	}

	if found != 1 {
		t.Fatalf("expected 1 discovered queue, got %d (of %d resources)", found, len(res))
	}
}
