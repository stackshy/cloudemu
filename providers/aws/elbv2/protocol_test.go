package elbv2

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// assertInvalidArgument checks err is InvalidArgument, or nil when wantErr is
// false.
func assertInvalidArgument(t *testing.T, err error, wantErr bool) {
	t.Helper()
	assertError(t, err, wantErr)

	if wantErr && !errors.IsInvalidArgument(err) {
		t.Fatalf("got %v, want InvalidArgument", err)
	}
}

func TestCreateTargetGroupProtocol(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.TargetGroupConfig
		expectErr bool
	}{
		{name: "bogus protocol", cfg: driver.TargetGroupConfig{Name: "t1", Protocol: "BOGUS", Port: 80}, expectErr: true},
		{name: "lambda with protocol", cfg: driver.TargetGroupConfig{Name: "t2", Protocol: "HTTP", TargetType: "lambda"},
			expectErr: true},
		{name: "lambda without protocol", cfg: driver.TargetGroupConfig{Name: "t3", TargetType: "lambda"}},
		{name: "quic", cfg: driver.TargetGroupConfig{Name: "t4", Protocol: "QUIC", Port: 443}},
		{name: "tcp_quic", cfg: driver.TargetGroupConfig{Name: "t5", Protocol: "TCP_QUIC", Port: 443}},
		{name: "geneve", cfg: driver.TargetGroupConfig{Name: "t6", Protocol: "GENEVE", Port: 6081}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			_, err := m.CreateTargetGroup(context.Background(), tc.cfg)
			assertInvalidArgument(t, err, tc.expectErr)
		})
	}
}

func TestCreateListenerProtocolByLBType(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	alb := createTestLB(m)

	nlb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "nlb", Type: "network"})
	requireNoError(t, err)

	gwlb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "gwlb", Type: "gateway"})
	requireNoError(t, err)

	untyped, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "untyped"})
	requireNoError(t, err)

	twoCerts := append(append([]driver.Certificate{}, testCerts...), testCerts...)

	tests := []struct {
		name      string
		cfg       driver.ListenerConfig
		expectErr bool
	}{
		{name: "HTTP on NLB", cfg: driver.ListenerConfig{LBARN: nlb.ARN, Protocol: "HTTP", Port: 80}, expectErr: true},
		{name: "TCP on ALB", cfg: driver.ListenerConfig{LBARN: alb.ARN, Protocol: "TCP", Port: 80}, expectErr: true},
		{name: "TCP on untyped LB", cfg: driver.ListenerConfig{LBARN: untyped.ARN, Protocol: "TCP", Port: 80}, expectErr: true},
		{name: "bogus protocol", cfg: driver.ListenerConfig{LBARN: alb.ARN, Protocol: "BOGUS", Port: 80}, expectErr: true},
		{name: "protocol on GWLB", cfg: driver.ListenerConfig{LBARN: gwlb.ARN, Protocol: "GENEVE"}, expectErr: true},
		{name: "HTTPS without cert", cfg: driver.ListenerConfig{LBARN: alb.ARN, Protocol: "HTTPS", Port: 443}, expectErr: true},
		{name: "TLS without cert", cfg: driver.ListenerConfig{LBARN: nlb.ARN, Protocol: "TLS", Port: 443}, expectErr: true},
		{name: "two default certs", cfg: driver.ListenerConfig{
			LBARN: alb.ARN, Protocol: "HTTPS", Port: 443, Certificates: twoCerts,
		}, expectErr: true},
		{name: "HTTPS with cert", cfg: driver.ListenerConfig{LBARN: alb.ARN, Protocol: "HTTPS", Port: 443, Certificates: testCerts}},
		{name: "QUIC on NLB", cfg: driver.ListenerConfig{LBARN: nlb.ARN, Protocol: "QUIC", Port: 443}},
		{name: "no protocol on GWLB", cfg: driver.ListenerConfig{LBARN: gwlb.ARN}},
		{name: "HTTP on untyped LB", cfg: driver.ListenerConfig{LBARN: untyped.ARN, Protocol: "HTTP", Port: 8080}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.CreateListener(ctx, tc.cfg)
			assertInvalidArgument(t, err, tc.expectErr)
		})
	}
}

func TestModifyListenerChecksMergedState(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	lb := createTestLB(m)

	li, err := m.CreateListener(ctx, driver.ListenerConfig{LBARN: lb.ARN, Protocol: "HTTP", Port: 80})
	requireNoError(t, err)

	err = m.ModifyListener(ctx, driver.ModifyListenerInput{ListenerARN: li.ARN, Protocol: "HTTPS"})
	assertInvalidArgument(t, err, true)

	err = m.ModifyListener(ctx, driver.ModifyListenerInput{ListenerARN: li.ARN, Protocol: "TCP"})
	assertInvalidArgument(t, err, true)

	got, err := m.DescribeListeners(ctx, lb.ARN)
	requireNoError(t, err)
	assertEqual(t, "HTTP", got[0].Protocol)

	requireNoError(t, m.ModifyListener(ctx, driver.ModifyListenerInput{
		ListenerARN: li.ARN, Protocol: "HTTPS", Certificates: testCerts,
	}))
}
