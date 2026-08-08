package resourcediscovery

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/route53"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
)

func TestWalkDNS(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(0, 0))))
	z := route53.New(opts)

	if _, err := z.CreateZone(ctx, dnsdriver.ZoneConfig{Name: "example.com"}); err != nil {
		t.Fatalf("create zone: %v", err)
	}

	eng := New(ProviderAWS, "123456789012", "us-east-1", &Drivers{DNS: z})

	res, err := eng.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	var found int
	for i := range res {
		if res[i].Service == ServiceDNS && res[i].Type == TypeZone {
			found++
			if res[i].ID != "example.com" {
				t.Fatalf("zone ID = %q, want example.com", res[i].ID)
			}
		}
	}

	if found != 1 {
		t.Fatalf("expected 1 discovered zone, got %d (of %d resources)", found, len(res))
	}
}
