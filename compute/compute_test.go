package compute

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCompute is a minimal in-memory mock that satisfies driver.Compute.
type mockCompute struct {
	mu        sync.Mutex
	instances map[string]*driver.Instance
	counter   int
}

func newMockCompute() *mockCompute {
	return &mockCompute{instances: make(map[string]*driver.Instance)}
}

func (m *mockCompute) RunInstances(_ context.Context, cfg driver.InstanceConfig, count int) ([]driver.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []driver.Instance

	for range count {
		m.counter++
		id := fmt.Sprintf("i-%06d", m.counter)

		inst := &driver.Instance{
			ID:           id,
			ImageID:      cfg.ImageID,
			InstanceType: cfg.InstanceType,
			State:        "running",
			Tags:         cfg.Tags,
		}
		m.instances[id] = inst
		result = append(result, *inst)
	}

	return result, nil
}

func (m *mockCompute) findInstance(id string) (*driver.Instance, error) {
	inst, ok := m.instances[id]
	if !ok {
		return nil, fmt.Errorf("instance %s not found", id)
	}

	return inst, nil
}

func (m *mockCompute) StartInstances(_ context.Context, ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, id := range ids {
		inst, err := m.findInstance(id)
		if err != nil {
			return err
		}

		inst.State = "running"
	}

	return nil
}

func (m *mockCompute) StopInstances(_ context.Context, ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, id := range ids {
		inst, err := m.findInstance(id)
		if err != nil {
			return err
		}

		inst.State = "stopped"
	}

	return nil
}

func (m *mockCompute) RebootInstances(_ context.Context, ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, id := range ids {
		if _, err := m.findInstance(id); err != nil {
			return err
		}
	}

	return nil
}

func (m *mockCompute) TerminateInstances(_ context.Context, ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, id := range ids {
		inst, err := m.findInstance(id)
		if err != nil {
			return err
		}

		inst.State = "terminated"
	}

	return nil
}

func (m *mockCompute) DescribeInstances(_ context.Context, ids []string, _ []driver.DescribeFilter) ([]driver.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []driver.Instance

	if len(ids) == 0 {
		for _, inst := range m.instances {
			result = append(result, *inst)
		}

		return result, nil
	}

	for _, id := range ids {
		inst, err := m.findInstance(id)
		if err != nil {
			return nil, err
		}

		result = append(result, *inst)
	}

	return result, nil
}

func (m *mockCompute) ModifyInstance(_ context.Context, id string, input driver.ModifyInstanceInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, err := m.findInstance(id)
	if err != nil {
		return err
	}

	if input.InstanceType != "" {
		inst.InstanceType = input.InstanceType
	}

	return nil
}

func (m *mockCompute) CreateAutoScalingGroup(_ context.Context, cfg driver.AutoScalingGroupConfig) (*driver.AutoScalingGroup, error) {
	return &driver.AutoScalingGroup{Name: cfg.Name, DesiredCapacity: cfg.DesiredCapacity, Status: "Active"}, nil
}

func (m *mockCompute) DeleteAutoScalingGroup(context.Context, string, bool) error { return nil }

func (m *mockCompute) GetAutoScalingGroup(_ context.Context, name string) (*driver.AutoScalingGroup, error) {
	return &driver.AutoScalingGroup{Name: name, Status: "Active"}, nil
}

func (m *mockCompute) ListAutoScalingGroups(context.Context) ([]driver.AutoScalingGroup, error) {
	return nil, nil
}

func (m *mockCompute) UpdateAutoScalingGroup(context.Context, string, int, int, int) error {
	return nil
}

func (m *mockCompute) SetDesiredCapacity(context.Context, string, int) error { return nil }

func (m *mockCompute) PutScalingPolicy(context.Context, driver.ScalingPolicy) error { return nil }

func (m *mockCompute) DeleteScalingPolicy(context.Context, string, string) error { return nil }

func (m *mockCompute) ExecuteScalingPolicy(context.Context, string, string) error { return nil }

func (m *mockCompute) RequestSpotInstances(_ context.Context, cfg driver.SpotRequestConfig) ([]driver.SpotInstanceRequest, error) {
	return []driver.SpotInstanceRequest{{ID: "sir-001", Status: "open"}}, nil
}

func (m *mockCompute) CancelSpotRequests(context.Context, []string) error { return nil }

func (m *mockCompute) DescribeSpotRequests(_ context.Context, _ []string) ([]driver.SpotInstanceRequest, error) {
	return nil, nil
}

func (m *mockCompute) CreateLaunchTemplate(_ context.Context, cfg driver.LaunchTemplateConfig) (*driver.LaunchTemplate, error) {
	return &driver.LaunchTemplate{Name: cfg.Name}, nil
}

func (m *mockCompute) DeleteLaunchTemplate(context.Context, string) error { return nil }

func (m *mockCompute) GetLaunchTemplate(_ context.Context, name string) (*driver.LaunchTemplate, error) {
	return &driver.LaunchTemplate{Name: name}, nil
}

func (m *mockCompute) ListLaunchTemplates(context.Context) ([]driver.LaunchTemplate, error) {
	return nil, nil
}

func newTestCompute(opts ...Option) *Compute {
	return NewCompute(newMockCompute(), opts...)
}

func TestNewCompute(t *testing.T) {
	c := newTestCompute()

	require.NotNil(t, c)
	require.NotNil(t, c.driver)
}

func TestRunInstancesPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	instances, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID:      "ami-12345",
		InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, len(instances))
	assert.Equal(t, "running", instances[0].State)
}

func TestDescribeInstancesPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	instances, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID:      "ami-12345",
		InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)

	described, err := c.DescribeInstances(ctx, []string{instances[0].ID}, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, len(described))
	assert.Equal(t, instances[0].ID, described[0].ID)
}

func TestStopStartInstancesPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	instances, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID:      "ami-12345",
		InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)

	err = c.StopInstances(ctx, []string{instances[0].ID})
	require.NoError(t, err)

	err = c.StartInstances(ctx, []string{instances[0].ID})
	require.NoError(t, err)
}

func TestTerminateInstancesPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	instances, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID:      "ami-12345",
		InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)

	err = c.TerminateInstances(ctx, []string{instances[0].ID})
	require.NoError(t, err)
}

func TestRebootInstancesPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	instances, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID:      "ami-12345",
		InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)

	err = c.RebootInstances(ctx, []string{instances[0].ID})
	require.NoError(t, err)
}

func TestWithRecorder(t *testing.T) {
	rec := recorder.New()
	c := newTestCompute(WithRecorder(rec))
	ctx := context.Background()

	instances, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID:      "ami-12345",
		InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)

	_, err = c.DescribeInstances(ctx, []string{instances[0].ID}, nil)
	require.NoError(t, err)

	totalCalls := rec.CallCount()
	assert.GreaterOrEqual(t, totalCalls, 2)

	runCalls := rec.CallCountFor("compute", "RunInstances")
	assert.Equal(t, 1, runCalls)

	describeCalls := rec.CallCountFor("compute", "DescribeInstances")
	assert.Equal(t, 1, describeCalls)
}

func TestWithRecorderOnError(t *testing.T) {
	rec := recorder.New()
	c := newTestCompute(WithRecorder(rec))
	ctx := context.Background()

	_ = c.StopInstances(ctx, []string{"i-nonexistent"})

	totalCalls := rec.CallCount()
	assert.Equal(t, 1, totalCalls)

	last := rec.LastCall()
	require.NotNil(t, last, "expected a recorded call")
	assert.NotNil(t, last.Error, "expected recorded call to have an error")
}

func TestWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	c := newTestCompute(WithMetrics(mc))
	ctx := context.Background()

	instances, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID:      "ami-12345",
		InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)

	_, err = c.DescribeInstances(ctx, []string{instances[0].ID}, nil)
	require.NoError(t, err)

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").Count()
	assert.GreaterOrEqual(t, callsCount, 2)

	durCount := q.ByName("call_duration").Count()
	assert.GreaterOrEqual(t, durCount, 2)
}

func TestWithMetricsOnError(t *testing.T) {
	mc := metrics.NewCollector()
	c := newTestCompute(WithMetrics(mc))
	ctx := context.Background()

	_ = c.StopInstances(ctx, []string{"i-nonexistent"})

	q := metrics.NewQuery(mc)

	errCount := q.ByName("errors_total").Count()
	assert.Equal(t, 1, errCount)
}

func TestWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	c := newTestCompute(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("injected failure")
	inj.Set("compute", "RunInstances", injectedErr, inject.Always{})

	_, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID:      "ami-12345",
		InstanceType: "t2.micro",
	}, 1)
	require.Error(t, err)
	assert.Equal(t, injectedErr, err)
}

func TestWithErrorInjectionRecorded(t *testing.T) {
	rec := recorder.New()
	inj := inject.NewInjector()
	c := newTestCompute(WithErrorInjection(inj), WithRecorder(rec))
	ctx := context.Background()

	injectedErr := fmt.Errorf("boom")
	inj.Set("compute", "StopInstances", injectedErr, inject.Always{})

	_, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID:      "ami-12345",
		InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)

	err = c.StopInstances(ctx, []string{"i-12345"})
	require.Error(t, err)

	stopCalls := rec.CallsFor("compute", "StopInstances")
	assert.Equal(t, 1, len(stopCalls))
	assert.NotNil(t, stopCalls[0].Error, "expected recorded StopInstances call to have an error")
}

func TestWithErrorInjectionRemoved(t *testing.T) {
	inj := inject.NewInjector()
	c := newTestCompute(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("fail")
	inj.Set("compute", "RunInstances", injectedErr, inject.Always{})

	_, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID:      "ami-12345",
		InstanceType: "t2.micro",
	}, 1)
	require.Error(t, err)

	inj.Remove("compute", "RunInstances")

	_, err = c.RunInstances(ctx, driver.InstanceConfig{
		ImageID:      "ami-12345",
		InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)
}

func TestWithRateLimiter(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	limiter := ratelimit.New(1, 1, fc)
	c := NewCompute(newMockCompute(), WithRateLimiter(limiter))
	ctx := context.Background()

	_, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID:      "ami-12345",
		InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)

	_, err = c.DescribeInstances(ctx, nil, nil)
	require.Error(t, err, "expected rate limit error on second call without time advance")
}

func TestWithLatency(t *testing.T) {
	latency := 1 * time.Millisecond
	c := newTestCompute(WithLatency(latency))
	ctx := context.Background()

	instances, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID:      "ami-12345",
		InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, len(instances))
}

func TestAllOptionsComposed(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	inj := inject.NewInjector()
	latency := 1 * time.Millisecond

	c := NewCompute(newMockCompute(),
		WithRecorder(rec),
		WithMetrics(mc),
		WithErrorInjection(inj),
		WithLatency(latency),
	)
	ctx := context.Background()

	_, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID:      "ami-12345",
		InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)

	_, err = c.DescribeInstances(ctx, nil, nil)
	require.NoError(t, err)

	assert.Equal(t, 2, rec.CallCount())

	q := metrics.NewQuery(mc)
	assert.Equal(t, 2, q.ByName("calls_total").Count())
}

func TestPortableStopInstancesError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	err := c.StopInstances(ctx, []string{"i-nonexistent"})
	require.Error(t, err)
}

func TestPortableStartInstancesError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	err := c.StartInstances(ctx, []string{"i-nonexistent"})
	require.Error(t, err)
}

func TestPortableTerminateInstancesError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	err := c.TerminateInstances(ctx, []string{"i-nonexistent"})
	require.Error(t, err)
}

//nolint:dupl // test mock stubs are intentionally repetitive
func (mc *mockCompute) CreateVolume(_ context.Context, _ driver.VolumeConfig) (*driver.VolumeInfo, error) {
	return nil, nil
}

func (mc *mockCompute) DeleteVolume(_ context.Context, _ string) error { return nil }

func (mc *mockCompute) DescribeVolumes(_ context.Context, _ []string) ([]driver.VolumeInfo, error) {
	return nil, nil
}

func (mc *mockCompute) AttachVolume(_ context.Context, _, _, _ string) error { return nil }
func (mc *mockCompute) DetachVolume(_ context.Context, _ string) error       { return nil }

func (mc *mockCompute) CreateSnapshot(_ context.Context, _ driver.SnapshotConfig) (*driver.SnapshotInfo, error) {
	return nil, nil
}

func (mc *mockCompute) DeleteSnapshot(_ context.Context, _ string) error { return nil }

func (mc *mockCompute) DescribeSnapshots(_ context.Context, _ []string) ([]driver.SnapshotInfo, error) {
	return nil, nil
}

func (mc *mockCompute) CreateImage(_ context.Context, _ driver.ImageConfig) (*driver.ImageInfo, error) {
	return nil, nil
}

func (mc *mockCompute) DeregisterImage(_ context.Context, _ string) error { return nil }

func (mc *mockCompute) DescribeImages(_ context.Context, _ []string) ([]driver.ImageInfo, error) {
	return nil, nil
}
