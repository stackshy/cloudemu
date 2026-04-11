// Package dns provides a portable DNS API with cross-cutting concerns.
package dns

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/dns/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

type DNS struct {
	driver   driver.DNS
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

func NewDNS(d driver.DNS, opts ...Option) *DNS {
	dn := &DNS{driver: d}
	for _, opt := range opts {
		opt(dn)
	}

	return dn
}

type Option func(*DNS)

func WithRecorder(r *recorder.Recorder) Option     { return func(d *DNS) { d.recorder = r } }
func WithMetrics(m *metrics.Collector) Option      { return func(d *DNS) { d.metrics = m } }
func WithRateLimiter(l *ratelimit.Limiter) Option  { return func(d *DNS) { d.limiter = l } }
func WithErrorInjection(i *inject.Injector) Option { return func(d *DNS) { d.injector = i } }
func WithLatency(dur time.Duration) Option         { return func(d *DNS) { d.latency = dur } }

func (d *DNS) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if d.injector != nil {
		if err := d.injector.Check("dns", op); err != nil {
			d.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if d.limiter != nil {
		if err := d.limiter.Allow(); err != nil {
			d.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if d.latency > 0 {
		time.Sleep(d.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if d.metrics != nil {
		labels := map[string]string{"service": "dns", "operation": op}
		d.metrics.Counter("calls_total", 1, labels)
		d.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			d.metrics.Counter("errors_total", 1, labels)
		}
	}

	d.rec(op, input, out, err, dur)

	return out, err
}

func (d *DNS) rec(op string, input, output any, err error, dur time.Duration) {
	if d.recorder != nil {
		d.recorder.Record("dns", op, input, output, err, dur)
	}
}

func (d *DNS) CreateZone(ctx context.Context, config driver.ZoneConfig) (*driver.ZoneInfo, error) {
	out, err := d.do(ctx, "CreateZone", config, func() (any, error) { return d.driver.CreateZone(ctx, config) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.ZoneInfo), nil
}

func (d *DNS) DeleteZone(ctx context.Context, id string) error {
	_, err := d.do(ctx, "DeleteZone", id, func() (any, error) { return nil, d.driver.DeleteZone(ctx, id) })
	return err
}

func (d *DNS) GetZone(ctx context.Context, id string) (*driver.ZoneInfo, error) {
	out, err := d.do(ctx, "GetZone", id, func() (any, error) { return d.driver.GetZone(ctx, id) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.ZoneInfo), nil
}

func (d *DNS) ListZones(ctx context.Context) ([]driver.ZoneInfo, error) {
	out, err := d.do(ctx, "ListZones", nil, func() (any, error) { return d.driver.ListZones(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.ZoneInfo), nil
}

//nolint:gocritic // config passed by value to match driver.DNS interface pattern
func (d *DNS) CreateRecord(ctx context.Context, config driver.RecordConfig) (*driver.RecordInfo, error) {
	out, err := d.do(ctx, "CreateRecord", config, func() (any, error) { return d.driver.CreateRecord(ctx, config) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.RecordInfo), nil
}

func (d *DNS) DeleteRecord(ctx context.Context, zoneID, name, recordType string) error {
	_, err := d.do(ctx, "DeleteRecord", name, func() (any, error) { return nil, d.driver.DeleteRecord(ctx, zoneID, name, recordType) })
	return err
}

func (d *DNS) GetRecord(ctx context.Context, zoneID, name, recordType string) (*driver.RecordInfo, error) {
	out, err := d.do(ctx, "GetRecord", name, func() (any, error) { return d.driver.GetRecord(ctx, zoneID, name, recordType) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.RecordInfo), nil
}

func (d *DNS) ListRecords(ctx context.Context, zoneID string) ([]driver.RecordInfo, error) {
	out, err := d.do(ctx, "ListRecords", zoneID, func() (any, error) { return d.driver.ListRecords(ctx, zoneID) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.RecordInfo), nil
}

//nolint:gocritic // config passed by value to match driver.DNS interface pattern
func (d *DNS) UpdateRecord(ctx context.Context, config driver.RecordConfig) (*driver.RecordInfo, error) {
	out, err := d.do(ctx, "UpdateRecord", config, func() (any, error) { return d.driver.UpdateRecord(ctx, config) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.RecordInfo), nil
}

//nolint:gocritic // config passed by value to match driver.DNS interface pattern
func (d *DNS) CreateHealthCheck(ctx context.Context, config driver.HealthCheckConfig) (*driver.HealthCheckInfo, error) {
	out, err := d.do(ctx, "CreateHealthCheck", config, func() (any, error) {
		return d.driver.CreateHealthCheck(ctx, config)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.HealthCheckInfo), nil
}

func (d *DNS) DeleteHealthCheck(ctx context.Context, id string) error {
	_, err := d.do(ctx, "DeleteHealthCheck", id, func() (any, error) {
		return nil, d.driver.DeleteHealthCheck(ctx, id)
	})

	return err
}

func (d *DNS) GetHealthCheck(ctx context.Context, id string) (*driver.HealthCheckInfo, error) {
	out, err := d.do(ctx, "GetHealthCheck", id, func() (any, error) { return d.driver.GetHealthCheck(ctx, id) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.HealthCheckInfo), nil
}

func (d *DNS) ListHealthChecks(ctx context.Context) ([]driver.HealthCheckInfo, error) {
	out, err := d.do(ctx, "ListHealthChecks", nil, func() (any, error) { return d.driver.ListHealthChecks(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.HealthCheckInfo), nil
}

//nolint:gocritic // config passed by value to match driver.DNS interface pattern
func (d *DNS) UpdateHealthCheck(ctx context.Context, id string, config driver.HealthCheckConfig) (*driver.HealthCheckInfo, error) {
	out, err := d.do(ctx, "UpdateHealthCheck", config, func() (any, error) {
		return d.driver.UpdateHealthCheck(ctx, id, config)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.HealthCheckInfo), nil
}

func (d *DNS) SetHealthCheckStatus(ctx context.Context, id, status string) error {
	_, err := d.do(ctx, "SetHealthCheckStatus", id, func() (any, error) {
		return nil, d.driver.SetHealthCheckStatus(ctx, id, status)
	})

	return err
}
