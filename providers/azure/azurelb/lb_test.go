package azurelb

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/loadbalancer/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() *Mock {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithAccountID("test-sub"), config.WithRegion("eastus"))

	return New(opts)
}

func createTestLB(t *testing.T, m *Mock) string {
	t.Helper()

	ctx := context.Background()
	lb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{
		Name: "test-lb", Type: "application", Scheme: "internet-facing",
		Subnets: []string{"subnet-1"}, Tags: map[string]string{"env": "test"},
	})
	require.NoError(t, err)

	return lb.ARN
}

func createTestTargetGroup(t *testing.T, m *Mock) string {
	t.Helper()

	ctx := context.Background()
	tg, err := m.CreateTargetGroup(ctx, driver.TargetGroupConfig{
		Name: "test-tg", Protocol: "HTTP", Port: 80, VPCID: "vnet-1", HealthPath: "/health",
	})
	require.NoError(t, err)

	return tg.ARN
}

func TestCreateLoadBalancer(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name    string
		cfg     driver.LBConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "success",
			cfg:  driver.LBConfig{Name: "my-lb", Type: "application", Scheme: "internal", Subnets: []string{"s1"}},
		},
		{name: "empty name", cfg: driver.LBConfig{}, wantErr: true, errMsg: "load balancer name is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateLoadBalancer(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, info.ID)
				assert.NotEmpty(t, info.ARN)
				assert.Equal(t, "my-lb", info.Name)
				assert.Equal(t, "active", info.State)
				assert.Contains(t, info.DNSName, "my-lb")
				assert.Contains(t, info.DNSName, "eastus")
			}
		})
	}
}

func TestDeleteLoadBalancer(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	lbARN := createTestLB(t, m)

	tests := []struct {
		name    string
		arn     string
		wantErr bool
		errMsg  string
	}{
		{name: "success", arn: lbARN},
		{name: "not found", arn: "missing-arn", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteLoadBalancer(ctx, tt.arn)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDescribeLoadBalancers(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	arn1 := createTestLB(t, m)

	lb2, _ := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "lb2", Type: "network"})

	tests := []struct {
		name      string
		arns      []string
		wantCount int
	}{
		{name: "all", arns: nil, wantCount: 2},
		{name: "by ARN", arns: []string{arn1}, wantCount: 1},
		{name: "multiple ARNs", arns: []string{arn1, lb2.ARN}, wantCount: 2},
		{name: "nonexistent", arns: []string{"missing"}, wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lbs, err := m.DescribeLoadBalancers(ctx, tt.arns)
			require.NoError(t, err)
			assert.Len(t, lbs, tt.wantCount)
		})
	}
}

func TestCreateTargetGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name    string
		cfg     driver.TargetGroupConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "success",
			cfg:  driver.TargetGroupConfig{Name: "tg1", Protocol: "HTTP", Port: 80, VPCID: "vnet-1", HealthPath: "/health"},
		},
		{name: "empty name", cfg: driver.TargetGroupConfig{}, wantErr: true, errMsg: "target group name is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateTargetGroup(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, info.ARN)
				assert.Equal(t, "tg1", info.Name)
				assert.Equal(t, "HTTP", info.Protocol)
				assert.Equal(t, 80, info.Port)
			}
		})
	}
}

func TestDeleteTargetGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	tgARN := createTestTargetGroup(t, m)

	tests := []struct {
		name    string
		arn     string
		wantErr bool
		errMsg  string
	}{
		{name: "success", arn: tgARN},
		{name: "not found", arn: "missing", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteTargetGroup(ctx, tt.arn)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDescribeTargetGroups(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	arn1 := createTestTargetGroup(t, m)

	tg2, _ := m.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "tg2", Protocol: "TCP", Port: 443})

	tests := []struct {
		name      string
		arns      []string
		wantCount int
	}{
		{name: "all", arns: nil, wantCount: 2},
		{name: "by ARN", arns: []string{arn1}, wantCount: 1},
		{name: "multiple ARNs", arns: []string{arn1, tg2.ARN}, wantCount: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tgs, err := m.DescribeTargetGroups(ctx, tt.arns)
			require.NoError(t, err)
			assert.Len(t, tgs, tt.wantCount)
		})
	}
}

func TestCreateListener(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	lbARN := createTestLB(t, m)
	tgARN := createTestTargetGroup(t, m)

	tests := []struct {
		name    string
		cfg     driver.ListenerConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "success",
			cfg:  driver.ListenerConfig{LBARN: lbARN, Protocol: "HTTP", Port: 80, TargetGroupARN: tgARN},
		},
		{
			name:    "LB not found",
			cfg:     driver.ListenerConfig{LBARN: "missing", Protocol: "HTTP", Port: 80},
			wantErr: true, errMsg: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateListener(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, info.ARN)
				assert.Equal(t, lbARN, info.LBARN)
				assert.Equal(t, "HTTP", info.Protocol)
				assert.Equal(t, 80, info.Port)
			}
		})
	}
}

func TestDeleteListener(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	lbARN := createTestLB(t, m)

	li, err := m.CreateListener(ctx, driver.ListenerConfig{LBARN: lbARN, Protocol: "HTTP", Port: 80})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := m.DeleteListener(ctx, li.ARN)
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteListener(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDescribeListeners(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	lbARN := createTestLB(t, m)

	_, _ = m.CreateListener(ctx, driver.ListenerConfig{LBARN: lbARN, Protocol: "HTTP", Port: 80})
	_, _ = m.CreateListener(ctx, driver.ListenerConfig{LBARN: lbARN, Protocol: "HTTPS", Port: 443})

	t.Run("success", func(t *testing.T) {
		listeners, err := m.DescribeListeners(ctx, lbARN)
		require.NoError(t, err)
		assert.Len(t, listeners, 2)
	})

	t.Run("LB not found", func(t *testing.T) {
		_, err := m.DescribeListeners(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestRegisterAndDeregisterTargets(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	tgARN := createTestTargetGroup(t, m)

	targets := []driver.Target{
		{ID: "vm-1", Port: 80},
		{ID: "vm-2", Port: 80},
	}

	t.Run("register targets", func(t *testing.T) {
		err := m.RegisterTargets(ctx, tgARN, targets)
		require.NoError(t, err)

		health, err := m.DescribeTargetHealth(ctx, tgARN)
		require.NoError(t, err)
		assert.Len(t, health, 2)

		for _, h := range health {
			assert.Equal(t, "initial", h.State)
		}
	})

	t.Run("deregister one target", func(t *testing.T) {
		err := m.DeregisterTargets(ctx, tgARN, []driver.Target{{ID: "vm-1", Port: 80}})
		require.NoError(t, err)

		health, err := m.DescribeTargetHealth(ctx, tgARN)
		require.NoError(t, err)
		assert.Len(t, health, 1)
	})

	t.Run("register to nonexistent TG", func(t *testing.T) {
		err := m.RegisterTargets(ctx, "missing", targets)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("deregister from nonexistent TG", func(t *testing.T) {
		err := m.DeregisterTargets(ctx, "missing", targets)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestSetTargetHealth(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	tgARN := createTestTargetGroup(t, m)

	require.NoError(t, m.RegisterTargets(ctx, tgARN, []driver.Target{{ID: "vm-1", Port: 80}}))

	tests := []struct {
		name     string
		tgARN    string
		targetID string
		state    string
		wantErr  bool
		errMsg   string
	}{
		{name: "set healthy", tgARN: tgARN, targetID: "vm-1", state: "healthy"},
		{name: "set unhealthy", tgARN: tgARN, targetID: "vm-1", state: "unhealthy"},
		{name: "TG not found", tgARN: "missing", targetID: "vm-1", state: "healthy", wantErr: true, errMsg: "not found"},
		{name: "target not found", tgARN: tgARN, targetID: "vm-99", state: "healthy", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.SetTargetHealth(ctx, tt.tgARN, tt.targetID, tt.state)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)

				health, _ := m.DescribeTargetHealth(ctx, tgARN)
				require.Len(t, health, 1)
				assert.Equal(t, tt.state, health[0].State)
			}
		})
	}
}

func TestDescribeTargetHealthNotFound(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.DescribeTargetHealth(ctx, "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDeleteLBCascadesListeners(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	lbARN := createTestLB(t, m)

	_, _ = m.CreateListener(ctx, driver.ListenerConfig{LBARN: lbARN, Protocol: "HTTP", Port: 80})

	require.NoError(t, m.DeleteLoadBalancer(ctx, lbARN))

	// Creating a new LB to verify listeners don't leak
	newLBARN := createTestLB(t, m)
	listeners, err := m.DescribeListeners(ctx, newLBARN)
	require.NoError(t, err)
	assert.Empty(t, listeners)
}
