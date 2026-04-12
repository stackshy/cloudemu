package clouddns

import (
	"context"

	"github.com/stackshy/cloudemu/dns/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
)

const (
	defaultInterval         = 30
	defaultFailureThreshold = 3
	statusHealthy           = "HEALTHY"
	statusUnhealthy         = "UNHEALTHY"
)

// CreateHealthCheck creates a new Cloud DNS health check.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateHealthCheck(_ context.Context, cfg driver.HealthCheckConfig) (*driver.HealthCheckInfo, error) {
	if cfg.Endpoint == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "endpoint is required")
	}

	id := idgen.GCPID(m.opts.ProjectID, "healthChecks", idgen.GenerateID("hc-"))

	interval := cfg.IntervalSeconds
	if interval == 0 {
		interval = defaultInterval
	}

	threshold := cfg.FailureThreshold
	if threshold == 0 {
		threshold = defaultFailureThreshold
	}

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	hc := driver.HealthCheckInfo{
		ID:               id,
		Endpoint:         cfg.Endpoint,
		Port:             cfg.Port,
		Protocol:         cfg.Protocol,
		Path:             cfg.Path,
		IntervalSeconds:  interval,
		FailureThreshold: threshold,
		Status:           statusHealthy,
		Tags:             tags,
	}

	m.healthChecks.Set(id, hc)

	result := hc

	return &result, nil
}

// DeleteHealthCheck deletes a Cloud DNS health check by ID.
func (m *Mock) DeleteHealthCheck(_ context.Context, id string) error {
	if !m.healthChecks.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "health check %q not found", id)
	}

	return nil
}

// GetHealthCheck retrieves a Cloud DNS health check by ID.
func (m *Mock) GetHealthCheck(_ context.Context, id string) (*driver.HealthCheckInfo, error) {
	hc, ok := m.healthChecks.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "health check %q not found", id)
	}

	result := hc

	return &result, nil
}

// ListHealthChecks returns all Cloud DNS health checks.
func (m *Mock) ListHealthChecks(_ context.Context) ([]driver.HealthCheckInfo, error) {
	all := m.healthChecks.All()

	checks := make([]driver.HealthCheckInfo, 0, len(all))
	for _, hc := range all {
		checks = append(checks, hc)
	}

	return checks, nil
}

// UpdateHealthCheck updates an existing Cloud DNS health check.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) UpdateHealthCheck(_ context.Context, id string, cfg driver.HealthCheckConfig) (*driver.HealthCheckInfo, error) {
	hc, ok := m.healthChecks.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "health check %q not found", id)
	}

	if cfg.Endpoint != "" {
		hc.Endpoint = cfg.Endpoint
	}

	if cfg.Port != 0 {
		hc.Port = cfg.Port
	}

	if cfg.Protocol != "" {
		hc.Protocol = cfg.Protocol
	}

	if cfg.Path != "" {
		hc.Path = cfg.Path
	}

	if cfg.IntervalSeconds != 0 {
		hc.IntervalSeconds = cfg.IntervalSeconds
	}

	if cfg.FailureThreshold != 0 {
		hc.FailureThreshold = cfg.FailureThreshold
	}

	if cfg.Tags != nil {
		tags := make(map[string]string, len(cfg.Tags))
		for k, v := range cfg.Tags {
			tags[k] = v
		}

		hc.Tags = tags
	}

	m.healthChecks.Set(id, hc)

	result := hc

	return &result, nil
}

// SetHealthCheckStatus sets the status of a Cloud DNS health check.
func (m *Mock) SetHealthCheckStatus(_ context.Context, id, status string) error {
	if status != statusHealthy && status != statusUnhealthy {
		return cerrors.Newf(cerrors.InvalidArgument, "status must be %q or %q", statusHealthy, statusUnhealthy)
	}

	if !m.healthChecks.Has(id) {
		return cerrors.Newf(cerrors.NotFound, "health check %q not found", id)
	}

	m.healthChecks.Update(id, func(hc driver.HealthCheckInfo) driver.HealthCheckInfo {
		hc.Status = status
		return hc
	})

	return nil
}
