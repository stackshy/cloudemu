package resourcediscovery

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	monitoringdriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

func TestWalkMonitoring(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(0, 0))))
	cw := cloudwatch.New(opts)

	cfg := monitoringdriver.AlarmConfig{
		Name:               "cpu-high",
		Namespace:          "AWS/EC2",
		MetricName:         "CPUUtilization",
		ComparisonOperator: "GreaterThanThreshold",
		Threshold:          80,
	}
	if err := cw.CreateAlarm(ctx, cfg); err != nil {
		t.Fatalf("create alarm: %v", err)
	}

	eng := New(ProviderAWS, "123456789012", "us-east-1", &Drivers{Monitoring: cw})

	res, err := eng.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	var found int
	for i := range res {
		if res[i].Service == ServiceMonitoring && res[i].Type == TypeAlarm {
			found++
			if res[i].ID != "cpu-high" {
				t.Fatalf("alarm ID = %q, want cpu-high", res[i].ID)
			}
		}
	}

	if found != 1 {
		t.Fatalf("expected 1 discovered alarm, got %d (of %d resources)", found, len(res))
	}
}
