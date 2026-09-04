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
