package route53

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/dns/driver"
)

const (
	defaultInterval         = 30
	defaultFailureThreshold = 3
	statusHealthy           = "HEALTHY"
	statusUnhealthy         = "UNHEALTHY"
	healthCheckTCP          = "TCP"
	typeCalculated          = "CALCULATED"
	typeCloudWatchMetric    = "CLOUDWATCH_METRIC"
	typeRecoveryControl     = "RECOVERY_CONTROL"
	typeHTTPStrMatch        = "HTTP_STR_MATCH"
	typeHTTPSStrMatch       = "HTTPS_STR_MATCH"

	// Limits from the Route 53 HealthCheckConfig reference.
	maxSearchStringLen   = 255
	maxChildHealthChecks = 256
)

// CreateHealthCheck creates a new Route 53 health check.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateHealthCheck(_ context.Context, cfg driver.HealthCheckConfig) (*driver.HealthCheckInfo, error) {
	interval, threshold := cfg.IntervalSeconds, cfg.FailureThreshold

	// CALCULATED, CLOUDWATCH_METRIC and RECOVERY_CONTROL checks do no probing,
	// so real Route 53 gives them no request interval or failure threshold.
	if probes(cfg.Protocol) {
		interval = valueOrDefault(interval, defaultInterval)
		threshold = valueOrDefault(threshold, defaultFailureThreshold)
	}

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	hc := driver.HealthCheckInfo{
		Endpoint:         cfg.Endpoint,
		Port:             cfg.Port,
		Protocol:         cfg.Protocol,
		Path:             cfg.Path,
		IntervalSeconds:  interval,
		FailureThreshold: threshold,
		Status:           statusHealthy,
		Tags:             tags,
	}

	applyRoute53Fields(&hc, &cfg)

	if err := validateHealthCheck(&hc); err != nil {
		return nil, err
	}

	hc.ID = idgen.GenerateID("hc-")
	m.healthChecks.Set(hc.ID, hc)

	result := hc

	return &result, nil
}

// probes reports whether a health check type sends requests to an endpoint.
func probes(protocol string) bool {
	switch protocol {
	case typeCalculated, typeCloudWatchMetric, typeRecoveryControl:
		return false
	default:
		return true
	}
}

func valueOrDefault(v, def int) int {
	if v == 0 {
		return def
	}

	return v
}

// applyRoute53Fields copies the Route 53 only fields that cfg sets onto hc.
// Fields cfg leaves unset keep their value in hc.
func applyRoute53Fields(hc *driver.HealthCheckInfo, cfg *driver.HealthCheckConfig) {
	if cfg.SearchString != "" {
		hc.SearchString = cfg.SearchString
	}

	if cfg.Inverted != nil {
		hc.Inverted = *cfg.Inverted
	}

	if cfg.HealthThreshold != nil {
		hc.HealthThreshold = *cfg.HealthThreshold
	}

	if cfg.ChildHealthChecks != nil {
		hc.ChildHealthChecks = append([]string{}, cfg.ChildHealthChecks...)
	}

	if cfg.AlarmIdentifier != nil {
		alarm := *cfg.AlarmIdentifier
		hc.AlarmIdentifier = &alarm
	}

	if cfg.InsufficientDataHealthStatus != "" {
		hc.InsufficientDataHealthStatus = cfg.InsufficientDataHealthStatus
	}
}

// validateHealthCheck applies the per-type rules from the Route 53
// HealthCheckConfig reference. CALCULATED, CLOUDWATCH_METRIC and
// RECOVERY_CONTROL checks watch other resources, so they take no endpoint or
// port. Every other type needs an endpoint.
func validateHealthCheck(hc *driver.HealthCheckInfo) error {
	switch hc.Protocol {
	case typeCalculated:
		return validateCalculated(hc)
	case typeCloudWatchMetric:
		return validateCloudWatchMetric(hc)
	case typeRecoveryControl:
		return validateNoEndpoint(hc)
	case "", "HTTP", "HTTPS", typeHTTPStrMatch, typeHTTPSStrMatch, healthCheckTCP:
		return validateEndpointCheck(hc)
	default:
		return errors.Newf(errors.InvalidArgument, "invalid health check type %q", hc.Protocol)
	}
}

func validateNoEndpoint(hc *driver.HealthCheckInfo) error {
	if hc.Endpoint != "" || hc.Port != 0 {
		return errors.Newf(errors.InvalidArgument,
			"IPAddress, FullyQualifiedDomainName and Port are not allowed for %s health checks", hc.Protocol)
	}

	return nil
}

func validateCalculated(hc *driver.HealthCheckInfo) error {
	if err := validateNoEndpoint(hc); err != nil {
		return err
	}

	if hc.HealthThreshold < 0 || hc.HealthThreshold > maxChildHealthChecks {
		return errors.Newf(errors.InvalidArgument, "HealthThreshold must be between 0 and %d", maxChildHealthChecks)
	}

	if len(hc.ChildHealthChecks) > maxChildHealthChecks {
		return errors.Newf(errors.InvalidArgument, "ChildHealthChecks can have at most %d members", maxChildHealthChecks)
	}

	return nil
}

func validateCloudWatchMetric(hc *driver.HealthCheckInfo) error {
	if err := validateNoEndpoint(hc); err != nil {
		return err
	}

	if hc.AlarmIdentifier == nil || hc.AlarmIdentifier.Name == "" || hc.AlarmIdentifier.Region == "" {
		return errors.New(errors.InvalidArgument, "AlarmIdentifier with Region and Name is required for CLOUDWATCH_METRIC health checks")
	}

	switch hc.InsufficientDataHealthStatus {
	case "", "Healthy", "Unhealthy", "LastKnownStatus":
		return nil
	default:
		return errors.Newf(errors.InvalidArgument, "invalid InsufficientDataHealthStatus %q", hc.InsufficientDataHealthStatus)
	}
}

func validateEndpointCheck(hc *driver.HealthCheckInfo) error {
	if hc.Endpoint == "" {
		return errors.New(errors.InvalidArgument, "endpoint is required")
	}

	if hc.Protocol == healthCheckTCP && hc.Port == 0 {
		return errors.New(errors.InvalidArgument, "Port is required for TCP health checks")
	}

	if len(hc.SearchString) > maxSearchStringLen {
		return errors.Newf(errors.InvalidArgument, "SearchString must be at most %d characters", maxSearchStringLen)
	}

	isStrMatch := hc.Protocol == typeHTTPStrMatch || hc.Protocol == typeHTTPSStrMatch
	if isStrMatch && hc.SearchString == "" {
		return errors.Newf(errors.InvalidArgument, "SearchString is required for %s health checks", hc.Protocol)
	}

	return nil
}

// DeleteHealthCheck deletes a Route 53 health check by ID.
func (m *Mock) DeleteHealthCheck(_ context.Context, id string) error {
	if !m.healthChecks.Delete(id) {
		return errors.Newf(errors.NotFound, "health check %q not found", id)
	}

	return nil
}

// GetHealthCheck retrieves a Route 53 health check by ID.
func (m *Mock) GetHealthCheck(_ context.Context, id string) (*driver.HealthCheckInfo, error) {
	hc, ok := m.healthChecks.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "health check %q not found", id)
	}

	result := hc

	return &result, nil
}

// ListHealthChecks returns all Route 53 health checks.
func (m *Mock) ListHealthChecks(_ context.Context) ([]driver.HealthCheckInfo, error) {
	all := m.healthChecks.SortedValues()

	checks := make([]driver.HealthCheckInfo, 0, len(all))
	for _, hc := range all {
		checks = append(checks, hc)
	}

	return checks, nil
}

// UpdateHealthCheck updates an existing Route 53 health check.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) UpdateHealthCheck(_ context.Context, id string, cfg driver.HealthCheckConfig) (*driver.HealthCheckInfo, error) {
	hc, ok := m.healthChecks.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "health check %q not found", id)
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

	applyRoute53Fields(&hc, &cfg)

	if err := validateHealthCheck(&hc); err != nil {
		return nil, err
	}

	m.healthChecks.Set(id, hc)

	result := hc

	return &result, nil
}

// SetHealthCheckStatus sets the status of a Route 53 health check.
func (m *Mock) SetHealthCheckStatus(_ context.Context, id, status string) error {
	if status != statusHealthy && status != statusUnhealthy {
		return errors.Newf(errors.InvalidArgument, "status must be %q or %q", statusHealthy, statusUnhealthy)
	}

	if !m.healthChecks.Has(id) {
		return errors.Newf(errors.NotFound, "health check %q not found", id)
	}

	m.healthChecks.Update(id, func(hc driver.HealthCheckInfo) driver.HealthCheckInfo {
		hc.Status = status
		return hc
	})

	return nil
}
