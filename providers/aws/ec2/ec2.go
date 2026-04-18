package ec2

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/stackshy/cloudemu/compute"
	"github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stackshy/cloudemu/statemachine"
)

var _ driver.Compute = (*Mock)(nil)

const (
	ipSegmentSize  = 256
	stateAvailable = "available"
	stateInUse     = "in-use"
)

type lifecycleTransition struct {
	intermediateState string
	finalState        string
	metricValues      []float64
	errVerb           string
}

var (
	runningMetricValues = []float64{25.0, 1024.0, 512.0, 100.0, 50.0} //nolint:gochecknoglobals // package-level test fixtures
	zeroMetricValues    = []float64{0.0, 0.0, 0.0, 0.0, 0.0}          //nolint:gochecknoglobals // package-level test fixtures

	startTransition = lifecycleTransition{ //nolint:gochecknoglobals // package-level config
		intermediateState: compute.StatePending,
		finalState:        compute.StateRunning,
		metricValues:      runningMetricValues,
		errVerb:           "start",
	}
	stopTransition = lifecycleTransition{ //nolint:gochecknoglobals // package-level config
		intermediateState: compute.StateStopping,
		finalState:        compute.StateStopped,
		metricValues:      zeroMetricValues,
		errVerb:           "stop",
	}
	rebootTransition = lifecycleTransition{ //nolint:gochecknoglobals // package-level config
		intermediateState: compute.StateRestarting,
		finalState:        compute.StateRunning,
		metricValues:      runningMetricValues,
		errVerb:           "reboot",
	}
	terminateTransition = lifecycleTransition{ //nolint:gochecknoglobals // package-level config
		intermediateState: compute.StateShuttingDown,
		finalState:        compute.StateTerminated,
		metricValues:      zeroMetricValues,
		errVerb:           "terminate",
	}
)

type instanceData struct {
	ID             string
	ImageID        string
	InstanceType   string
	State          string
	PrivateIP      string
	PublicIP       string
	SubnetID       string
	VPCID          string
	SecurityGroups []string
	Tags           map[string]string
	LaunchTime     string
}

type asgData struct {
	config   driver.AutoScalingGroup
	policies *memstore.Store[driver.ScalingPolicy]
}

// Mock is an in-memory mock implementation of the AWS EC2 service.
type Mock struct {
	instances    *memstore.Store[*instanceData]
	asgs         *memstore.Store[*asgData]
	spotRequests *memstore.Store[*driver.SpotInstanceRequest]
	templates    *memstore.Store[*driver.LaunchTemplate]
	volumes      *memstore.Store[*driver.VolumeInfo]
	snapshots    *memstore.Store[*driver.SnapshotInfo]
	images       *memstore.Store[*driver.ImageInfo]
	keyPairs     *memstore.Store[*driver.KeyPairInfo]
	sm           *statemachine.Machine
	opts         *config.Options
	ipCounter    atomic.Int64
	volCounter   atomic.Int64
	snapCounter  atomic.Int64
	amiCounter   atomic.Int64
	monitoring   mondriver.Monitoring
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) emitInstanceMetrics(ctx context.Context, instanceID, launchTime string) {
	if m.monitoring == nil {
		return
	}

	lt, err := time.Parse("2006-01-02T15:04:05Z", launchTime)
	if err != nil {
		lt = m.opts.Clock.Now()
	}

	metrics := []string{"CPUUtilization", "NetworkIn", "NetworkOut", "DiskReadOps", "DiskWriteOps"}
	values := []float64{25.0, 1024.0, 512.0, 100.0, 50.0}

	var data []mondriver.MetricDatum

	for i, metricName := range metrics {
		for j := 0; j < 5; j++ {
			ts := lt.Add(time.Duration(j) * time.Minute)
			data = append(data, mondriver.MetricDatum{
				Namespace:  "AWS/EC2",
				MetricName: metricName,
				Value:      values[i],
				Unit:       "None",
				Dimensions: map[string]string{"InstanceId": instanceID},
				Timestamp:  ts,
			})
		}
	}

	_ = m.monitoring.PutMetricData(ctx, data)
}

func (m *Mock) emitLifecycleMetrics(ctx context.Context, instanceID string, values []float64) {
	if m.monitoring == nil {
		return
	}

	metrics := []string{"CPUUtilization", "NetworkIn", "NetworkOut", "DiskReadOps", "DiskWriteOps"}
	now := m.opts.Clock.Now()
	data := make([]mondriver.MetricDatum, len(metrics))

	for i, metricName := range metrics {
		data[i] = mondriver.MetricDatum{
			Namespace:  "AWS/EC2",
			MetricName: metricName,
			Value:      values[i],
			Unit:       "None",
			Dimensions: map[string]string{"InstanceId": instanceID},
			Timestamp:  now,
		}
	}

	_ = m.monitoring.PutMetricData(ctx, data)
}

// New creates a new EC2 mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		instances:    memstore.New[*instanceData](),
		asgs:         memstore.New[*asgData](),
		spotRequests: memstore.New[*driver.SpotInstanceRequest](),
		templates:    memstore.New[*driver.LaunchTemplate](),
		volumes:      memstore.New[*driver.VolumeInfo](),
		snapshots:    memstore.New[*driver.SnapshotInfo](),
		images:       memstore.New[*driver.ImageInfo](),
		keyPairs:     memstore.New[*driver.KeyPairInfo](),
		sm:           statemachine.New(compute.VMTransitions()),
		opts:         opts,
	}
}

func (m *Mock) nextIP() string {
	n := m.ipCounter.Add(1)
	return fmt.Sprintf("10.0.%d.%d", n/ipSegmentSize, n%ipSegmentSize)
}

func toInstance(d *instanceData) driver.Instance {
	sg := make([]string, len(d.SecurityGroups))
	copy(sg, d.SecurityGroups)

	tags := make(map[string]string, len(d.Tags))

	for k, v := range d.Tags {
		tags[k] = v
	}

	return driver.Instance{
		ID: d.ID, ImageID: d.ImageID, InstanceType: d.InstanceType, State: d.State,
		PrivateIP: d.PrivateIP, PublicIP: d.PublicIP, SubnetID: d.SubnetID, VPCID: d.VPCID,
		SecurityGroups: sg, Tags: tags, LaunchTime: d.LaunchTime,
	}
}

//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) RunInstances(ctx context.Context, cfg driver.InstanceConfig, count int) ([]driver.Instance, error) {
	if count <= 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "count must be greater than 0")
	}

	results := make([]driver.Instance, 0, count)

	for i := 0; i < count; i++ {
		id := idgen.GenerateID("i-")

		tags := make(map[string]string, len(cfg.Tags))

		for k, v := range cfg.Tags {
			tags[k] = v
		}

		sg := make([]string, len(cfg.SecurityGroups))
		copy(sg, cfg.SecurityGroups)

		inst := &instanceData{
			ID: id, ImageID: cfg.ImageID, InstanceType: cfg.InstanceType,
			State: compute.StatePending, PrivateIP: m.nextIP(), SubnetID: cfg.SubnetID,
			SecurityGroups: sg, Tags: tags,
			LaunchTime: m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		}
		m.instances.Set(id, inst)
		m.sm.SetState(id, compute.StatePending)
		_ = m.sm.Transition(id, compute.StateRunning)
		inst.State = compute.StateRunning
		results = append(results, toInstance(inst))
		m.emitInstanceMetrics(ctx, id, inst.LaunchTime)
	}

	return results, nil
}

func (m *Mock) transitionInstances(ctx context.Context, instanceIDs []string, t lifecycleTransition) error {
	for _, id := range instanceIDs {
		inst, ok := m.instances.Get(id)
		if !ok {
			return cerrors.Newf(cerrors.NotFound, "instance %q not found", id)
		}

		if err := m.sm.Transition(id, t.intermediateState); err != nil {
			return cerrors.Newf(cerrors.FailedPrecondition, "cannot %s instance %q: %v", t.errVerb, id, err)
		}

		inst.State = t.intermediateState
		_ = m.sm.Transition(id, t.finalState)
		inst.State = t.finalState

		m.emitLifecycleMetrics(ctx, id, t.metricValues)
	}

	return nil
}

func (m *Mock) StartInstances(ctx context.Context, instanceIDs []string) error {
	return m.transitionInstances(ctx, instanceIDs, startTransition)
}

func (m *Mock) StopInstances(ctx context.Context, instanceIDs []string) error {
	return m.transitionInstances(ctx, instanceIDs, stopTransition)
}

func (m *Mock) RebootInstances(ctx context.Context, instanceIDs []string) error {
	return m.transitionInstances(ctx, instanceIDs, rebootTransition)
}

func (m *Mock) TerminateInstances(ctx context.Context, instanceIDs []string) error {
	return m.transitionInstances(ctx, instanceIDs, terminateTransition)
}

func (m *Mock) DescribeInstances(_ context.Context, instanceIDs []string, filters []driver.DescribeFilter) ([]driver.Instance, error) {
	var candidates []*instanceData

	if len(instanceIDs) > 0 {
		for _, id := range instanceIDs {
			if inst, ok := m.instances.Get(id); ok {
				candidates = append(candidates, inst)
			}
		}
	} else {
		for _, inst := range m.instances.All() {
			candidates = append(candidates, inst)
		}
	}

	results := make([]driver.Instance, 0)

	for _, inst := range candidates {
		if matchesFilters(inst, filters) {
			results = append(results, toInstance(inst))
		}
	}

	return results, nil
}

func matchesFilters(inst *instanceData, filters []driver.DescribeFilter) bool {
	for _, f := range filters {
		if !matchesSingleFilter(inst, f) {
			return false
		}
	}

	return true
}

func matchesSingleFilter(inst *instanceData, f driver.DescribeFilter) bool {
	switch f.Name {
	case "instance-id":
		return containsValue(f.Values, inst.ID)
	case "instance-type":
		return containsValue(f.Values, inst.InstanceType)
	case "instance-state-name":
		return containsValue(f.Values, inst.State)
	default:
		return matchesTagFilter(inst, f)
	}
}

func matchesTagFilter(inst *instanceData, f driver.DescribeFilter) bool {
	if len(f.Name) > 4 && f.Name[:4] == "tag:" {
		tagKey := f.Name[4:]

		tagVal, ok := inst.Tags[tagKey]
		if !ok || !containsValue(f.Values, tagVal) {
			return false
		}
	}

	return true
}

func containsValue(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}

	return false
}

func (m *Mock) ModifyInstance(_ context.Context, instanceID string, input driver.ModifyInstanceInput) error {
	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	if inst.State != compute.StateStopped {
		return cerrors.Newf(cerrors.FailedPrecondition, "instance %q must be stopped to modify", instanceID)
	}

	if input.InstanceType != "" {
		inst.InstanceType = input.InstanceType
	}

	if input.Tags != nil {
		for k, v := range input.Tags {
			inst.Tags[k] = v
		}
	}

	return nil
}

// SetInstanceVPC sets the VPC ID on an existing instance. This is a test
// helper since RunInstances does not automatically resolve VPC from subnet.
func (m *Mock) SetInstanceVPC(instanceID, vpcID string) error {
	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	inst.VPCID = vpcID

	return nil
}

// CreateVolume creates a new EBS volume.
func (m *Mock) CreateVolume(_ context.Context, cfg driver.VolumeConfig) (*driver.VolumeInfo, error) {
	id := fmt.Sprintf("vol-%012d", m.volCounter.Add(1))

	volType := cfg.VolumeType
	if volType == "" {
		volType = "gp3"
	}

	az := cfg.AvailabilityZone
	if az == "" {
		az = m.opts.Region + "a"
	}

	vol := &driver.VolumeInfo{
		ID:               id,
		Size:             cfg.Size,
		VolumeType:       volType,
		State:            stateAvailable,
		AvailabilityZone: az,
		CreatedAt:        m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Tags:             copyTags(cfg.Tags),
	}

	m.volumes.Set(id, vol)

	result := *vol

	return &result, nil
}

// DeleteVolume deletes an EBS volume.
func (m *Mock) DeleteVolume(_ context.Context, id string) error {
	vol, ok := m.volumes.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "volume %q not found", id)
	}

	if vol.State == stateInUse {
		return cerrors.Newf(cerrors.FailedPrecondition, "volume %q is attached", id)
	}

	m.volumes.Delete(id)

	return nil
}

// DescribeVolumes returns volumes matching the given IDs.
func (m *Mock) DescribeVolumes(_ context.Context, ids []string) ([]driver.VolumeInfo, error) {
	return describeResources(m.volumes, ids), nil
}

// AttachVolume attaches a volume to an instance.
func (m *Mock) AttachVolume(_ context.Context, volumeID, instanceID, device string) error {
	vol, ok := m.volumes.Get(volumeID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "volume %q not found", volumeID)
	}

	if vol.State == stateInUse {
		return cerrors.Newf(cerrors.FailedPrecondition, "volume %q already attached", volumeID)
	}

	if _, ok := m.instances.Get(instanceID); !ok {
		return cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	vol.State = stateInUse
	vol.AttachedTo = instanceID
	vol.Device = device

	return nil
}

// DetachVolume detaches a volume from an instance.
func (m *Mock) DetachVolume(_ context.Context, volumeID string) error {
	vol, ok := m.volumes.Get(volumeID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "volume %q not found", volumeID)
	}

	if vol.State != "in-use" {
		return cerrors.Newf(cerrors.FailedPrecondition, "volume %q is not attached", volumeID)
	}

	vol.State = stateAvailable
	vol.AttachedTo = ""
	vol.Device = ""

	return nil
}

// CreateSnapshot creates a snapshot from a volume.
func (m *Mock) CreateSnapshot(_ context.Context, cfg driver.SnapshotConfig) (*driver.SnapshotInfo, error) {
	vol, ok := m.volumes.Get(cfg.VolumeID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "volume %q not found", cfg.VolumeID)
	}

	id := fmt.Sprintf("snap-%012d", m.snapCounter.Add(1))

	snap := &driver.SnapshotInfo{
		ID:          id,
		VolumeID:    cfg.VolumeID,
		State:       "completed",
		Description: cfg.Description,
		Size:        vol.Size,
		CreatedAt:   m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Tags:        copyTags(cfg.Tags),
	}

	m.snapshots.Set(id, snap)

	result := *snap

	return &result, nil
}

// DeleteSnapshot deletes a snapshot.
func (m *Mock) DeleteSnapshot(_ context.Context, id string) error {
	if !m.snapshots.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "snapshot %q not found", id)
	}

	return nil
}

// DescribeSnapshots returns snapshots matching the given IDs.
func (m *Mock) DescribeSnapshots(_ context.Context, ids []string) ([]driver.SnapshotInfo, error) {
	return describeResources(m.snapshots, ids), nil
}

// CreateImage creates a machine image from an instance.
func (m *Mock) CreateImage(_ context.Context, cfg driver.ImageConfig) (*driver.ImageInfo, error) {
	if _, ok := m.instances.Get(cfg.InstanceID); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "instance %q not found", cfg.InstanceID)
	}

	id := fmt.Sprintf("ami-%012d", m.amiCounter.Add(1))

	img := &driver.ImageInfo{
		ID:          id,
		Name:        cfg.Name,
		State:       "available",
		Description: cfg.Description,
		CreatedAt:   m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Tags:        copyTags(cfg.Tags),
	}

	m.images.Set(id, img)

	result := *img

	return &result, nil
}

// DeregisterImage deregisters a machine image.
func (m *Mock) DeregisterImage(_ context.Context, id string) error {
	if !m.images.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "image %q not found", id)
	}

	return nil
}

// DescribeImages returns images matching the given IDs.
func (m *Mock) DescribeImages(_ context.Context, ids []string) ([]driver.ImageInfo, error) {
	return describeResources(m.images, ids), nil
}

// CreateKeyPair creates a new key pair.
func (m *Mock) CreateKeyPair(_ context.Context, cfg driver.KeyPairConfig) (*driver.KeyPairInfo, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "key pair name must not be empty")
	}

	if _, ok := m.keyPairs.Get(cfg.Name); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "key pair %q already exists", cfg.Name)
	}

	keyType := cfg.KeyType
	if keyType == "" {
		keyType = "rsa"
	}

	kp := &driver.KeyPairInfo{
		ID:          idgen.AWSARN("ec2", m.opts.Region, "123456789012", "key-pair/"+cfg.Name),
		Name:        cfg.Name,
		Fingerprint: "fp-" + cfg.Name,
		KeyType:     keyType,
		PublicKey:   "mock-public-key-" + cfg.Name,
		PrivateKey:  "mock-private-key-" + cfg.Name,
		CreatedAt:   m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Tags:        copyTags(cfg.Tags),
	}

	m.keyPairs.Set(cfg.Name, kp)

	result := *kp

	return &result, nil
}

// DeleteKeyPair deletes a key pair by name.
func (m *Mock) DeleteKeyPair(_ context.Context, name string) error {
	if !m.keyPairs.Delete(name) {
		return cerrors.Newf(cerrors.NotFound, "key pair %q not found", name)
	}

	return nil
}

// DescribeKeyPairs returns key pairs matching the given names.
func (m *Mock) DescribeKeyPairs(_ context.Context, names []string) ([]driver.KeyPairInfo, error) {
	if len(names) == 0 {
		all := m.keyPairs.All()
		result := make([]driver.KeyPairInfo, 0, len(all))

		for _, kp := range all {
			cp := *kp
			cp.PrivateKey = ""
			result = append(result, cp)
		}

		return result, nil
	}

	var result []driver.KeyPairInfo

	for _, name := range names {
		if kp, ok := m.keyPairs.Get(name); ok {
			cp := *kp
			cp.PrivateKey = ""
			result = append(result, cp)
		}
	}

	return result, nil
}

func describeResources[T any](store *memstore.Store[*T], ids []string) []T {
	if len(ids) == 0 {
		all := store.All()
		result := make([]T, 0, len(all))

		for _, v := range all {
			result = append(result, *v)
		}

		return result
	}

	var result []T

	for _, id := range ids {
		if v, ok := store.Get(id); ok {
			result = append(result, *v)
		}
	}

	return result
}
