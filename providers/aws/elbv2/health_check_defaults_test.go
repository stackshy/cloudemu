package elbv2

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// TestDefaultHealthCheckProtocolHTTPS proves an HTTPS target group's health
// check defaults to HTTP, not HTTPS. Per the CreateTargetGroup API reference,
// ELBv2 never mirrors the target group's own protocol back onto the health
// check: an Application Load Balancer target group (HTTP or HTTPS) always
// defaults its health check to HTTP. Mirroring the protocol instead (the prior
// behavior) produced a health check protocol real AWS never returns —
// surfacing as a perpetual Terraform plan diff on any aws_lb_target_group with
// protocol = "HTTPS" and no explicit health_check block.
func TestDefaultHealthCheckProtocolHTTPS(t *testing.T) {
	m := newTestMock()

	tg, err := m.CreateTargetGroup(context.Background(), driver.TargetGroupConfig{
		Name: "https-tg", Protocol: "HTTPS", Port: 443, TargetType: "instance",
	})
	requireNoError(t, err)

	assertEqual(t, "HTTP", tg.HealthCheck.Protocol)
}

// TestDefaultHealthCheckProtocolHTTP proves an HTTP target group's health
// check protocol defaults to HTTP, same as before this fix.
func TestDefaultHealthCheckProtocolHTTP(t *testing.T) {
	m := newTestMock()

	tg, err := m.CreateTargetGroup(context.Background(), driver.TargetGroupConfig{
		Name: "http-tg", Protocol: "HTTP", Port: 80, TargetType: "instance",
	})
	requireNoError(t, err)

	assertEqual(t, "HTTP", tg.HealthCheck.Protocol)
}

// TestDefaultHealthCheckProtocolTCPFamily proves the TCP-family protocols
// (TCP, TLS, UDP, TCP_UDP) all default their health check to TCP, matching
// the Network/Gateway Load Balancer default in the API reference.
func TestDefaultHealthCheckProtocolTCPFamily(t *testing.T) {
	for _, proto := range []string{"TCP", "TLS", "UDP", "TCP_UDP"} {
		m := newTestMock()

		tg, err := m.CreateTargetGroup(context.Background(), driver.TargetGroupConfig{
			Name: "tcp-tg-" + proto, Protocol: proto, Port: 80, TargetType: "instance",
		})
		requireNoError(t, err)

		if tg.HealthCheck.Protocol != "TCP" {
			t.Errorf("protocol %s: health check protocol = %q, want TCP", proto, tg.HealthCheck.Protocol)
		}
	}
}

// TestDefaultHealthCheckProtocolExplicitOverride proves an explicitly supplied
// HealthCheck.Protocol is still honored rather than overridden by the default.
func TestDefaultHealthCheckProtocolExplicitOverride(t *testing.T) {
	m := newTestMock()

	tg, err := m.CreateTargetGroup(context.Background(), driver.TargetGroupConfig{
		Name: "explicit-tg", Protocol: "HTTPS", Port: 443, TargetType: "instance",
		HealthCheck: driver.HealthCheck{Protocol: "HTTPS"},
	})
	requireNoError(t, err)

	assertEqual(t, "HTTPS", tg.HealthCheck.Protocol)
}

// TestDefaultHealthCheckTimeoutByProtocol proves HealthCheckTimeoutSeconds
// defaults per the CreateTargetGroup API reference: HTTP is 6, HTTPS/TCP/TLS
// (and the other TCP-family protocols) are 10, and GENEVE is 5. The prior flat
// 5 under-reported the timeout real AWS returns for every non-GENEVE protocol.
func TestDefaultHealthCheckTimeoutByProtocol(t *testing.T) {
	cases := []struct {
		protocol string
		want     int
	}{
		{"HTTP", 6},
		{"HTTPS", 10},
		{"TCP", 10},
		{"TLS", 10},
		{"UDP", 10},
		{"TCP_UDP", 10},
	}

	for _, c := range cases {
		m := newTestMock()

		tg, err := m.CreateTargetGroup(context.Background(), driver.TargetGroupConfig{
			Name: "to-tg-" + c.protocol, Protocol: c.protocol, Port: 80, TargetType: "instance",
		})
		requireNoError(t, err)

		if tg.HealthCheck.TimeoutSeconds != c.want {
			t.Errorf("protocol %s: timeout = %d, want %d", c.protocol, tg.HealthCheck.TimeoutSeconds, c.want)
		}
	}
}

// TestDefaultHealthCheckTimeoutExplicit proves an explicitly supplied timeout is
// preserved rather than overwritten by the protocol default.
func TestDefaultHealthCheckTimeoutExplicit(t *testing.T) {
	m := newTestMock()

	tg, err := m.CreateTargetGroup(context.Background(), driver.TargetGroupConfig{
		Name: "to-explicit", Protocol: "TCP", Port: 80, TargetType: "instance",
		HealthCheck: driver.HealthCheck{TimeoutSeconds: 8},
	})
	requireNoError(t, err)

	assertEqual(t, 8, tg.HealthCheck.TimeoutSeconds)
}

// TestDefaultHealthCheckLambda proves a lambda target group defaults with no
// health-check protocol, port, or path (health checks are disabled by default),
// and with the lambda-specific numeric defaults: interval 35, timeout 30, and
// both threshold counts 5. Returning a protocol here made Terraform reject the
// group with "health_check.protocol cannot be specified when target_type is
// lambda".
func TestDefaultHealthCheckLambda(t *testing.T) {
	m := newTestMock()

	tg, err := m.CreateTargetGroup(context.Background(), driver.TargetGroupConfig{
		Name: "lambda-tg", TargetType: "lambda",
	})
	requireNoError(t, err)

	assertEqual(t, "", tg.HealthCheck.Protocol)
	assertEqual(t, "", tg.HealthCheck.Port)
	assertEqual(t, "", tg.HealthCheck.Path)
	assertEqual(t, 35, tg.HealthCheck.IntervalSeconds)
	assertEqual(t, 30, tg.HealthCheck.TimeoutSeconds)
	assertEqual(t, 5, tg.HealthCheck.HealthyThreshold)
	assertEqual(t, 5, tg.HealthCheck.UnhealthyThreshold)
}
