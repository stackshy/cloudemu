package loadbalancer

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/loadbalancer/driver"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDriver implements driver.LoadBalancer for testing the portable wrapper.
type mockDriver struct {
	lbs          map[string]*driver.LBInfo
	targetGroups map[string]*driver.TargetGroupInfo
	listeners    map[string]*driver.ListenerInfo
	targets      map[string][]driver.TargetHealth
	seq          int
}

func newMockDriver() *mockDriver {
	return &mockDriver{
		lbs:          make(map[string]*driver.LBInfo),
		targetGroups: make(map[string]*driver.TargetGroupInfo),
		listeners:    make(map[string]*driver.ListenerInfo),
		targets:      make(map[string][]driver.TargetHealth),
	}
}

func (m *mockDriver) nextID(prefix string) string {
	m.seq++

	return fmt.Sprintf("%s-%d", prefix, m.seq)
}

func (m *mockDriver) CreateLoadBalancer(_ context.Context, config driver.LBConfig) (*driver.LBInfo, error) {
	if config.Name == "" {
		return nil, fmt.Errorf("name required")
	}

	arn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/" + m.nextID("lb")
	info := &driver.LBInfo{ARN: arn, Name: config.Name, Type: config.Type, Scheme: config.Scheme, State: "active", DNSName: config.Name + ".elb.amazonaws.com"}
	m.lbs[arn] = info

	return info, nil
}

func (m *mockDriver) DeleteLoadBalancer(_ context.Context, arn string) error {
	if _, ok := m.lbs[arn]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.lbs, arn)

	return nil
}

func (m *mockDriver) DescribeLoadBalancers(_ context.Context, arns []string) ([]driver.LBInfo, error) {
	if len(arns) == 0 {
		result := make([]driver.LBInfo, 0, len(m.lbs))
		for _, lb := range m.lbs {
			result = append(result, *lb)
		}

		return result, nil
	}

	var result []driver.LBInfo

	for _, arn := range arns {
		if lb, ok := m.lbs[arn]; ok {
			result = append(result, *lb)
		}
	}

	return result, nil
}

func (m *mockDriver) CreateTargetGroup(_ context.Context, config driver.TargetGroupConfig) (*driver.TargetGroupInfo, error) {
	if config.Name == "" {
		return nil, fmt.Errorf("name required")
	}

	arn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/" + m.nextID("tg")
	info := &driver.TargetGroupInfo{ARN: arn, Name: config.Name, Protocol: config.Protocol, Port: config.Port, VPCID: config.VPCID}
	m.targetGroups[arn] = info

	return info, nil
}

func (m *mockDriver) DeleteTargetGroup(_ context.Context, arn string) error {
	if _, ok := m.targetGroups[arn]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.targetGroups, arn)

	return nil
}

func (m *mockDriver) DescribeTargetGroups(_ context.Context, arns []string) ([]driver.TargetGroupInfo, error) {
	if len(arns) == 0 {
		result := make([]driver.TargetGroupInfo, 0, len(m.targetGroups))
		for _, tg := range m.targetGroups {
			result = append(result, *tg)
		}

		return result, nil
	}

	var result []driver.TargetGroupInfo

	for _, arn := range arns {
		if tg, ok := m.targetGroups[arn]; ok {
			result = append(result, *tg)
		}
	}

	return result, nil
}

func (m *mockDriver) CreateListener(_ context.Context, config driver.ListenerConfig) (*driver.ListenerInfo, error) {
	if _, ok := m.lbs[config.LBARN]; !ok {
		return nil, fmt.Errorf("lb not found")
	}

	arn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:listener/" + m.nextID("lis")
	info := &driver.ListenerInfo{ARN: arn, LBARN: config.LBARN, Protocol: config.Protocol, Port: config.Port, TargetGroupARN: config.TargetGroupARN}
	m.listeners[arn] = info

	return info, nil
}

func (m *mockDriver) DeleteListener(_ context.Context, arn string) error {
	if _, ok := m.listeners[arn]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.listeners, arn)

	return nil
}

func (m *mockDriver) DescribeListeners(_ context.Context, lbARN string) ([]driver.ListenerInfo, error) {
	var result []driver.ListenerInfo

	for _, lis := range m.listeners {
		if lis.LBARN == lbARN {
			result = append(result, *lis)
		}
	}

	return result, nil
}

func (m *mockDriver) RegisterTargets(_ context.Context, tgARN string, targets []driver.Target) error {
	if _, ok := m.targetGroups[tgARN]; !ok {
		return fmt.Errorf("target group not found")
	}

	for _, t := range targets {
		m.targets[tgARN] = append(m.targets[tgARN], driver.TargetHealth{Target: t, State: "healthy"})
	}

	return nil
}

func (m *mockDriver) DeregisterTargets(_ context.Context, tgARN string, _ []driver.Target) error {
	if _, ok := m.targetGroups[tgARN]; !ok {
		return fmt.Errorf("target group not found")
	}

	return nil
}

func (m *mockDriver) DescribeTargetHealth(_ context.Context, tgARN string) ([]driver.TargetHealth, error) {
	if _, ok := m.targetGroups[tgARN]; !ok {
		return nil, fmt.Errorf("target group not found")
	}

	return m.targets[tgARN], nil
}

func (m *mockDriver) SetTargetHealth(_ context.Context, tgARN, targetID, state string) error {
	if _, ok := m.targetGroups[tgARN]; !ok {
		return fmt.Errorf("target group not found")
	}

	for idx := range m.targets[tgARN] {
		if m.targets[tgARN][idx].Target.ID == targetID {
			m.targets[tgARN][idx].State = state
			return nil
		}
	}

	return fmt.Errorf("target not found")
}

func newTestLB(opts ...Option) *LB {
	return NewLB(newMockDriver(), opts...)
}

func TestNewLB(t *testing.T) {
	lb := newTestLB()
	require.NotNil(t, lb)
	require.NotNil(t, lb.driver)
}

func TestCreateLoadBalancer(t *testing.T) {
	lb := newTestLB()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		info, err := lb.CreateLoadBalancer(ctx, driver.LBConfig{Name: "my-lb", Type: "application"})
		require.NoError(t, err)
		assert.Equal(t, "my-lb", info.Name)
		assert.NotEmpty(t, info.ARN)
		assert.Equal(t, "active", info.State)
	})

	t.Run("empty name error", func(t *testing.T) {
		_, err := lb.CreateLoadBalancer(ctx, driver.LBConfig{})
		require.Error(t, err)
	})
}

func TestDeleteLoadBalancer(t *testing.T) {
	lb := newTestLB()
	ctx := context.Background()

	info, err := lb.CreateLoadBalancer(ctx, driver.LBConfig{Name: "del-lb"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := lb.DeleteLoadBalancer(ctx, info.ARN)
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := lb.DeleteLoadBalancer(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestDescribeLoadBalancers(t *testing.T) {
	lb := newTestLB()
	ctx := context.Background()

	_, err := lb.CreateLoadBalancer(ctx, driver.LBConfig{Name: "lb-a"})
	require.NoError(t, err)

	_, err = lb.CreateLoadBalancer(ctx, driver.LBConfig{Name: "lb-b"})
	require.NoError(t, err)

	lbs, err := lb.DescribeLoadBalancers(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, len(lbs))
}

func TestCreateTargetGroup(t *testing.T) {
	lb := newTestLB()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		info, err := lb.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "my-tg", Protocol: "HTTP", Port: 80})
		require.NoError(t, err)
		assert.Equal(t, "my-tg", info.Name)
	})

	t.Run("empty name error", func(t *testing.T) {
		_, err := lb.CreateTargetGroup(ctx, driver.TargetGroupConfig{})
		require.Error(t, err)
	})
}

func TestDeleteTargetGroup(t *testing.T) {
	lb := newTestLB()
	ctx := context.Background()

	tg, err := lb.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "del-tg"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := lb.DeleteTargetGroup(ctx, tg.ARN)
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := lb.DeleteTargetGroup(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestDescribeTargetGroups(t *testing.T) {
	lb := newTestLB()
	ctx := context.Background()

	_, err := lb.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "tg-a"})
	require.NoError(t, err)

	_, err = lb.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "tg-b"})
	require.NoError(t, err)

	tgs, err := lb.DescribeTargetGroups(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, len(tgs))
}

func TestCreateListener(t *testing.T) {
	lb := newTestLB()
	ctx := context.Background()

	lbInfo, err := lb.CreateLoadBalancer(ctx, driver.LBConfig{Name: "lis-lb"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		info, err := lb.CreateListener(ctx, driver.ListenerConfig{LBARN: lbInfo.ARN, Protocol: "HTTP", Port: 80})
		require.NoError(t, err)
		assert.Equal(t, lbInfo.ARN, info.LBARN)
	})

	t.Run("lb not found", func(t *testing.T) {
		_, err := lb.CreateListener(ctx, driver.ListenerConfig{LBARN: "nonexistent"})
		require.Error(t, err)
	})
}

func TestDeleteListener(t *testing.T) {
	lb := newTestLB()
	ctx := context.Background()

	lbInfo, err := lb.CreateLoadBalancer(ctx, driver.LBConfig{Name: "dellis-lb"})
	require.NoError(t, err)

	lis, err := lb.CreateListener(ctx, driver.ListenerConfig{LBARN: lbInfo.ARN, Protocol: "HTTP", Port: 80})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := lb.DeleteListener(ctx, lis.ARN)
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := lb.DeleteListener(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestDescribeListeners(t *testing.T) {
	lb := newTestLB()
	ctx := context.Background()

	lbInfo, err := lb.CreateLoadBalancer(ctx, driver.LBConfig{Name: "desclis-lb"})
	require.NoError(t, err)

	_, err = lb.CreateListener(ctx, driver.ListenerConfig{LBARN: lbInfo.ARN, Protocol: "HTTP", Port: 80})
	require.NoError(t, err)

	listeners, err := lb.DescribeListeners(ctx, lbInfo.ARN)
	require.NoError(t, err)
	assert.Equal(t, 1, len(listeners))
}

func TestRegisterAndDescribeTargets(t *testing.T) {
	lb := newTestLB()
	ctx := context.Background()

	tg, err := lb.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "reg-tg"})
	require.NoError(t, err)

	targets := []driver.Target{{ID: "i-1234", Port: 80}}

	err = lb.RegisterTargets(ctx, tg.ARN, targets)
	require.NoError(t, err)

	health, err := lb.DescribeTargetHealth(ctx, tg.ARN)
	require.NoError(t, err)
	assert.Equal(t, 1, len(health))
	assert.Equal(t, "healthy", health[0].State)
}

func TestDeregisterTargets(t *testing.T) {
	lb := newTestLB()
	ctx := context.Background()

	tg, err := lb.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "dereg-tg"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := lb.DeregisterTargets(ctx, tg.ARN, []driver.Target{{ID: "i-1234", Port: 80}})
		require.NoError(t, err)
	})

	t.Run("tg not found", func(t *testing.T) {
		err := lb.DeregisterTargets(ctx, "nonexistent", []driver.Target{{ID: "i-1234"}})
		require.Error(t, err)
	})
}

func TestSetTargetHealth(t *testing.T) {
	lb := newTestLB()
	ctx := context.Background()

	tg, err := lb.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "health-tg"})
	require.NoError(t, err)

	err = lb.RegisterTargets(ctx, tg.ARN, []driver.Target{{ID: "i-1234", Port: 80}})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := lb.SetTargetHealth(ctx, tg.ARN, "i-1234", "unhealthy")
		require.NoError(t, err)
	})

	t.Run("target not found", func(t *testing.T) {
		err := lb.SetTargetHealth(ctx, tg.ARN, "nonexistent", "unhealthy")
		require.Error(t, err)
	})
}

func TestLBWithRecorder(t *testing.T) {
	rec := recorder.New()
	lb := newTestLB(WithRecorder(rec))
	ctx := context.Background()

	_, err := lb.CreateLoadBalancer(ctx, driver.LBConfig{Name: "rec-lb"})
	require.NoError(t, err)

	totalCalls := rec.CallCount()
	assert.GreaterOrEqual(t, totalCalls, 1)

	createCalls := rec.CallCountFor("loadbalancer", "CreateLoadBalancer")
	assert.Equal(t, 1, createCalls)
}

func TestLBWithRecorderOnError(t *testing.T) {
	rec := recorder.New()
	lb := newTestLB(WithRecorder(rec))
	ctx := context.Background()

	_ = lb.DeleteLoadBalancer(ctx, "nonexistent")

	totalCalls := rec.CallCount()
	assert.Equal(t, 1, totalCalls)

	last := rec.LastCall()
	require.NotNil(t, last)
	assert.NotNil(t, last.Error)
}

func TestLBWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	lb := newTestLB(WithMetrics(mc))
	ctx := context.Background()

	_, err := lb.CreateLoadBalancer(ctx, driver.LBConfig{Name: "met-lb"})
	require.NoError(t, err)

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").Count()
	assert.GreaterOrEqual(t, callsCount, 1)

	durCount := q.ByName("call_duration").Count()
	assert.GreaterOrEqual(t, durCount, 1)
}

func TestLBWithMetricsOnError(t *testing.T) {
	mc := metrics.NewCollector()
	lb := newTestLB(WithMetrics(mc))
	ctx := context.Background()

	_ = lb.DeleteLoadBalancer(ctx, "nonexistent")

	q := metrics.NewQuery(mc)

	errCount := q.ByName("errors_total").Count()
	assert.Equal(t, 1, errCount)
}

func TestLBWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	lb := newTestLB(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("injected failure")
	inj.Set("loadbalancer", "CreateLoadBalancer", injectedErr, inject.Always{})

	_, err := lb.CreateLoadBalancer(ctx, driver.LBConfig{Name: "fail-lb"})
	require.Error(t, err)
	assert.Equal(t, injectedErr, err)
}

func TestLBWithErrorInjectionRecorded(t *testing.T) {
	rec := recorder.New()
	inj := inject.NewInjector()
	lb := newTestLB(WithErrorInjection(inj), WithRecorder(rec))
	ctx := context.Background()

	injectedErr := fmt.Errorf("boom")
	inj.Set("loadbalancer", "DeleteLoadBalancer", injectedErr, inject.Always{})

	_, err := lb.CreateLoadBalancer(ctx, driver.LBConfig{Name: "inj-lb"})
	require.NoError(t, err)

	err = lb.DeleteLoadBalancer(ctx, "some-arn")
	require.Error(t, err)

	delCalls := rec.CallsFor("loadbalancer", "DeleteLoadBalancer")
	assert.Equal(t, 1, len(delCalls))
	assert.NotNil(t, delCalls[0].Error)
}

func TestLBWithErrorInjectionRemoved(t *testing.T) {
	inj := inject.NewInjector()
	lb := newTestLB(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("fail")
	inj.Set("loadbalancer", "CreateLoadBalancer", injectedErr, inject.Always{})

	_, err := lb.CreateLoadBalancer(ctx, driver.LBConfig{Name: "test"})
	require.Error(t, err)

	inj.Remove("loadbalancer", "CreateLoadBalancer")

	_, err = lb.CreateLoadBalancer(ctx, driver.LBConfig{Name: "test"})
	require.NoError(t, err)
}

func TestLBWithLatency(t *testing.T) {
	latency := 1 * time.Millisecond
	lb := newTestLB(WithLatency(latency))
	ctx := context.Background()

	info, err := lb.CreateLoadBalancer(ctx, driver.LBConfig{Name: "lat-lb"})
	require.NoError(t, err)
	assert.Equal(t, "lat-lb", info.Name)
}

func TestLBAllOptionsComposed(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	inj := inject.NewInjector()
	latency := 1 * time.Millisecond

	lb := NewLB(newMockDriver(),
		WithRecorder(rec),
		WithMetrics(mc),
		WithErrorInjection(inj),
		WithLatency(latency),
	)
	ctx := context.Background()

	_, err := lb.CreateLoadBalancer(ctx, driver.LBConfig{Name: "all-opts"})
	require.NoError(t, err)

	_, err = lb.DescribeLoadBalancers(ctx, nil)
	require.NoError(t, err)

	assert.Equal(t, 2, rec.CallCount())

	q := metrics.NewQuery(mc)
	assert.Equal(t, 2, q.ByName("calls_total").Count())
}
