package loadbalancer

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
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

func TestCreateRule(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	lbARN := createTestLB(t, m)
	tgARN := createTestTargetGroup(t, m)

	li, err := m.CreateListener(ctx, driver.ListenerConfig{
		LBARN: lbARN, Protocol: "HTTP", Port: 80, TargetGroupARN: tgARN,
	})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		rule, ruleErr := m.CreateRule(ctx, driver.RuleConfig{
			ListenerARN: li.ARN,
			Priority:    10,
			Conditions:  []driver.RuleCondition{{Field: "path-pattern", Values: []string{"/api/*"}}},
			Actions:     []driver.RuleAction{{Type: "forward", TargetGroupARN: tgARN}},
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
	lbARN := createTestLB(t, m)

	li, err := m.CreateListener(ctx, driver.ListenerConfig{LBARN: lbARN, Protocol: "HTTP", Port: 80})
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
	lbARN := createTestLB(t, m)
	tgARN := createTestTargetGroup(t, m)

	li, err := m.CreateListener(ctx, driver.ListenerConfig{
		LBARN: lbARN, Protocol: "HTTP", Port: 80, TargetGroupARN: tgARN,
	})
	require.NoError(t, err)

	_, _ = m.CreateRule(ctx, driver.RuleConfig{
		ListenerARN: li.ARN, Priority: 10,
		Conditions: []driver.RuleCondition{{Field: "path-pattern", Values: []string{"/api/*"}}},
		Actions:    []driver.RuleAction{{Type: "forward", TargetGroupARN: tgARN}},
	})
	_, _ = m.CreateRule(ctx, driver.RuleConfig{
		ListenerARN: li.ARN, Priority: 20,
		Conditions: []driver.RuleCondition{{Field: "host-header", Values: []string{"example.com"}}},
		Actions:    []driver.RuleAction{{Type: "forward", TargetGroupARN: tgARN}},
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
	lbARN := createTestLB(t, m)
	tgARN := createTestTargetGroup(t, m)

	li, err := m.CreateListener(ctx, driver.ListenerConfig{
		LBARN: lbARN, Protocol: "HTTP", Port: 80, TargetGroupARN: tgARN,
	})
	require.NoError(t, err)

	t.Run("modify port", func(t *testing.T) {
		require.NoError(t, m.ModifyListener(ctx, driver.ModifyListenerInput{
			ListenerARN: li.ARN, Port: 8080,
		}))

		listeners, _ := m.DescribeListeners(ctx, lbARN)
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
	lbARN := createTestLB(t, m)

	t.Run("default attributes", func(t *testing.T) {
		attrs, err := m.GetLBAttributes(ctx, lbARN)
		require.NoError(t, err)
		assert.Equal(t, 60, attrs.IdleTimeout)
		assert.False(t, attrs.DeletionProtection)
	})

	t.Run("put and get", func(t *testing.T) {
		require.NoError(t, m.PutLBAttributes(ctx, lbARN, driver.LBAttributes{
			IdleTimeout:        120,
			DeletionProtection: true,
			AccessLogsEnabled:  true,
			AccessLogsBucket:   "my-logs",
		}))

		attrs, err := m.GetLBAttributes(ctx, lbARN)
		require.NoError(t, err)
		assert.Equal(t, 120, attrs.IdleTimeout)
		assert.True(t, attrs.DeletionProtection)
		assert.True(t, attrs.AccessLogsEnabled)
		assert.Equal(t, "my-logs", attrs.AccessLogsBucket)
	})

	t.Run("LB not found get", func(t *testing.T) {
		_, err := m.GetLBAttributes(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("LB not found put", func(t *testing.T) {
		err := m.PutLBAttributes(ctx, "missing", driver.LBAttributes{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
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

// TestUpsertAzureLBBackendPoolConcurrent guards the sub-resource CRUD path
// against lost updates: N goroutines each add a distinct pool to the SAME load
// balancer. With a non-atomic Get()+Set() read-modify-write all but a handful
// silently vanish; the store.Update path keeps every one. Run with -race.
func TestUpsertAzureLBBackendPoolConcurrent(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	const rg, name = "rg1", "lb-concurrent-pools"

	_, err := m.CreateOrUpdateAzureLoadBalancer(ctx, rg, name, driver.AzureLoadBalancer{
		Location: "eastus", SKUName: "Standard",
	})
	require.NoError(t, err)

	const n = 50

	var wg sync.WaitGroup

	wg.Add(n)

	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()

			_, upErr := m.UpsertAzureLBBackendPool(ctx, rg, name, fmt.Sprintf("pool-%d", i))
			assert.NoError(t, upErr)
		}(i)
	}

	wg.Wait()

	lb, err := m.GetAzureLoadBalancer(ctx, rg, name)
	require.NoError(t, err)
	assert.Len(t, lb.BackendPools, n, "every concurrently-added backend pool must survive")
}

// TestUpsertAzureLBNatRuleConcurrent is the NAT-rule twin of the backend-pool
// concurrency guard above.
func TestUpsertAzureLBNatRuleConcurrent(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	const rg, name, frontend = "rg1", "lb-concurrent-nat", "fe1"

	_, err := m.CreateOrUpdateAzureLoadBalancer(ctx, rg, name, driver.AzureLoadBalancer{
		Location:  "eastus",
		SKUName:   "Standard",
		Frontends: []driver.AzureLBFrontend{{Name: frontend, PrivateIPAddress: "10.0.0.4"}},
	})
	require.NoError(t, err)

	const n = 50

	var wg sync.WaitGroup

	wg.Add(n)

	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()

			_, upErr := m.UpsertAzureLBNatRule(ctx, rg, name, fmt.Sprintf("nat-%d", i), driver.AzureLBNatRule{
				Protocol:     "Tcp",
				FrontendPort: 1000 + i,
				BackendPort:  22,
				FrontendName: frontend,
			})
			assert.NoError(t, upErr)
		}(i)
	}

	wg.Wait()

	lb, err := m.GetAzureLoadBalancer(ctx, rg, name)
	require.NoError(t, err)
	assert.Len(t, lb.NatRules, n, "every concurrently-added inbound NAT rule must survive")
}

// TestDeleteAzureLBBackendPoolInUseByRule proves the standalone backend-pool
// DELETE rejects a pool still referenced by a load balancing rule instead of
// silently leaving the rule pointing at a pool that no longer exists — the
// whole-LB PUT path rejects this via validateAzureLB, but a standalone DELETE
// bypasses that validation entirely, so the driver itself must guard it.
func TestDeleteAzureLBBackendPoolInUseByRule(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	const rg, name = "rg1", "lb-pool-inuse"

	_, err := m.CreateOrUpdateAzureLoadBalancer(ctx, rg, name, driver.AzureLoadBalancer{
		Location:     "eastus",
		SKUName:      "Standard",
		Frontends:    []driver.AzureLBFrontend{{Name: "fe1", PrivateIPAddress: "10.0.0.4"}},
		BackendPools: []string{"pool-a"},
		Rules: []driver.AzureLBRule{{
			Name: "rule-1", Protocol: "Tcp", FrontendPort: 80, BackendPort: 80,
			FrontendName: "fe1", BackendPoolName: "pool-a",
		}},
	})
	require.NoError(t, err)

	err = m.DeleteAzureLBBackendPool(ctx, rg, name, "pool-a")
	require.Error(t, err)
	assert.True(t, cerrors.IsFailedPrecondition(err), "got %v", err)

	lb, err := m.GetAzureLoadBalancer(ctx, rg, name)
	require.NoError(t, err)
	assert.Contains(t, lb.BackendPools, "pool-a", "pool referenced by a rule must survive the rejected delete")
}

// TestDeleteAzureLBBackendPoolInUseByOutboundRule is the outbound-rule twin of
// the load-balancing-rule guard above.
func TestDeleteAzureLBBackendPoolInUseByOutboundRule(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	const rg, name = "rg1", "lb-pool-inuse-outbound"

	_, err := m.CreateOrUpdateAzureLoadBalancer(ctx, rg, name, driver.AzureLoadBalancer{
		Location:     "eastus",
		SKUName:      "Standard",
		Frontends:    []driver.AzureLBFrontend{{Name: "fe1", PrivateIPAddress: "10.0.0.4"}},
		BackendPools: []string{"pool-a"},
		OutboundRules: []driver.AzureLBOutboundRule{{
			Name: "out-1", Protocol: "All", BackendPoolName: "pool-a", FrontendNames: []string{"fe1"},
		}},
	})
	require.NoError(t, err)

	err = m.DeleteAzureLBBackendPool(ctx, rg, name, "pool-a")
	require.Error(t, err)
	assert.True(t, cerrors.IsFailedPrecondition(err), "got %v", err)

	lb, err := m.GetAzureLoadBalancer(ctx, rg, name)
	require.NoError(t, err)
	assert.Contains(t, lb.BackendPools, "pool-a", "pool referenced by an outbound rule must survive the rejected delete")
}

// TestDeleteAzureLBBackendPoolUnreferencedSucceeds proves the guard does not
// false-positive: a pool with no rule or outbound rule pointing at it deletes
// cleanly.
func TestDeleteAzureLBBackendPoolUnreferencedSucceeds(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	const rg, name = "rg1", "lb-pool-free"

	_, err := m.CreateOrUpdateAzureLoadBalancer(ctx, rg, name, driver.AzureLoadBalancer{
		Location: "eastus", SKUName: "Standard", BackendPools: []string{"pool-a", "pool-b"},
	})
	require.NoError(t, err)

	require.NoError(t, m.DeleteAzureLBBackendPool(ctx, rg, name, "pool-a"))

	lb, err := m.GetAzureLoadBalancer(ctx, rg, name)
	require.NoError(t, err)
	assert.NotContains(t, lb.BackendPools, "pool-a")
	assert.Contains(t, lb.BackendPools, "pool-b")
}
