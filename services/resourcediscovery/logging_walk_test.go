package resourcediscovery

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs"
	loggingdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

func TestWalkLogging(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(0, 0))))
	l := cloudwatchlogs.New(opts)

	if _, err := l.CreateLogGroup(ctx, loggingdriver.LogGroupConfig{Name: "/aws/lambda/fn"}); err != nil {
		t.Fatalf("create log group: %v", err)
	}

	eng := New(ProviderAWS, "123456789012", "us-east-1", &Drivers{Logging: l})

	res, err := eng.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	var found int
	for i := range res {
		if res[i].Service == ServiceLogging && res[i].Type == TypeLogGroup {
			found++
			if res[i].ID != "/aws/lambda/fn" {
				t.Fatalf("log group ID = %q, want /aws/lambda/fn", res[i].ID)
			}
		}
	}

	if found != 1 {
		t.Fatalf("expected 1 discovered log group, got %d (of %d resources)", found, len(res))
	}
}
