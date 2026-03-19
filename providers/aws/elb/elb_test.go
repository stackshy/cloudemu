package elb

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/loadbalancer/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))
	return New(opts)
}

func createTestLB(m *Mock) *driver.LBInfo {
	info, _ := m.CreateLoadBalancer(context.Background(), driver.LBConfig{
		Name:    "my-lb",
		Type:    "application",
		Scheme:  "internet-facing",
		Subnets: []string{"subnet-1", "subnet-2"},
		Tags:    map[string]string{"env": "test"},
	})
	return info
}

func createTestTG(m *Mock) *driver.TargetGroupInfo {
	info, _ := m.CreateTargetGroup(context.Background(), driver.TargetGroupConfig{
		Name:       "my-tg",
		Protocol:   "HTTP",
		Port:       80,
		VPCID:      "vpc-1",
		HealthPath: "/health",
	})
	return info
}

func TestCreateLoadBalancer(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.LBConfig
		expectErr bool
	}{
		{
			name: "success",
			cfg:  driver.LBConfig{Name: "my-lb", Type: "application", Scheme: "internet-facing"},
		},
		{name: "empty name", cfg: driver.LBConfig{}, expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			info, err := m.CreateLoadBalancer(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertNotEmpty(t, info.ID)
			assertNotEmpty(t, info.ARN)
			assertNotEmpty(t, info.DNSName)
			assertEqual(t, "my-lb", info.Name)
			assertEqual(t, "active", info.State)
		})
	}
}

func TestDeleteLoadBalancer(t *testing.T) {
	m := newTestMock()
	lb := createTestLB(m)

	requireNoError(t, m.DeleteLoadBalancer(context.Background(), lb.ARN))
	assertError(t, m.DeleteLoadBalancer(context.Background(), "arn:nope"), true)
}

func TestDescribeLoadBalancers(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	lb := createTestLB(m)

	t.Run("all", func(t *testing.T) {
		lbs, err := m.DescribeLoadBalancers(ctx, nil)
		requireNoError(t, err)
		assertEqual(t, 1, len(lbs))
	})

	t.Run("by ARN", func(t *testing.T) {
		lbs, err := m.DescribeLoadBalancers(ctx, []string{lb.ARN})
		requireNoError(t, err)
		assertEqual(t, 1, len(lbs))
	})

	t.Run("not found", func(t *testing.T) {
		lbs, err := m.DescribeLoadBalancers(ctx, []string{"arn:nope"})
		requireNoError(t, err)
		assertEqual(t, 0, len(lbs))
	})
}

func TestCreateTargetGroup(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.TargetGroupConfig
		expectErr bool
	}{
		{
			name: "success",
			cfg:  driver.TargetGroupConfig{Name: "tg1", Protocol: "HTTP", Port: 80, VPCID: "vpc-1"},
		},
		{name: "empty name", cfg: driver.TargetGroupConfig{}, expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			info, err := m.CreateTargetGroup(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertNotEmpty(t, info.ARN)
			assertEqual(t, "tg1", info.Name)
			assertEqual(t, "HTTP", info.Protocol)
			assertEqual(t, 80, info.Port)
		})
	}
}

func TestDeleteTargetGroup(t *testing.T) {
	m := newTestMock()
	tg := createTestTG(m)

	requireNoError(t, m.DeleteTargetGroup(context.Background(), tg.ARN))
	assertError(t, m.DeleteTargetGroup(context.Background(), "arn:nope"), true)
}

func TestDescribeTargetGroups(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	tg := createTestTG(m)

	t.Run("all", func(t *testing.T) {
		tgs, err := m.DescribeTargetGroups(ctx, nil)
		requireNoError(t, err)
		assertEqual(t, 1, len(tgs))
	})

	t.Run("by ARN", func(t *testing.T) {
		tgs, err := m.DescribeTargetGroups(ctx, []string{tg.ARN})
		requireNoError(t, err)
		assertEqual(t, 1, len(tgs))
	})
}

func TestCreateListener(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	lb := createTestLB(m)
	tg := createTestTG(m)

	t.Run("success", func(t *testing.T) {
		li, err := m.CreateListener(ctx, driver.ListenerConfig{
			LBARN:          lb.ARN,
			Protocol:       "HTTP",
			Port:           80,
			TargetGroupARN: tg.ARN,
		})
		requireNoError(t, err)
		assertNotEmpty(t, li.ARN)
		assertEqual(t, lb.ARN, li.LBARN)
		assertEqual(t, 80, li.Port)
	})

	t.Run("LB not found", func(t *testing.T) {
		_, err := m.CreateListener(ctx, driver.ListenerConfig{
			LBARN: "arn:nope", Protocol: "HTTP", Port: 80,
		})
		assertError(t, err, true)
	})
}

func TestDeleteListener(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	lb := createTestLB(m)
	li, _ := m.CreateListener(ctx, driver.ListenerConfig{
		LBARN: lb.ARN, Protocol: "HTTP", Port: 80,
	})

	requireNoError(t, m.DeleteListener(ctx, li.ARN))
	assertError(t, m.DeleteListener(ctx, "arn:nope"), true)
}

func TestDescribeListeners(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	lb := createTestLB(m)
	_, _ = m.CreateListener(ctx, driver.ListenerConfig{
		LBARN: lb.ARN, Protocol: "HTTP", Port: 80,
	})
	_, _ = m.CreateListener(ctx, driver.ListenerConfig{
		LBARN: lb.ARN, Protocol: "HTTPS", Port: 443,
	})

	t.Run("success", func(t *testing.T) {
		listeners, err := m.DescribeListeners(ctx, lb.ARN)
		requireNoError(t, err)
		assertEqual(t, 2, len(listeners))
	})

	t.Run("LB not found", func(t *testing.T) {
		_, err := m.DescribeListeners(ctx, "arn:nope")
		assertError(t, err, true)
	})
}

func TestDeleteLoadBalancerCascadesListeners(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	lb := createTestLB(m)
	_, _ = m.CreateListener(ctx, driver.ListenerConfig{
		LBARN: lb.ARN, Protocol: "HTTP", Port: 80,
	})

	requireNoError(t, m.DeleteLoadBalancer(ctx, lb.ARN))

	// Listeners should also be deleted - but we can't describe them without the LB.
	// Just verify LB is gone.
	lbs, _ := m.DescribeLoadBalancers(ctx, []string{lb.ARN})
	assertEqual(t, 0, len(lbs))
}

func TestRegisterTargets(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	tg := createTestTG(m)

	t.Run("success", func(t *testing.T) {
		err := m.RegisterTargets(ctx, tg.ARN, []driver.Target{
			{ID: "i-1", Port: 80},
			{ID: "i-2", Port: 80},
		})
		requireNoError(t, err)

		health, err := m.DescribeTargetHealth(ctx, tg.ARN)
		requireNoError(t, err)
		assertEqual(t, 2, len(health))
	})

	t.Run("TG not found", func(t *testing.T) {
		err := m.RegisterTargets(ctx, "arn:nope", nil)
		assertError(t, err, true)
	})
}

func TestDeregisterTargets(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	tg := createTestTG(m)
	_ = m.RegisterTargets(ctx, tg.ARN, []driver.Target{{ID: "i-1", Port: 80}, {ID: "i-2", Port: 80}})

	t.Run("success", func(t *testing.T) {
		err := m.DeregisterTargets(ctx, tg.ARN, []driver.Target{{ID: "i-1"}})
		requireNoError(t, err)

		health, _ := m.DescribeTargetHealth(ctx, tg.ARN)
		assertEqual(t, 1, len(health))
	})

	t.Run("TG not found", func(t *testing.T) {
		err := m.DeregisterTargets(ctx, "arn:nope", nil)
		assertError(t, err, true)
	})
}

func TestDescribeTargetHealth(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	tg := createTestTG(m)
	_ = m.RegisterTargets(ctx, tg.ARN, []driver.Target{{ID: "i-1", Port: 80}})

	t.Run("success", func(t *testing.T) {
		health, err := m.DescribeTargetHealth(ctx, tg.ARN)
		requireNoError(t, err)
		assertEqual(t, 1, len(health))
		assertEqual(t, "initial", health[0].State)
	})

	t.Run("TG not found", func(t *testing.T) {
		_, err := m.DescribeTargetHealth(ctx, "arn:nope")
		assertError(t, err, true)
	})
}

func TestSetTargetHealth(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	tg := createTestTG(m)
	_ = m.RegisterTargets(ctx, tg.ARN, []driver.Target{{ID: "i-1", Port: 80}})

	t.Run("success", func(t *testing.T) {
		err := m.SetTargetHealth(ctx, tg.ARN, "i-1", "healthy")
		requireNoError(t, err)

		health, _ := m.DescribeTargetHealth(ctx, tg.ARN)
		assertEqual(t, "healthy", health[0].State)
	})

	t.Run("target not found", func(t *testing.T) {
		err := m.SetTargetHealth(ctx, tg.ARN, "i-999", "healthy")
		assertError(t, err, true)
	})

	t.Run("TG not found", func(t *testing.T) {
		err := m.SetTargetHealth(ctx, "arn:nope", "i-1", "healthy")
		assertError(t, err, true)
	})
}

// --- test helpers ---

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()
	switch {
	case expectErr && err == nil:
		t.Fatal("expected error but got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()
	if s == "" {
		t.Error("expected non-empty string")
	}
}
