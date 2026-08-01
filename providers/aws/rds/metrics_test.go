package rds

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func TestInstanceMetricsEmitted(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))),
		config.WithRegion("us-east-1"),
		config.WithAccountID("123456789012"),
	)

	cw := cloudwatch.New(opts)
	m := New(opts)
	m.SetMonitoring(cw)

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db", Engine: "mysql"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	names, err := cw.ListMetrics(ctx, "AWS/RDS")
	if err != nil {
		t.Fatalf("ListMetrics: %v", err)
	}

	got := make(map[string]bool, len(names))
	for _, n := range names {
		got[n] = true
	}

	for _, want := range []string{
		"CPUUtilization", "DatabaseConnections", "FreeableMemory", "FreeStorageSpace",
		"ReadIOPS", "WriteIOPS", "ReadLatency", "WriteLatency",
		"NetworkReceiveThroughput", "NetworkTransmitThroughput",
	} {
		if !got[want] {
			t.Errorf("metric %q not emitted (have %v)", want, names)
		}
	}
}
