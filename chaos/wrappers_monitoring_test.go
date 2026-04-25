package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu"
	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
)

func newChaosMonitoring(t *testing.T) (mondriver.Monitoring, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	return chaos.WrapMonitoring(cloudemu.NewAWS().CloudWatch, e), e
}

func TestWrapMonitoringPutMetricDataChaos(t *testing.T) {
	m, e := newChaosMonitoring(t)
	ctx := context.Background()
	data := []mondriver.MetricDatum{{Namespace: "AWS/EC2", MetricName: "CPU", Value: 1, Timestamp: time.Now()}}

	if err := m.PutMetricData(ctx, data); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("monitoring", time.Hour))

	if err := m.PutMetricData(ctx, data); err == nil {
		t.Error("expected chaos error on PutMetricData")
	}
}

func TestWrapMonitoringGetMetricDataChaos(t *testing.T) {
	m, e := newChaosMonitoring(t)
	ctx := context.Background()
	in := mondriver.GetMetricInput{
		Namespace: "AWS/EC2", MetricName: "CPU",
		StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(), Period: 60, Stat: "Average",
	}

	e.Apply(chaos.ServiceOutage("monitoring", time.Hour))

	if _, err := m.GetMetricData(ctx, in); err == nil {
		t.Error("expected chaos error on GetMetricData")
	}
}

func TestWrapMonitoringListMetricsChaos(t *testing.T) {
	m, e := newChaosMonitoring(t)
	ctx := context.Background()

	if _, err := m.ListMetrics(ctx, "AWS/EC2"); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("monitoring", time.Hour))

	if _, err := m.ListMetrics(ctx, "AWS/EC2"); err == nil {
		t.Error("expected chaos error on ListMetrics")
	}
}

func TestWrapMonitoringCreateAlarmChaos(t *testing.T) {
	m, e := newChaosMonitoring(t)
	ctx := context.Background()
	cfg := mondriver.AlarmConfig{
		Name: "a", Namespace: "AWS/EC2", MetricName: "CPU",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 80, Period: 60, EvaluationPeriods: 1, Stat: "Average",
	}

	e.Apply(chaos.ServiceOutage("monitoring", time.Hour))

	if err := m.CreateAlarm(ctx, cfg); err == nil {
		t.Error("expected chaos error on CreateAlarm")
	}
}

func TestWrapMonitoringDeleteAlarmChaos(t *testing.T) {
	m, e := newChaosMonitoring(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("monitoring", time.Hour))

	if err := m.DeleteAlarm(ctx, "a"); err == nil {
		t.Error("expected chaos error on DeleteAlarm")
	}
}

func TestWrapMonitoringDescribeAlarmsChaos(t *testing.T) {
	m, e := newChaosMonitoring(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("monitoring", time.Hour))

	if _, err := m.DescribeAlarms(ctx, nil); err == nil {
		t.Error("expected chaos error on DescribeAlarms")
	}
}
