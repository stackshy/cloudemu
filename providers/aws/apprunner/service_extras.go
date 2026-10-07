package apprunner

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// settleWindow is how long a resource reports a transient status under async
// settling.
const settleWindow = settle.DefaultClusterSettle

// AWS/AppRunner metrics are published on the ServiceName and ServiceID
// dimensions. The emulator runs no web workload, so traffic-driven metrics
// (Requests, status counts, latency, CPU and memory) have no samples; the
// service-level ActiveInstances gauge is published as 0 (no requests are being
// processed) when a service is created, paused or resumed.
const (
	metricsNamespace = "AWS/AppRunner"
	unitCount        = "Count"
)

// publishServiceMetrics emits the ActiveInstances gauge of a service.
func (m *Mock) publishServiceMetrics(svc *driver.Service) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: metricsNamespace, MetricName: "ActiveInstances", Value: 0, Unit: unitCount, Timestamp: m.opts.Clock.Now(),
		Dimensions: map[string]string{"ServiceName": svc.ServiceName, "ServiceID": svc.ServiceID},
	}})
}

// logGroupNames are the service and application log groups App Runner writes a
// service's logs to.
func logGroupNames(svc *driver.Service) []string {
	base := "/aws/apprunner/" + svc.ServiceName + "/" + svc.ServiceID

	return []string{base + "/service", base + "/application"}
}

// createLogGroups creates a service's log groups when CloudWatch Logs is wired.
func (m *Mock) createLogGroups(ctx context.Context, svc *driver.Service) {
	if m.logs == nil {
		return
	}

	for _, name := range logGroupNames(svc) {
		_, _ = m.logs.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: name})
	}
}

// deleteLogGroups removes a deleted service's log groups.
func (m *Mock) deleteLogGroups(ctx context.Context, svc *driver.Service) {
	if m.logs == nil {
		return
	}

	for _, name := range logGroupNames(svc) {
		_ = m.logs.DeleteLogGroup(ctx, name)
	}
}
