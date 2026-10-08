package apprunner

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// AWS/AppRunner metrics are published on the ServiceName and ServiceID
// dimensions. The emulator runs no web workload, so traffic-driven metrics
// (Requests, status counts, latency, CPU and memory) have no samples; the
// service-level ActiveInstances gauge is published as 1 while a service runs and
// 0 while it is paused, when a service is created, paused or resumed.
const (
	metricsNamespace = "AWS/AppRunner"
	unitCount        = "Count"
)

// publishServiceMetrics emits the ActiveInstances gauge of a service: one instance
// while it runs, none while it is paused.
func (m *Mock) publishServiceMetrics(svc *driver.Service) {
	if m.monitoring == nil {
		return
	}

	active := 0.0
	if svc.Status == driver.StatusRunning {
		active = 1
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: metricsNamespace, MetricName: "ActiveInstances", Value: active, Unit: unitCount, Timestamp: m.opts.Clock.Now(),
		Dimensions: map[string]string{"ServiceName": svc.ServiceName, "ServiceID": svc.ServiceID},
	}})
}
