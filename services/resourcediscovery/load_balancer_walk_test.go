package resourcediscovery

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/elbv2"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

func TestWalkLoadBalancer(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(0, 0))))
	lb := elbv2.New(opts)

	if _, err := lb.CreateLoadBalancer(ctx, lbdriver.LBConfig{Name: "web-lb", Type: "application"}); err != nil {
		t.Fatalf("create load balancer: %v", err)
	}

	eng := New(ProviderAWS, "123456789012", "us-east-1", &Drivers{LoadBalancer: lb})

	res, err := eng.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	var found int
	for i := range res {
		if res[i].Service == ServiceLB && res[i].Type == TypeLoadBalancer {
			found++
			if res[i].ID != "web-lb" {
				t.Fatalf("load balancer ID = %q, want web-lb", res[i].ID)
			}
		}
	}

	if found != 1 {
		t.Fatalf("expected 1 discovered load balancer, got %d (of %d resources)", found, len(res))
	}
}
