package route53

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/dns/driver"
)

// TestCreateHealthCheckRulesByType checks the per-type rules from the Route 53
// HealthCheckConfig reference.
func TestCreateHealthCheckRulesByType(t *testing.T) {
	alarm := &driver.HealthCheckAlarm{Region: "us-east-1", Name: "cpu-high"}
	threshold, tooHigh := 1, 257

	tests := []struct {
		name      string
		cfg       driver.HealthCheckConfig
		expectErr bool
	}{
		{name: "calculated needs no endpoint", cfg: driver.HealthCheckConfig{Protocol: "CALCULATED"}},
		{name: "calculated rejects an endpoint", cfg: driver.HealthCheckConfig{Protocol: "CALCULATED", Endpoint: "10.0.0.1"},
			expectErr: true},
		{name: "calculated threshold over 256", cfg: driver.HealthCheckConfig{Protocol: "CALCULATED", HealthThreshold: &tooHigh},
			expectErr: true},
		{name: "calculated with children", cfg: driver.HealthCheckConfig{
			Protocol: "CALCULATED", HealthThreshold: &threshold, ChildHealthChecks: []string{"hc-1"},
		}},
		{name: "cloudwatch metric with alarm", cfg: driver.HealthCheckConfig{Protocol: "CLOUDWATCH_METRIC", AlarmIdentifier: alarm}},
		{name: "cloudwatch metric without alarm", cfg: driver.HealthCheckConfig{Protocol: "CLOUDWATCH_METRIC"}, expectErr: true},
		{name: "cloudwatch metric bad insufficient data status", cfg: driver.HealthCheckConfig{
			Protocol: "CLOUDWATCH_METRIC", AlarmIdentifier: alarm, InsufficientDataHealthStatus: "Maybe",
		}, expectErr: true},
		{name: "http needs an endpoint", cfg: driver.HealthCheckConfig{Protocol: "HTTP"}, expectErr: true},
		{name: "tcp needs a port", cfg: driver.HealthCheckConfig{Protocol: "TCP", Endpoint: "10.0.0.1"}, expectErr: true},
		{name: "tcp with port", cfg: driver.HealthCheckConfig{Protocol: "TCP", Endpoint: "10.0.0.1", Port: 22}},
		{name: "bogus type", cfg: driver.HealthCheckConfig{Protocol: "BOGUS", Endpoint: "10.0.0.1"}, expectErr: true},
		{name: "http with endpoint", cfg: driver.HealthCheckConfig{Protocol: "HTTP", Endpoint: "example.com"}},
		{name: "str match needs search string", cfg: driver.HealthCheckConfig{Protocol: "HTTP_STR_MATCH", Endpoint: "example.com"},
			expectErr: true},
		{name: "https str match needs search string", cfg: driver.HealthCheckConfig{
			Protocol: "HTTPS_STR_MATCH", Endpoint: "example.com",
		}, expectErr: true},
		{name: "search string over 255", cfg: driver.HealthCheckConfig{
			Protocol: "HTTP_STR_MATCH", Endpoint: "example.com", SearchString: strings.Repeat("a", 256),
		}, expectErr: true},
		{name: "search string of 255", cfg: driver.HealthCheckConfig{
			Protocol: "HTTP_STR_MATCH", Endpoint: "example.com", SearchString: strings.Repeat("a", 255),
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			_, err := m.CreateHealthCheck(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr && !errors.IsInvalidArgument(err) {
				t.Fatalf("got %v, want InvalidArgument", err)
			}
		})
	}
}

// TestHealthCheckRoute53FieldsRoundTrip checks the Route 53 only fields are
// stored on create, merged on update, and checked again after the merge.
func TestHealthCheckRoute53FieldsRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	inverted, two, one := true, 2, 1

	hc, err := m.CreateHealthCheck(ctx, driver.HealthCheckConfig{
		Protocol: "CALCULATED", HealthThreshold: &two, ChildHealthChecks: []string{"hc-a", "hc-b"}, Inverted: &inverted,
	})
	requireNoError(t, err)
	assertEqual(t, 2, hc.HealthThreshold)
	assertEqual(t, 2, len(hc.ChildHealthChecks))
	assertEqual(t, true, hc.Inverted)

	got, err := m.UpdateHealthCheck(ctx, hc.ID, driver.HealthCheckConfig{HealthThreshold: &one, ChildHealthChecks: []string{"hc-a"}})
	requireNoError(t, err)
	assertEqual(t, 1, got.HealthThreshold)
	assertEqual(t, 1, len(got.ChildHealthChecks))
	assertEqual(t, true, got.Inverted)

	str, err := m.CreateHealthCheck(ctx, driver.HealthCheckConfig{
		Protocol: "HTTP_STR_MATCH", Endpoint: "example.com", SearchString: "ok",
	})
	requireNoError(t, err)
	assertEqual(t, "ok", str.SearchString)

	_, err = m.UpdateHealthCheck(ctx, str.ID, driver.HealthCheckConfig{SearchString: strings.Repeat("a", 256)})
	assertError(t, err, true)

	after, err := m.GetHealthCheck(ctx, str.ID)
	requireNoError(t, err)
	assertEqual(t, "ok", after.SearchString)
}
