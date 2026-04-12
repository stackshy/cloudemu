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
	volumes   map[string]*driver.VolumeInfo
	snapshots map[string]*driver.SnapshotInfo
	images    map[string]*driver.ImageInfo
	keyPairs  map[string]*driver.KeyPairInfo
	asgs      map[string]*driver.AutoScalingGroup
	policies  map[string]*driver.ScalingPolicy
	spots     map[string]*driver.SpotInstanceRequest
	templates map[string]*driver.LaunchTemplate
	counter   int
}

func newMockCompute() *mockCompute {
	return &mockCompute{
		instances: make(map[string]*driver.Instance),
		volumes:   make(map[string]*driver.VolumeInfo),
		snapshots: make(map[string]*driver.SnapshotInfo),
		images:    make(map[string]*driver.ImageInfo),
		keyPairs:  make(map[string]*driver.KeyPairInfo),
		asgs:      make(map[string]*driver.AutoScalingGroup),
		policies:  make(map[string]*driver.ScalingPolicy),
		spots:     make(map[string]*driver.SpotInstanceRequest),
		templates: make(map[string]*driver.LaunchTemplate),
	}
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
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.asgs[cfg.Name]; ok {
		return nil, fmt.Errorf("asg %s already exists", cfg.Name)
	}

	asg := &driver.AutoScalingGroup{
		Name:            cfg.Name,
		MinSize:         cfg.MinSize,
		MaxSize:         cfg.MaxSize,
		DesiredCapacity: cfg.DesiredCapacity,
		Status:          "Active",
	}
	m.asgs[cfg.Name] = asg

	result := *asg

	return &result, nil
}

func (m *mockCompute) DeleteAutoScalingGroup(_ context.Context, name string, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.asgs[name]; !ok {
		return fmt.Errorf("asg %s not found", name)
	}

	delete(m.asgs, name)

	return nil
}

func (m *mockCompute) GetAutoScalingGroup(_ context.Context, name string) (*driver.AutoScalingGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	asg, ok := m.asgs[name]
	if !ok {
		return nil, fmt.Errorf("asg %s not found", name)
	}

	result := *asg

	return &result, nil
}

func (m *mockCompute) ListAutoScalingGroups(_ context.Context) ([]driver.AutoScalingGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []driver.AutoScalingGroup
	for _, asg := range m.asgs {
		result = append(result, *asg)
	}

	return result, nil
}

func (m *mockCompute) UpdateAutoScalingGroup(_ context.Context, name string, desired, minSize, maxSize int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	asg, ok := m.asgs[name]
	if !ok {
		return fmt.Errorf("asg %s not found", name)
	}

	asg.DesiredCapacity = desired
	asg.MinSize = minSize
	asg.MaxSize = maxSize

	return nil
}

func (m *mockCompute) SetDesiredCapacity(_ context.Context, name string, desired int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	asg, ok := m.asgs[name]
	if !ok {
		return fmt.Errorf("asg %s not found", name)
	}

	asg.DesiredCapacity = desired

	return nil
}

// policyKey builds a map key for scaling policies.
func policyKey(asgName, policyName string) string {
	return asgName + "/" + policyName
}

func (m *mockCompute) PutScalingPolicy(_ context.Context, policy driver.ScalingPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.asgs[policy.AutoScalingGroup]; !ok {
		return fmt.Errorf("asg %s not found", policy.AutoScalingGroup)
	}

	p := policy
	m.policies[policyKey(policy.AutoScalingGroup, policy.Name)] = &p

	return nil
}

func (m *mockCompute) DeleteScalingPolicy(_ context.Context, asgName, policyName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := policyKey(asgName, policyName)
	if _, ok := m.policies[key]; !ok {
		return fmt.Errorf("policy %s not found", policyName)
	}

	delete(m.policies, key)

	return nil
}

func (m *mockCompute) ExecuteScalingPolicy(_ context.Context, asgName, policyName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := policyKey(asgName, policyName)
	if _, ok := m.policies[key]; !ok {
		return fmt.Errorf("policy %s not found", policyName)
	}

	return nil
}

func (m *mockCompute) RequestSpotInstances(_ context.Context, cfg driver.SpotRequestConfig) ([]driver.SpotInstanceRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := cfg.Count
	if count == 0 {
		count = 1
	}

	var result []driver.SpotInstanceRequest

	for range count {
		m.counter++
		id := fmt.Sprintf("sir-%06d", m.counter)

		req := &driver.SpotInstanceRequest{
			ID:       id,
			MaxPrice: cfg.MaxPrice,
			Status:   "open",
			Type:     cfg.Type,
		}
		m.spots[id] = req
		result = append(result, *req)
	}

	return result, nil
}

func (m *mockCompute) CancelSpotRequests(_ context.Context, ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, id := range ids {
		req, ok := m.spots[id]
		if !ok {
			return fmt.Errorf("spot request %s not found", id)
		}

		req.Status = "canceled"
	}

	return nil
}

func (m *mockCompute) DescribeSpotRequests(_ context.Context, ids []string) ([]driver.SpotInstanceRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []driver.SpotInstanceRequest

	if len(ids) == 0 {
		for _, req := range m.spots {
			result = append(result, *req)
		}

		return result, nil
	}

	for _, id := range ids {
		req, ok := m.spots[id]
		if !ok {
			return nil, fmt.Errorf("spot request %s not found", id)
		}

		result = append(result, *req)
	}

	return result, nil
}

func (m *mockCompute) CreateLaunchTemplate(_ context.Context, cfg driver.LaunchTemplateConfig) (*driver.LaunchTemplate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.templates[cfg.Name]; ok {
		return nil, fmt.Errorf("launch template %s already exists", cfg.Name)
	}

	m.counter++
	tmpl := &driver.LaunchTemplate{
		ID:      fmt.Sprintf("lt-%06d", m.counter),
		Name:    cfg.Name,
		Version: 1,
	}
	m.templates[cfg.Name] = tmpl

	result := *tmpl

	return &result, nil
}

func (m *mockCompute) DeleteLaunchTemplate(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.templates[name]; !ok {
		return fmt.Errorf("launch template %s not found", name)
	}

	delete(m.templates, name)

	return nil
}

func (m *mockCompute) GetLaunchTemplate(_ context.Context, name string) (*driver.LaunchTemplate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tmpl, ok := m.templates[name]
	if !ok {
		return nil, fmt.Errorf("launch template %s not found", name)
	}

	result := *tmpl

	return &result, nil
}

func (m *mockCompute) ListLaunchTemplates(_ context.Context) ([]driver.LaunchTemplate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []driver.LaunchTemplate
	for _, tmpl := range m.templates {
		result = append(result, *tmpl)
	}

	return result, nil
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
func (mc *mockCompute) CreateVolume(_ context.Context, cfg driver.VolumeConfig) (*driver.VolumeInfo, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.counter++
	id := fmt.Sprintf("vol-%06d", mc.counter)

	volType := cfg.VolumeType
	if volType == "" {
		volType = "gp3"
	}

	vol := &driver.VolumeInfo{
		ID: id, Size: cfg.Size, VolumeType: volType, State: "available",
		AvailabilityZone: cfg.AvailabilityZone, Tags: cfg.Tags,
	}
	mc.volumes[id] = vol

	result := *vol

	return &result, nil
}

func (mc *mockCompute) DeleteVolume(_ context.Context, id string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	vol, ok := mc.volumes[id]
	if !ok {
		return fmt.Errorf("volume %s not found", id)
	}

	if vol.State == "in-use" {
		return fmt.Errorf("volume %s is attached", id)
	}

	delete(mc.volumes, id)

	return nil
}

func (mc *mockCompute) DescribeVolumes(_ context.Context, ids []string) ([]driver.VolumeInfo, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	var result []driver.VolumeInfo

	if len(ids) == 0 {
		for _, v := range mc.volumes {
			result = append(result, *v)
		}

		return result, nil
	}

	for _, id := range ids {
		if v, ok := mc.volumes[id]; ok {
			result = append(result, *v)
		}
	}

	return result, nil
}

func (mc *mockCompute) AttachVolume(_ context.Context, volumeID, instanceID, device string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	vol, ok := mc.volumes[volumeID]
	if !ok {
		return fmt.Errorf("volume %s not found", volumeID)
	}

	if _, ok := mc.instances[instanceID]; !ok {
		return fmt.Errorf("instance %s not found", instanceID)
	}

	vol.State = "in-use"
	vol.AttachedTo = instanceID
	vol.Device = device

	return nil
}

func (mc *mockCompute) DetachVolume(_ context.Context, volumeID string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	vol, ok := mc.volumes[volumeID]
	if !ok {
		return fmt.Errorf("volume %s not found", volumeID)
	}

	if vol.State != "in-use" {
		return fmt.Errorf("volume %s is not attached", volumeID)
	}

	vol.State = "available"
	vol.AttachedTo = ""
	vol.Device = ""

	return nil
}

func (mc *mockCompute) CreateSnapshot(_ context.Context, cfg driver.SnapshotConfig) (*driver.SnapshotInfo, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	vol, ok := mc.volumes[cfg.VolumeID]
	if !ok {
		return nil, fmt.Errorf("volume %s not found", cfg.VolumeID)
	}

	mc.counter++
	id := fmt.Sprintf("snap-%06d", mc.counter)

	snap := &driver.SnapshotInfo{
		ID: id, VolumeID: cfg.VolumeID, State: "completed",
		Description: cfg.Description, Size: vol.Size, Tags: cfg.Tags,
	}
	mc.snapshots[id] = snap

	result := *snap

	return &result, nil
}

func (mc *mockCompute) DeleteSnapshot(_ context.Context, id string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if _, ok := mc.snapshots[id]; !ok {
		return fmt.Errorf("snapshot %s not found", id)
	}

	delete(mc.snapshots, id)

	return nil
}

func (mc *mockCompute) DescribeSnapshots(_ context.Context, ids []string) ([]driver.SnapshotInfo, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	var result []driver.SnapshotInfo

	if len(ids) == 0 {
		for _, s := range mc.snapshots {
			result = append(result, *s)
		}

		return result, nil
	}

	for _, id := range ids {
		if s, ok := mc.snapshots[id]; ok {
			result = append(result, *s)
		}
	}

	return result, nil
}

func (mc *mockCompute) CreateImage(_ context.Context, cfg driver.ImageConfig) (*driver.ImageInfo, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if _, ok := mc.instances[cfg.InstanceID]; !ok {
		return nil, fmt.Errorf("instance %s not found", cfg.InstanceID)
	}

	mc.counter++
	id := fmt.Sprintf("img-%06d", mc.counter)

	img := &driver.ImageInfo{
		ID: id, Name: cfg.Name, State: "available",
		Description: cfg.Description, Tags: cfg.Tags,
	}
	mc.images[id] = img

	result := *img

	return &result, nil
}

func (mc *mockCompute) DeregisterImage(_ context.Context, id string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if _, ok := mc.images[id]; !ok {
		return fmt.Errorf("image %s not found", id)
	}

	delete(mc.images, id)

	return nil
}

func (mc *mockCompute) DescribeImages(_ context.Context, ids []string) ([]driver.ImageInfo, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	var result []driver.ImageInfo

	if len(ids) == 0 {
		for _, img := range mc.images {
			result = append(result, *img)
		}

		return result, nil
	}

	for _, id := range ids {
		if img, ok := mc.images[id]; ok {
			result = append(result, *img)
		}
	}

	return result, nil
}

// =====================================================================
// Volume Portable Tests
// =====================================================================

func TestCreateVolumePortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	vol, err := c.CreateVolume(ctx, driver.VolumeConfig{Size: 100})
	require.NoError(t, err)
	assert.NotEmpty(t, vol.ID)
	assert.Equal(t, 100, vol.Size)
	assert.Equal(t, "available", vol.State)
}

func TestCreateVolumePortableError(t *testing.T) {
	inj := inject.NewInjector()
	c := newTestCompute(WithErrorInjection(inj))
	ctx := context.Background()

	inj.Set("compute", "CreateVolume", fmt.Errorf("injected"), inject.Always{})

	_, err := c.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
	require.Error(t, err)
}

func TestDeleteVolumePortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	vol, err := c.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
	require.NoError(t, err)

	err = c.DeleteVolume(ctx, vol.ID)
	require.NoError(t, err)
}

func TestDeleteVolumePortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	err := c.DeleteVolume(ctx, "vol-nonexistent")
	require.Error(t, err)
}

func TestDescribeVolumesPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	vol, err := c.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
	require.NoError(t, err)

	vols, err := c.DescribeVolumes(ctx, []string{vol.ID})
	require.NoError(t, err)
	assert.Equal(t, 1, len(vols))
	assert.Equal(t, vol.ID, vols[0].ID)
}

func TestAttachVolumePortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	instances, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID: "ami-12345", InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)

	vol, err := c.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
	require.NoError(t, err)

	err = c.AttachVolume(ctx, vol.ID, instances[0].ID, "/dev/sdf")
	require.NoError(t, err)

	// Verify state
	vols, err := c.DescribeVolumes(ctx, []string{vol.ID})
	require.NoError(t, err)
	assert.Equal(t, "in-use", vols[0].State)
}

func TestAttachVolumePortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	err := c.AttachVolume(ctx, "vol-nonexistent", "i-nonexistent", "/dev/sdf")
	require.Error(t, err)
}

func TestDetachVolumePortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	instances, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID: "ami-12345", InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)

	vol, err := c.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
	require.NoError(t, err)

	err = c.AttachVolume(ctx, vol.ID, instances[0].ID, "/dev/sdf")
	require.NoError(t, err)

	err = c.DetachVolume(ctx, vol.ID)
	require.NoError(t, err)

	// Verify state
	vols, err := c.DescribeVolumes(ctx, []string{vol.ID})
	require.NoError(t, err)
	assert.Equal(t, "available", vols[0].State)
}

func TestDetachVolumePortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	vol, err := c.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
	require.NoError(t, err)

	err = c.DetachVolume(ctx, vol.ID)
	require.Error(t, err)
}

// =====================================================================
// Snapshot Portable Tests
// =====================================================================

func TestCreateSnapshotPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	vol, err := c.CreateVolume(ctx, driver.VolumeConfig{Size: 50})
	require.NoError(t, err)

	snap, err := c.CreateSnapshot(ctx, driver.SnapshotConfig{
		VolumeID:    vol.ID,
		Description: "test snapshot",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, snap.ID)
	assert.Equal(t, vol.ID, snap.VolumeID)
	assert.Equal(t, "completed", snap.State)
	assert.Equal(t, 50, snap.Size)
}

func TestCreateSnapshotPortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.CreateSnapshot(ctx, driver.SnapshotConfig{VolumeID: "vol-nonexistent"})
	require.Error(t, err)
}

func TestDeleteSnapshotPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	vol, err := c.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
	require.NoError(t, err)

	snap, err := c.CreateSnapshot(ctx, driver.SnapshotConfig{VolumeID: vol.ID})
	require.NoError(t, err)

	err = c.DeleteSnapshot(ctx, snap.ID)
	require.NoError(t, err)
}

func TestDeleteSnapshotPortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	err := c.DeleteSnapshot(ctx, "snap-nonexistent")
	require.Error(t, err)
}

func TestDescribeSnapshotsPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	vol, err := c.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
	require.NoError(t, err)

	snap, err := c.CreateSnapshot(ctx, driver.SnapshotConfig{VolumeID: vol.ID})
	require.NoError(t, err)

	snaps, err := c.DescribeSnapshots(ctx, []string{snap.ID})
	require.NoError(t, err)
	assert.Equal(t, 1, len(snaps))
	assert.Equal(t, snap.ID, snaps[0].ID)
}

// =====================================================================
// Image Portable Tests
// =====================================================================

func TestCreateImagePortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	instances, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID: "ami-12345", InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)

	img, err := c.CreateImage(ctx, driver.ImageConfig{
		InstanceID: instances[0].ID,
		Name:       "test-image",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, img.ID)
	assert.Equal(t, "test-image", img.Name)
	assert.Equal(t, "available", img.State)
}

func TestCreateImagePortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.CreateImage(ctx, driver.ImageConfig{InstanceID: "i-nonexistent"})
	require.Error(t, err)
}

func TestDeregisterImagePortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	instances, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID: "ami-12345", InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)

	img, err := c.CreateImage(ctx, driver.ImageConfig{
		InstanceID: instances[0].ID,
		Name:       "del-image",
	})
	require.NoError(t, err)

	err = c.DeregisterImage(ctx, img.ID)
	require.NoError(t, err)
}

func TestDeregisterImagePortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	err := c.DeregisterImage(ctx, "img-nonexistent")
	require.Error(t, err)
}

func TestDescribeImagesPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	instances, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID: "ami-12345", InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)

	img, err := c.CreateImage(ctx, driver.ImageConfig{
		InstanceID: instances[0].ID,
		Name:       "desc-image",
	})
	require.NoError(t, err)

	imgs, err := c.DescribeImages(ctx, []string{img.ID})
	require.NoError(t, err)
	assert.Equal(t, 1, len(imgs))
	assert.Equal(t, img.ID, imgs[0].ID)
}

// =====================================================================
// ModifyInstance Portable Tests
// =====================================================================

func TestModifyInstancePortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	instances, err := c.RunInstances(ctx, driver.InstanceConfig{
		ImageID: "ami-12345", InstanceType: "t2.micro",
	}, 1)
	require.NoError(t, err)

	err = c.ModifyInstance(ctx, instances[0].ID, driver.ModifyInstanceInput{
		InstanceType: "t2.large",
	})
	require.NoError(t, err)

	described, err := c.DescribeInstances(ctx, []string{instances[0].ID}, nil)
	require.NoError(t, err)
	assert.Equal(t, "t2.large", described[0].InstanceType)
}

func TestModifyInstancePortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	err := c.ModifyInstance(ctx, "i-nonexistent", driver.ModifyInstanceInput{
		InstanceType: "t2.large",
	})
	require.Error(t, err)
}

// =====================================================================
// Auto Scaling Group Portable Tests
// =====================================================================

func TestCreateAutoScalingGroupPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	asg, err := c.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name:            "test-asg",
		MinSize:         1,
		MaxSize:         5,
		DesiredCapacity: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, "test-asg", asg.Name)
	assert.Equal(t, 2, asg.DesiredCapacity)
	assert.Equal(t, "Active", asg.Status)
}

func TestCreateAutoScalingGroupPortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "dup-asg",
	})
	require.NoError(t, err)

	_, err = c.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "dup-asg",
	})
	require.Error(t, err)
}

func TestDeleteAutoScalingGroupPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "del-asg",
	})
	require.NoError(t, err)

	err = c.DeleteAutoScalingGroup(ctx, "del-asg", false)
	require.NoError(t, err)
}

func TestDeleteAutoScalingGroupPortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	err := c.DeleteAutoScalingGroup(ctx, "nonexistent-asg", false)
	require.Error(t, err)
}

func TestGetAutoScalingGroupPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name:            "get-asg",
		DesiredCapacity: 3,
	})
	require.NoError(t, err)

	asg, err := c.GetAutoScalingGroup(ctx, "get-asg")
	require.NoError(t, err)
	assert.Equal(t, "get-asg", asg.Name)
	assert.Equal(t, 3, asg.DesiredCapacity)
}

func TestGetAutoScalingGroupPortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.GetAutoScalingGroup(ctx, "nonexistent-asg")
	require.Error(t, err)
}

func TestListAutoScalingGroupsPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{Name: "asg-a"})
	require.NoError(t, err)

	_, err = c.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{Name: "asg-b"})
	require.NoError(t, err)

	asgs, err := c.ListAutoScalingGroups(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(asgs))
}

func TestListAutoScalingGroupsPortableEmpty(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	asgs, err := c.ListAutoScalingGroups(ctx)
	require.NoError(t, err)
	assert.Empty(t, asgs)
}

func TestUpdateAutoScalingGroupPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name:            "upd-asg",
		MinSize:         1,
		MaxSize:         5,
		DesiredCapacity: 2,
	})
	require.NoError(t, err)

	err = c.UpdateAutoScalingGroup(ctx, "upd-asg", 4, 2, 10)
	require.NoError(t, err)

	asg, err := c.GetAutoScalingGroup(ctx, "upd-asg")
	require.NoError(t, err)
	assert.Equal(t, 4, asg.DesiredCapacity)
	assert.Equal(t, 2, asg.MinSize)
	assert.Equal(t, 10, asg.MaxSize)
}

func TestUpdateAutoScalingGroupPortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	err := c.UpdateAutoScalingGroup(ctx, "nonexistent-asg", 1, 1, 5)
	require.Error(t, err)
}

func TestSetDesiredCapacityPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name:            "cap-asg",
		DesiredCapacity: 1,
	})
	require.NoError(t, err)

	err = c.SetDesiredCapacity(ctx, "cap-asg", 5)
	require.NoError(t, err)

	asg, err := c.GetAutoScalingGroup(ctx, "cap-asg")
	require.NoError(t, err)
	assert.Equal(t, 5, asg.DesiredCapacity)
}

func TestSetDesiredCapacityPortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	err := c.SetDesiredCapacity(ctx, "nonexistent-asg", 3)
	require.Error(t, err)
}

// =====================================================================
// Scaling Policy Portable Tests
// =====================================================================

func TestPutScalingPolicyPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "policy-asg",
	})
	require.NoError(t, err)

	err = c.PutScalingPolicy(ctx, driver.ScalingPolicy{
		Name:             "scale-up",
		AutoScalingGroup: "policy-asg",
		PolicyType:       "SimpleScaling",
	})
	require.NoError(t, err)
}

func TestPutScalingPolicyPortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	err := c.PutScalingPolicy(ctx, driver.ScalingPolicy{
		Name:             "scale-up",
		AutoScalingGroup: "nonexistent-asg",
	})
	require.Error(t, err)
}

func TestDeleteScalingPolicyPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "delpol-asg",
	})
	require.NoError(t, err)

	err = c.PutScalingPolicy(ctx, driver.ScalingPolicy{
		Name:             "scale-down",
		AutoScalingGroup: "delpol-asg",
	})
	require.NoError(t, err)

	err = c.DeleteScalingPolicy(ctx, "delpol-asg", "scale-down")
	require.NoError(t, err)
}

func TestDeleteScalingPolicyPortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	err := c.DeleteScalingPolicy(ctx, "nonexistent-asg", "nonexistent-policy")
	require.Error(t, err)
}

func TestExecuteScalingPolicyPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "exec-asg",
	})
	require.NoError(t, err)

	err = c.PutScalingPolicy(ctx, driver.ScalingPolicy{
		Name:             "scale-out",
		AutoScalingGroup: "exec-asg",
	})
	require.NoError(t, err)

	err = c.ExecuteScalingPolicy(ctx, "exec-asg", "scale-out")
	require.NoError(t, err)
}

func TestExecuteScalingPolicyPortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	err := c.ExecuteScalingPolicy(ctx, "nonexistent-asg", "nonexistent-policy")
	require.Error(t, err)
}

// =====================================================================
// Spot Instance Portable Tests
// =====================================================================

func TestRequestSpotInstancesPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	spots, err := c.RequestSpotInstances(ctx, driver.SpotRequestConfig{
		MaxPrice: 0.05,
		Count:    2,
		Type:     "one-time",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, len(spots))
	assert.Equal(t, "open", spots[0].Status)
	assert.Equal(t, "open", spots[1].Status)
	assert.NotEqual(t, spots[0].ID, spots[1].ID)
}

func TestRequestSpotInstancesPortableDefaultCount(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	spots, err := c.RequestSpotInstances(ctx, driver.SpotRequestConfig{
		MaxPrice: 0.10,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, len(spots))
}

func TestCancelSpotRequestsPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	spots, err := c.RequestSpotInstances(ctx, driver.SpotRequestConfig{
		MaxPrice: 0.05,
		Count:    1,
	})
	require.NoError(t, err)

	err = c.CancelSpotRequests(ctx, []string{spots[0].ID})
	require.NoError(t, err)

	described, err := c.DescribeSpotRequests(ctx, []string{spots[0].ID})
	require.NoError(t, err)
	assert.Equal(t, "canceled", described[0].Status)
}

func TestCancelSpotRequestsPortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	err := c.CancelSpotRequests(ctx, []string{"sir-nonexistent"})
	require.Error(t, err)
}

func TestDescribeSpotRequestsPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	spots, err := c.RequestSpotInstances(ctx, driver.SpotRequestConfig{
		MaxPrice: 0.05,
		Count:    1,
	})
	require.NoError(t, err)

	described, err := c.DescribeSpotRequests(ctx, []string{spots[0].ID})
	require.NoError(t, err)
	assert.Equal(t, 1, len(described))
	assert.Equal(t, spots[0].ID, described[0].ID)
}

func TestDescribeSpotRequestsPortableAll(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.RequestSpotInstances(ctx, driver.SpotRequestConfig{
		MaxPrice: 0.05,
		Count:    3,
	})
	require.NoError(t, err)

	described, err := c.DescribeSpotRequests(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, len(described))
}

func TestDescribeSpotRequestsPortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.DescribeSpotRequests(ctx, []string{"sir-nonexistent"})
	require.Error(t, err)
}

// =====================================================================
// Launch Template Portable Tests
// =====================================================================

func TestCreateLaunchTemplatePortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	tmpl, err := c.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
		Name: "test-template",
		InstanceConfig: driver.InstanceConfig{
			ImageID:      "ami-12345",
			InstanceType: "t2.micro",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "test-template", tmpl.Name)
	assert.NotEmpty(t, tmpl.ID)
	assert.Equal(t, 1, tmpl.Version)
}

func TestCreateLaunchTemplatePortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
		Name: "dup-template",
	})
	require.NoError(t, err)

	_, err = c.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
		Name: "dup-template",
	})
	require.Error(t, err)
}

func TestDeleteLaunchTemplatePortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
		Name: "del-template",
	})
	require.NoError(t, err)

	err = c.DeleteLaunchTemplate(ctx, "del-template")
	require.NoError(t, err)
}

func TestDeleteLaunchTemplatePortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	err := c.DeleteLaunchTemplate(ctx, "nonexistent-template")
	require.Error(t, err)
}

func TestGetLaunchTemplatePortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
		Name: "get-template",
	})
	require.NoError(t, err)

	tmpl, err := c.GetLaunchTemplate(ctx, "get-template")
	require.NoError(t, err)
	assert.Equal(t, "get-template", tmpl.Name)
}

func TestGetLaunchTemplatePortableError(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.GetLaunchTemplate(ctx, "nonexistent-template")
	require.Error(t, err)
}

func TestListLaunchTemplatesPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{Name: "tmpl-a"})
	require.NoError(t, err)

	_, err = c.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{Name: "tmpl-b"})
	require.NoError(t, err)

	templates, err := c.ListLaunchTemplates(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(templates))
}

func TestListLaunchTemplatesPortableEmpty(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	templates, err := c.ListLaunchTemplates(ctx)
	require.NoError(t, err)
	assert.Empty(t, templates)
}

// =====================================================================
// Error Injection Tests for List/Describe methods (cover error return paths)
// =====================================================================

func TestListAutoScalingGroupsPortableError(t *testing.T) {
	inj := inject.NewInjector()
	c := newTestCompute(WithErrorInjection(inj))
	ctx := context.Background()

	inj.Set("compute", "ListAutoScalingGroups", fmt.Errorf("injected"), inject.Always{})

	_, err := c.ListAutoScalingGroups(ctx)
	require.Error(t, err)
}

func TestRequestSpotInstancesPortableError(t *testing.T) {
	inj := inject.NewInjector()
	c := newTestCompute(WithErrorInjection(inj))
	ctx := context.Background()

	inj.Set("compute", "RequestSpotInstances", fmt.Errorf("injected"), inject.Always{})

	_, err := c.RequestSpotInstances(ctx, driver.SpotRequestConfig{MaxPrice: 0.05})
	require.Error(t, err)
}

func TestDescribeSpotRequestsPortableInjectedError(t *testing.T) {
	inj := inject.NewInjector()
	c := newTestCompute(WithErrorInjection(inj))
	ctx := context.Background()

	inj.Set("compute", "DescribeSpotRequests", fmt.Errorf("injected"), inject.Always{})

	_, err := c.DescribeSpotRequests(ctx, nil)
	require.Error(t, err)
}

func TestListLaunchTemplatesPortableError(t *testing.T) {
	inj := inject.NewInjector()
	c := newTestCompute(WithErrorInjection(inj))
	ctx := context.Background()

	inj.Set("compute", "ListLaunchTemplates", fmt.Errorf("injected"), inject.Always{})

	_, err := c.ListLaunchTemplates(ctx)
	require.Error(t, err)
}

func TestDescribeVolumesPortableError(t *testing.T) {
	inj := inject.NewInjector()
	c := newTestCompute(WithErrorInjection(inj))
	ctx := context.Background()

	inj.Set("compute", "DescribeVolumes", fmt.Errorf("injected"), inject.Always{})

	_, err := c.DescribeVolumes(ctx, nil)
	require.Error(t, err)
}

func TestDescribeSnapshotsPortableError(t *testing.T) {
	inj := inject.NewInjector()
	c := newTestCompute(WithErrorInjection(inj))
	ctx := context.Background()

	inj.Set("compute", "DescribeSnapshots", fmt.Errorf("injected"), inject.Always{})

	_, err := c.DescribeSnapshots(ctx, nil)
	require.Error(t, err)
}

func TestDescribeImagesPortableError(t *testing.T) {
	inj := inject.NewInjector()
	c := newTestCompute(WithErrorInjection(inj))
	ctx := context.Background()

	inj.Set("compute", "DescribeImages", fmt.Errorf("injected"), inject.Always{})

	_, err := c.DescribeImages(ctx, nil)
	require.Error(t, err)
}

// =====================================================================
// Key Pair Mock Methods
// =====================================================================

func (mc *mockCompute) CreateKeyPair(_ context.Context, cfg driver.KeyPairConfig) (*driver.KeyPairInfo, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if cfg.Name == "" {
		return nil, fmt.Errorf("key pair name must not be empty")
	}

	if _, ok := mc.keyPairs[cfg.Name]; ok {
		return nil, fmt.Errorf("key pair %s already exists", cfg.Name)
	}

	keyType := cfg.KeyType
	if keyType == "" {
		keyType = "rsa"
	}

	mc.counter++
	kp := &driver.KeyPairInfo{
		ID:          fmt.Sprintf("kp-%06d", mc.counter),
		Name:        cfg.Name,
		Fingerprint: "fp-" + cfg.Name,
		KeyType:     keyType,
		PublicKey:    "mock-public-key-" + cfg.Name,
		PrivateKey:  "mock-private-key-" + cfg.Name,
		Tags:        cfg.Tags,
	}
	mc.keyPairs[cfg.Name] = kp

	result := *kp

	return &result, nil
}

func (mc *mockCompute) DeleteKeyPair(_ context.Context, name string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if _, ok := mc.keyPairs[name]; !ok {
		return fmt.Errorf("key pair %s not found", name)
	}

	delete(mc.keyPairs, name)

	return nil
}

func (mc *mockCompute) DescribeKeyPairs(_ context.Context, names []string) ([]driver.KeyPairInfo, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	var result []driver.KeyPairInfo

	if len(names) == 0 {
		for _, kp := range mc.keyPairs {
			cp := *kp
			cp.PrivateKey = ""
			result = append(result, cp)
		}

		return result, nil
	}

	for _, name := range names {
		if kp, ok := mc.keyPairs[name]; ok {
			cp := *kp
			cp.PrivateKey = ""
			result = append(result, cp)
		}
	}

	return result, nil
}

// =====================================================================
// Key Pair Portable Tests
// =====================================================================

func TestCreateKeyPairPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	kp, err := c.CreateKeyPair(ctx, driver.KeyPairConfig{Name: "my-key", KeyType: "ed25519"})
	require.NoError(t, err)
	assert.NotEmpty(t, kp.ID)
	assert.Equal(t, "my-key", kp.Name)
	assert.Equal(t, "ed25519", kp.KeyType)
	assert.NotEmpty(t, kp.PrivateKey)
}

func TestCreateKeyPairPortableError(t *testing.T) {
	inj := inject.NewInjector()
	c := newTestCompute(WithErrorInjection(inj))
	ctx := context.Background()

	inj.Set("compute", "CreateKeyPair", fmt.Errorf("injected"), inject.Always{})

	_, err := c.CreateKeyPair(ctx, driver.KeyPairConfig{Name: "fail-key"})
	require.Error(t, err)
}

func TestDeleteKeyPairPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.CreateKeyPair(ctx, driver.KeyPairConfig{Name: "del-key"})
	require.NoError(t, err)

	err = c.DeleteKeyPair(ctx, "del-key")
	require.NoError(t, err)

	err = c.DeleteKeyPair(ctx, "del-key")
	require.Error(t, err)
}

func TestDescribeKeyPairsPortable(t *testing.T) {
	c := newTestCompute()
	ctx := context.Background()

	_, err := c.CreateKeyPair(ctx, driver.KeyPairConfig{Name: "kp-a"})
	require.NoError(t, err)

	_, err = c.CreateKeyPair(ctx, driver.KeyPairConfig{Name: "kp-b"})
	require.NoError(t, err)

	all, err := c.DescribeKeyPairs(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, len(all))

	for _, kp := range all {
		assert.Empty(t, kp.PrivateKey, "DescribeKeyPairs should not return PrivateKey")
	}

	filtered, err := c.DescribeKeyPairs(ctx, []string{"kp-a"})
	require.NoError(t, err)
	assert.Equal(t, 1, len(filtered))
	assert.Equal(t, "kp-a", filtered[0].Name)
}

func TestDescribeKeyPairsPortableError(t *testing.T) {
	inj := inject.NewInjector()
	c := newTestCompute(WithErrorInjection(inj))
	ctx := context.Background()

	inj.Set("compute", "DescribeKeyPairs", fmt.Errorf("injected"), inject.Always{})

	_, err := c.DescribeKeyPairs(ctx, nil)
	require.Error(t, err)
}
