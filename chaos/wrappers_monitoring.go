package chaos

import (
	"context"

	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
)

// chaosMonitoring wraps a monitoring driver. Hot-path: metric and alarm CRUD.
// Notification channels and alarm history delegate through.
type chaosMonitoring struct {
	mondriver.Monitoring
	engine *Engine
}

// WrapMonitoring returns a monitoring driver that consults engine on metric
// and alarm calls.
func WrapMonitoring(inner mondriver.Monitoring, engine *Engine) mondriver.Monitoring {
	return &chaosMonitoring{Monitoring: inner, engine: engine}
}

func (c *chaosMonitoring) PutMetricData(ctx context.Context, data []mondriver.MetricDatum) error {
	if err := applyChaos(ctx, c.engine, "monitoring", "PutMetricData"); err != nil {
		return err
	}

	return c.Monitoring.PutMetricData(ctx, data)
}

//nolint:gocritic // input is a value type by interface contract
func (c *chaosMonitoring) GetMetricData(
	ctx context.Context, input mondriver.GetMetricInput,
) (*mondriver.MetricDataResult, error) {
	if err := applyChaos(ctx, c.engine, "monitoring", "GetMetricData"); err != nil {
		return nil, err
	}

	return c.Monitoring.GetMetricData(ctx, input)
}

func (c *chaosMonitoring) ListMetrics(ctx context.Context, namespace string) ([]string, error) {
	if err := applyChaos(ctx, c.engine, "monitoring", "ListMetrics"); err != nil {
		return nil, err
	}

	return c.Monitoring.ListMetrics(ctx, namespace)
}

//nolint:gocritic // cfg is a value type by interface contract
func (c *chaosMonitoring) CreateAlarm(ctx context.Context, cfg mondriver.AlarmConfig) error {
	if err := applyChaos(ctx, c.engine, "monitoring", "CreateAlarm"); err != nil {
		return err
	}

	return c.Monitoring.CreateAlarm(ctx, cfg)
}

func (c *chaosMonitoring) DeleteAlarm(ctx context.Context, name string) error {
	if err := applyChaos(ctx, c.engine, "monitoring", "DeleteAlarm"); err != nil {
		return err
	}

	return c.Monitoring.DeleteAlarm(ctx, name)
}

func (c *chaosMonitoring) DescribeAlarms(ctx context.Context, names []string) ([]mondriver.AlarmInfo, error) {
	if err := applyChaos(ctx, c.engine, "monitoring", "DescribeAlarms"); err != nil {
		return nil, err
	}

	return c.Monitoring.DescribeAlarms(ctx, names)
}
