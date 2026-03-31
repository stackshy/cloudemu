package gcplb

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
	opts := config.NewOptions(config.WithClock(clk), config.WithRegion("us-central1"), config.WithProjectID("test-project"))

	return New(opts)
}

func TestCreateLoadBalancer(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name      string
		cfg       driver.LBConfig
		wantErr   bool
		errSubstr string
	}{
		{name: "success", cfg: driver.LBConfig{
			Name: "my-lb", Type: "application", Scheme: "internet-facing",
			Subnets: []string{"subnet-1"}, Tags: map[string]string{"env": "test"},
		}},
		{name: "empty name", cfg: driver.LBConfig{}, wantErr: true, errSubstr: "required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateLoadBalancer(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, "my-lb", info.Name)
				assert.Equal(t, "active", info.State)
				assert.NotEmpty(t, info.ARN)
				assert.NotEmpty(t, info.DNSName)
				assert.Equal(t, "application", info.Type)
			}
		})
	}
}

func TestDeleteLoadBalancer(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	lb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "lb1"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		arn       string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", arn: lb.ARN},
		{name: "not found", arn: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteLoadBalancer(ctx, tt.arn)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDescribeLoadBalancers(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	lb1, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "lb1"})
	require.NoError(t, err)
	_, err = m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "lb2"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		arns      []string
		wantCount int
	}{
		{name: "all", arns: nil, wantCount: 2},
		{name: "by arn", arns: []string{lb1.ARN}, wantCount: 1},
		{name: "unknown arn", arns: []string{"nope"}, wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lbs, descErr := m.DescribeLoadBalancers(ctx, tt.arns)
			require.NoError(t, descErr)
			assert.Len(t, lbs, tt.wantCount)
		})
	}
}

func TestCreateTargetGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name      string
		cfg       driver.TargetGroupConfig
		wantErr   bool
		errSubstr string
	}{
		{name: "success", cfg: driver.TargetGroupConfig{
			Name: "tg1", Protocol: "HTTP", Port: 80, VPCID: "vpc-1", HealthPath: "/health",
		}},
		{name: "empty name", cfg: driver.TargetGroupConfig{}, wantErr: true, errSubstr: "required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateTargetGroup(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, "tg1", info.Name)
				assert.Equal(t, "HTTP", info.Protocol)
				assert.Equal(t, 80, info.Port)
				assert.NotEmpty(t, info.ARN)
			}
		})
	}
}

func TestDeleteTargetGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tg, err := m.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "tg1", Protocol: "HTTP", Port: 80})
	require.NoError(t, err)

	tests := []struct {
		name      string
		arn       string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", arn: tg.ARN},
		{name: "not found", arn: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteTargetGroup(ctx, tt.arn)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDescribeTargetGroups(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tg1, err := m.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "tg1", Protocol: "HTTP", Port: 80})
	require.NoError(t, err)
	_, err = m.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "tg2", Protocol: "HTTPS", Port: 443})
	require.NoError(t, err)

	tests := []struct {
		name      string
		arns      []string
		wantCount int
	}{
		{name: "all", arns: nil, wantCount: 2},
		{name: "by arn", arns: []string{tg1.ARN}, wantCount: 1},
		{name: "unknown arn", arns: []string{"nope"}, wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tgs, descErr := m.DescribeTargetGroups(ctx, tt.arns)
			require.NoError(t, descErr)
			assert.Len(t, tgs, tt.wantCount)
		})
	}
}

func TestCreateListener(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	lb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "lb1"})
	require.NoError(t, err)
	tg, err := m.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "tg1", Protocol: "HTTP", Port: 80})
	require.NoError(t, err)

	tests := []struct {
		name      string
		cfg       driver.ListenerConfig
		wantErr   bool
		errSubstr string
	}{
		{name: "success", cfg: driver.ListenerConfig{LBARN: lb.ARN, Protocol: "HTTP", Port: 80, TargetGroupARN: tg.ARN}},
		{name: "lb not found", cfg: driver.ListenerConfig{LBARN: "missing", Port: 80}, wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateListener(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, info.ARN)
				assert.Equal(t, lb.ARN, info.LBARN)
				assert.Equal(t, 80, info.Port)
			}
		})
	}
}

func TestRegisterAndDeregisterTargets(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tg, err := m.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "tg1", Protocol: "HTTP", Port: 80})
	require.NoError(t, err)

	targets := []driver.Target{
		{ID: "inst-1", Port: 80},
		{ID: "inst-2", Port: 80},
	}

	t.Run("register targets", func(t *testing.T) {
		require.NoError(t, m.RegisterTargets(ctx, tg.ARN, targets))
		health, descErr := m.DescribeTargetHealth(ctx, tg.ARN)
		require.NoError(t, descErr)
		assert.Len(t, health, 2)
		assert.Equal(t, "initial", health[0].State)
	})

	t.Run("register to missing TG", func(t *testing.T) {
		err := m.RegisterTargets(ctx, "missing", targets)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("deregister targets", func(t *testing.T) {
		require.NoError(t, m.DeregisterTargets(ctx, tg.ARN, []driver.Target{{ID: "inst-1", Port: 80}}))
		health, descErr := m.DescribeTargetHealth(ctx, tg.ARN)
		require.NoError(t, descErr)
		assert.Len(t, health, 1)
	})

	t.Run("deregister from missing TG", func(t *testing.T) {
		err := m.DeregisterTargets(ctx, "missing", targets)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestSetTargetHealth(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tg, err := m.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "tg1", Protocol: "HTTP", Port: 80})
	require.NoError(t, err)
	require.NoError(t, m.RegisterTargets(ctx, tg.ARN, []driver.Target{{ID: "inst-1", Port: 80}}))

	tests := []struct {
		name      string
		tgARN     string
		targetID  string
		state     string
		wantErr   bool
		errSubstr string
	}{
		{name: "set healthy", tgARN: tg.ARN, targetID: "inst-1", state: "healthy"},
		{name: "tg not found", tgARN: "missing", targetID: "inst-1", state: "healthy", wantErr: true, errSubstr: "not found"},
		{name: "target not found", tgARN: tg.ARN, targetID: "inst-999", state: "healthy", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.SetTargetHealth(ctx, tt.tgARN, tt.targetID, tt.state)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				health, descErr := m.DescribeTargetHealth(ctx, tg.ARN)
				require.NoError(t, descErr)
				require.Len(t, health, 1)
				assert.Equal(t, "healthy", health[0].State)
			}
		})
	}
}

func TestDescribeTargetHealthErrors(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.DescribeTargetHealth(ctx, "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDescribeListeners(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	lb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "lb1"})
	require.NoError(t, err)

	_, err = m.CreateListener(ctx, driver.ListenerConfig{LBARN: lb.ARN, Protocol: "HTTP", Port: 80})
	require.NoError(t, err)
	_, err = m.CreateListener(ctx, driver.ListenerConfig{LBARN: lb.ARN, Protocol: "HTTPS", Port: 443})
	require.NoError(t, err)

	t.Run("list listeners", func(t *testing.T) {
		listeners, descErr := m.DescribeListeners(ctx, lb.ARN)
		require.NoError(t, descErr)
		assert.Len(t, listeners, 2)
	})

	t.Run("lb not found", func(t *testing.T) {
		_, descErr := m.DescribeListeners(ctx, "missing")
		require.Error(t, descErr)
		assert.Contains(t, descErr.Error(), "not found")
	})
}

func TestCreateRule(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	lb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "lb1"})
	require.NoError(t, err)

	tg, err := m.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "tg1", Protocol: "HTTP", Port: 80})
	require.NoError(t, err)

	li, err := m.CreateListener(ctx, driver.ListenerConfig{
		LBARN: lb.ARN, Protocol: "HTTP", Port: 80, TargetGroupARN: tg.ARN,
	})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		rule, ruleErr := m.CreateRule(ctx, driver.RuleConfig{
			ListenerARN: li.ARN,
			Priority:    10,
			Conditions:  []driver.RuleCondition{{Field: "path-pattern", Values: []string{"/api/*"}}},
			Actions:     []driver.RuleAction{{Type: "forward", TargetGroupARN: tg.ARN}},
		})
		require.NoError(t, ruleErr)
		assert.NotEmpty(t, rule.ARN)
		assert.Equal(t, li.ARN, rule.ListenerARN)
		assert.Equal(t, 10, rule.Priority)
		assert.False(t, rule.IsDefault)
	})

	t.Run("listener not found", func(t *testing.T) {
		_, ruleErr := m.CreateRule(ctx, driver.RuleConfig{ListenerARN: "missing"})
		require.Error(t, ruleErr)
		assert.Contains(t, ruleErr.Error(), "not found")
	})
}

func TestDeleteRule(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	lb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "lb1"})
	require.NoError(t, err)

	li, err := m.CreateListener(ctx, driver.ListenerConfig{LBARN: lb.ARN, Protocol: "HTTP", Port: 80})
	require.NoError(t, err)

	rule, err := m.CreateRule(ctx, driver.RuleConfig{ListenerARN: li.ARN, Priority: 10})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		require.NoError(t, m.DeleteRule(ctx, rule.ARN))
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteRule(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDescribeRules(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	lb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "lb1"})
	require.NoError(t, err)

	tg, err := m.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "tg1", Protocol: "HTTP", Port: 80})
	require.NoError(t, err)

	li, err := m.CreateListener(ctx, driver.ListenerConfig{
		LBARN: lb.ARN, Protocol: "HTTP", Port: 80, TargetGroupARN: tg.ARN,
	})
	require.NoError(t, err)

	_, _ = m.CreateRule(ctx, driver.RuleConfig{
		ListenerARN: li.ARN, Priority: 10,
		Conditions: []driver.RuleCondition{{Field: "path-pattern", Values: []string{"/api/*"}}},
		Actions:    []driver.RuleAction{{Type: "forward", TargetGroupARN: tg.ARN}},
	})
	_, _ = m.CreateRule(ctx, driver.RuleConfig{
		ListenerARN: li.ARN, Priority: 20,
		Conditions: []driver.RuleCondition{{Field: "host-header", Values: []string{"example.com"}}},
		Actions:    []driver.RuleAction{{Type: "forward", TargetGroupARN: tg.ARN}},
	})

	t.Run("success", func(t *testing.T) {
		rules, descErr := m.DescribeRules(ctx, li.ARN)
		require.NoError(t, descErr)
		assert.Len(t, rules, 2)
	})

	t.Run("listener not found", func(t *testing.T) {
		_, descErr := m.DescribeRules(ctx, "missing")
		require.Error(t, descErr)
		assert.Contains(t, descErr.Error(), "not found")
	})
}

func TestModifyListener(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	lb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "lb1"})
	require.NoError(t, err)

	tg, err := m.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "tg1", Protocol: "HTTP", Port: 80})
	require.NoError(t, err)

	li, err := m.CreateListener(ctx, driver.ListenerConfig{
		LBARN: lb.ARN, Protocol: "HTTP", Port: 80, TargetGroupARN: tg.ARN,
	})
	require.NoError(t, err)

	t.Run("modify port", func(t *testing.T) {
		require.NoError(t, m.ModifyListener(ctx, driver.ModifyListenerInput{
			ListenerARN: li.ARN, Port: 8080,
		}))

		listeners, _ := m.DescribeListeners(ctx, lb.ARN)
		assert.Equal(t, 8080, listeners[0].Port)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.ModifyListener(ctx, driver.ModifyListenerInput{ListenerARN: "missing", Port: 80})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestLBAttributes(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	lb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "lb1"})
	require.NoError(t, err)

	t.Run("default attributes", func(t *testing.T) {
		attrs, attrErr := m.GetLBAttributes(ctx, lb.ARN)
		require.NoError(t, attrErr)
		assert.Equal(t, 60, attrs.IdleTimeout)
		assert.False(t, attrs.DeletionProtection)
	})

	t.Run("put and get", func(t *testing.T) {
		require.NoError(t, m.PutLBAttributes(ctx, lb.ARN, driver.LBAttributes{
			IdleTimeout:        120,
			DeletionProtection: true,
			AccessLogsEnabled:  true,
			AccessLogsBucket:   "my-logs",
		}))

		attrs, attrErr := m.GetLBAttributes(ctx, lb.ARN)
		require.NoError(t, attrErr)
		assert.Equal(t, 120, attrs.IdleTimeout)
		assert.True(t, attrs.DeletionProtection)
		assert.True(t, attrs.AccessLogsEnabled)
		assert.Equal(t, "my-logs", attrs.AccessLogsBucket)
	})

	t.Run("LB not found get", func(t *testing.T) {
		_, attrErr := m.GetLBAttributes(ctx, "missing")
		require.Error(t, attrErr)
		assert.Contains(t, attrErr.Error(), "not found")
	})

	t.Run("LB not found put", func(t *testing.T) {
		attrErr := m.PutLBAttributes(ctx, "missing", driver.LBAttributes{})
		require.Error(t, attrErr)
		assert.Contains(t, attrErr.Error(), "not found")
	})
}

func TestDeleteListenerCleansUpOnLBDelete(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	lb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "lb1"})
	require.NoError(t, err)

	li, err := m.CreateListener(ctx, driver.ListenerConfig{LBARN: lb.ARN, Protocol: "HTTP", Port: 80})
	require.NoError(t, err)

	require.NoError(t, m.DeleteLoadBalancer(ctx, lb.ARN))

	// Listener should be gone
	err = m.DeleteListener(ctx, li.ARN)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
