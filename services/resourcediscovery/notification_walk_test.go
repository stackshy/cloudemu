package resourcediscovery

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/sns"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
)

func TestWalkNotification(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(0, 0))))
	n := sns.New(opts)

	if _, err := n.CreateTopic(ctx, notifdriver.TopicConfig{Name: "alerts"}); err != nil {
		t.Fatalf("create topic: %v", err)
	}

	eng := New(ProviderAWS, "123456789012", "us-east-1", &Drivers{Notification: n})

	res, err := eng.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	var found int
	for i := range res {
		if res[i].Service == ServiceNotif && res[i].Type == TypeTopic {
			found++
			if res[i].ID != "alerts" {
				t.Fatalf("topic ID = %q, want alerts", res[i].ID)
			}
		}
	}

	if found != 1 {
		t.Fatalf("expected 1 discovered topic, got %d (of %d resources)", found, len(res))
	}
}
