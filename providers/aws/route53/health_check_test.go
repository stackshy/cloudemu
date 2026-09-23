package route53

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/dns/driver"
)

// TestCreateHealthCheckEndpointByType checks the endpoint rule depends on the
// health check type, as in real Route 53.
func TestCreateHealthCheckEndpointByType(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.HealthCheckConfig
		expectErr bool
	}{
		{name: "calculated needs no endpoint", cfg: driver.HealthCheckConfig{Protocol: "CALCULATED"}},
		{name: "cloudwatch metric needs no endpoint", cfg: driver.HealthCheckConfig{Protocol: "CLOUDWATCH_METRIC"}},
		{name: "calculated rejects an endpoint", cfg: driver.HealthCheckConfig{Protocol: "CALCULATED", Endpoint: "10.0.0.1"},
			expectErr: true},
		{name: "http needs an endpoint", cfg: driver.HealthCheckConfig{Protocol: "HTTP"}, expectErr: true},
		{name: "tcp needs a port", cfg: driver.HealthCheckConfig{Protocol: "TCP", Endpoint: "10.0.0.1"}, expectErr: true},
		{name: "tcp with port", cfg: driver.HealthCheckConfig{Protocol: "TCP", Endpoint: "10.0.0.1", Port: 22}},
		{name: "bogus type", cfg: driver.HealthCheckConfig{Protocol: "BOGUS", Endpoint: "10.0.0.1"}, expectErr: true},
		{name: "http with endpoint", cfg: driver.HealthCheckConfig{Protocol: "HTTP", Endpoint: "example.com"}},
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
