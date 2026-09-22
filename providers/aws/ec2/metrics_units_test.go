package ec2

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// TestInstanceMetricUnits pins the CloudWatch units real EC2 publishes its
// basic-monitoring instance metrics with, for both the launch backfill and the
// lifecycle (stop) emission: CPUUtilization is Percent, NetworkIn/NetworkOut are
// Bytes, and DiskReadOps/DiskWriteOps are Count.
func TestInstanceMetricUnits(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	cw := cloudwatch.New(opts)
	m := New(opts)
	m.SetMonitoring(cw)

	ctx := context.Background()

	insts, err := m.RunInstances(ctx, defaultConfig(), 1)
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	id := insts[0].ID

	want := map[string]string{
		"CPUUtilization": "Percent",
		"NetworkIn":      "Bytes",
		"NetworkOut":     "Bytes",
		"DiskReadOps":    "Count",
		"DiskWriteOps":   "Count",
	}

	assertUnits := func(phase string) {
		t.Helper()

		for name, unit := range want {
			res, gerr := cw.GetMetricData(ctx, mondriver.GetMetricInput{
				Namespace: "AWS/EC2", MetricName: name,
				Dimensions: map[string]string{"InstanceId": id},
				StartTime:  fc.Now().Add(-time.Hour), EndTime: fc.Now().Add(time.Minute),
				Period: 60, Stat: "Average",
			})
			if gerr != nil {
				t.Fatalf("%s GetMetricData %s: %v", phase, name, gerr)
			}

			if len(res.Values) == 0 {
				t.Fatalf("%s: no %s datapoints for %s", phase, name, id)
			}

			if res.Unit != unit {
				t.Errorf("%s: %s unit = %q, want %q", phase, name, res.Unit, unit)
			}
		}
	}

	assertUnits("launch")

	fc.Advance(10 * time.Minute)

	if err := m.StopInstances(ctx, []string{id}); err != nil {
		t.Fatalf("StopInstances: %v", err)
	}

	// Query only the post-stop window so the unit comes from the lifecycle datum.
	for name, unit := range want {
		res, gerr := cw.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace: "AWS/EC2", MetricName: name,
			Dimensions: map[string]string{"InstanceId": id},
			StartTime:  fc.Now().Add(-time.Second), EndTime: fc.Now().Add(time.Minute),
			Period: 60, Stat: "Average",
		})
		if gerr != nil {
			t.Fatalf("stop GetMetricData %s: %v", name, gerr)
		}

		if len(res.Values) == 0 || res.Unit != unit {
			t.Errorf("stop: %s values=%v unit=%q, want unit %q", name, res.Values, res.Unit, unit)
		}
	}
}
