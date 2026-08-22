package ec2

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/statemachine"
	"github.com/stackshy/cloudemu/v2/services/compute"
	"github.com/stackshy/cloudemu/v2/services/compute/computeengine"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

var _ driver.Compute = (*Mock)(nil)

const (
	ipSegmentSize  = 256
	stateAvailable = "available"
	stateInUse     = "in-use"

	// visibilityHidden and visibilityVisible are the account-level
	// managed-resource-visibility settings.
	visibilityHidden  = "hidden"
	visibilityVisible = "visible"
)

type lifecycleTransition struct {
	intermediateState string
	finalState        string
	metricValues      []float64
	errVerb           string
	// idempotentStates are states where the operation is a no-op rather than
	// an error. Real AWS EC2 documents StartInstances on a running instance
	// and StopInstances on a stopped instance as idempotent — they return
	// 200 with currentState equal to previousState rather than
	// IncorrectInstanceState.
	idempotentStates []string
}

var (
	runningMetricValues = []float64{25.0, 1024.0, 512.0, 100.0, 50.0} //nolint:gochecknoglobals // package-level test fixtures
	zeroMetricValues    = []float64{0.0, 0.0, 0.0, 0.0, 0.0}          //nolint:gochecknoglobals // package-level test fixtures

	startTransition = lifecycleTransition{ //nolint:gochecknoglobals // package-level config
		intermediateState: compute.StatePending,
		finalState:        compute.StateRunning,
		metricValues:      runningMetricValues,
		errVerb:           "start",
		idempotentStates:  []string{compute.StateRunning, compute.StatePending},
	}
	stopTransition = lifecycleTransition{ //nolint:gochecknoglobals // package-level config
		intermediateState: compute.StateStopping,
		finalState:        compute.StateStopped,
		metricValues:      zeroMetricValues,
		errVerb:           "stop",
		idempotentStates:  []string{compute.StateStopped, compute.StateStopping},
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
	Operator       *operatorData
	// engineBacked is true when a real config.ComputeEngine backs this
	// instance, so Terminate deprovisions it and console output is read from
	// the engine rather than synthesized.
	engineBacked bool
}

// operatorData records service-provider managed-resource ownership.
type operatorData struct {
	Managed   bool
	Principal string
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
	// subnetResolver derives an instance's VPC from its subnet at launch, so
	// instances created with a --subnet-id carry the VPCID that connectivity
	// analysis and VPC teardown depend on. nil until wired by the provider.
	subnetResolver SubnetResolver
	// mu guards managedResourceVisibility, which is scalar shared state that
	// (unlike the memstores) has no internal locking of its own.
	mu sync.RWMutex
	// managedResourceVisibility is "visible" or "hidden". When "hidden",
	// service-provider-managed instances are omitted from DescribeInstances
	// unless the request opts in with IncludeManagedResources.
	managedResourceVisibility string
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

		managedResourceVisibility: visibilityVisible,
	}
}

func (m *Mock) nextIP() string {
	n := m.ipCounter.Add(1)
	return fmt.Sprintf("10.0.%d.%d", n/ipSegmentSize, n%ipSegmentSize)
}

// toInstance converts stored instance data to the driver shape. hidden is the
// account-level managed-resource-visibility ("hidden") resolved once by the
// caller, so this never touches m.mu (avoiding nested read locks).
func toInstance(d *instanceData, hidden bool) driver.Instance {
	sg := make([]string, len(d.SecurityGroups))
	copy(sg, d.SecurityGroups)

	tags := make(map[string]string, len(d.Tags))

	for k, v := range d.Tags {
		tags[k] = v
	}

	var operator *driver.OperatorInfo
	if d.Operator != nil {
		operator = &driver.OperatorInfo{
			Managed:         d.Operator.Managed,
			Principal:       d.Operator.Principal,
			HiddenByDefault: hidden,
		}
	}

	return driver.Instance{
		ID: d.ID, ImageID: d.ImageID, InstanceType: d.InstanceType, State: d.State,
		PrivateIP: d.PrivateIP, PublicIP: d.PublicIP, SubnetID: d.SubnetID, VPCID: d.VPCID,
		SecurityGroups: sg, Tags: tags, LaunchTime: d.LaunchTime, Operator: operator,
	}
}

//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) RunInstances(ctx context.Context, cfg driver.InstanceConfig, count int) ([]driver.Instance, error) {
	if count <= 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "count must be greater than 0")
	}

	// Bound the requested count so an oversized MaxCount can't drive an
	// unbounded slice allocation (real providers cap instances per call).
	const maxRunInstances = 1000
	if count > maxRunInstances {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "count %d exceeds the maximum of %d per call", count, maxRunInstances)
	}

	results := make([]driver.Instance, 0, count)
	hidden := m.visibility() == visibilityHidden

	// created tracks the instances already fully provisioned in this batch, so a
	// mid-batch engine failure can roll them back rather than orphaning live
	// containers and half-tracked instances (real EC2 launches the whole batch or
	// none of it).
	created := make([]*instanceData, 0, count)

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
			VPCID:          m.resolveSubnetVPC(ctx, cfg.SubnetID),
			SecurityGroups: sg, Tags: tags,
			LaunchTime: m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		}

		if cfg.Managed {
			inst.Operator = &operatorData{Managed: true, Principal: cfg.Principal}
		}

		// Back the instance with a real compute engine when one is configured.
		// The engine runs the decoded UserData as the boot script and may
		// surface a reachable IP that overrides the synthetic private IP.
		if engine := m.opts.ComputeEngine; engine != nil {
			di := driver.Instance{ID: inst.ID, ImageID: inst.ImageID, PrivateIP: inst.PrivateIP}
			if err := computeengine.Provision(ctx, engine, &di, &cfg); err != nil {
				m.rollbackInstances(ctx, created)

				return nil, err
			}

			inst.PrivateIP = di.PrivateIP
			inst.engineBacked = true
		}

		m.instances.Set(id, inst)
		m.sm.SetState(id, compute.StatePending)
		_ = m.sm.Transition(id, compute.StateRunning)
		inst.State = compute.StateRunning
		results = append(results, toInstance(inst, hidden))
		created = append(created, inst)

		// Managed (service-owned) instances are hidden from Describe, so
		// emitting instance-dimensioned CloudWatch metrics for them would leak
		// their existence. Suppress metrics for managed instances.
		if !isManaged(inst) {
			m.emitInstanceMetrics(ctx, id, inst.LaunchTime)
		}
	}

	return results, nil
}

// rollbackInstances best-effort tears down instances already provisioned earlier
// in a RunInstances batch that then failed: each engine-backed instance is
// deprovisioned (so no live container remains) and every instance is dropped from
// the store and the state machine (so no half-tracked state remains).
func (m *Mock) rollbackInstances(ctx context.Context, created []*instanceData) {
	engine := m.opts.ComputeEngine

	for _, inst := range created {
		if inst.engineBacked {
			di := driver.Instance{ID: inst.ID}
			_ = computeengine.Deprovision(ctx, engine, &di)
		}

		m.instances.Delete(inst.ID)
		m.sm.Remove(inst.ID)
	}
}

//nolint:gocritic // t is a small read-only config; copying once per call is fine.
func (m *Mock) transitionInstances(ctx context.Context, instanceIDs []string, t lifecycleTransition) error {
	for _, id := range instanceIDs {
		inst, ok := m.instances.Get(id)
		if !ok {
			return cerrors.Newf(cerrors.NotFound, "instance %q not found", id)
		}

		// Real AWS EC2 documents Start/Stop as idempotent on the target
		// state. Skip the state machine and return success without changing
		// state when we're already there.
		if isIdempotent(inst.State, t.idempotentStates) {
			continue
		}

		if err := m.sm.Transition(id, t.intermediateState); err != nil {
			return cerrors.Newf(cerrors.FailedPrecondition, "cannot %s instance %q: %v", t.errVerb, id, err)
		}

		inst.State = t.intermediateState
		_ = m.sm.Transition(id, t.finalState)
		inst.State = t.finalState

		// Managed instances are hidden from Describe; keep them out of metrics
		// too so a hidden instance isn't observable via CloudWatch.
		if !isManaged(inst) {
			m.emitLifecycleMetrics(ctx, id, t.metricValues)
		}
	}

	return nil
}

func isIdempotent(state string, idempotentStates []string) bool {
	for _, s := range idempotentStates {
		if state == s {
			return true
		}
	}

	return false
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
	if err := m.transitionInstances(ctx, instanceIDs, terminateTransition); err != nil {
		return err
	}

	// Tear down the real backing for any engine-backed instances. Every id is now
	// Terminated (transitionInstances verified they exist), and a Terminated
	// instance can't be terminated again — so this must be best-effort: continue
	// through the whole batch and aggregate errors, otherwise one instance's
	// Deprovision failure would strand the rest with a live backing and no API
	// path to clean it up. The cleared flag is persisted back into the store.
	engine := m.opts.ComputeEngine

	var errs []error

	for _, id := range instanceIDs {
		inst, ok := m.instances.Get(id)
		if !ok || !inst.engineBacked {
			continue
		}

		di := driver.Instance{ID: inst.ID}
		if err := computeengine.Deprovision(ctx, engine, &di); err != nil {
			errs = append(errs, err)

			continue
		}

		inst.engineBacked = false
		m.instances.Set(id, inst)
	}

	return errors.Join(errs...)
}

// GetConsoleOutput returns the console output the configured compute engine
// captured for the instance's boot script. It returns a nil slice when the
// instance is not engine-backed (no real backing produced console output).
func (m *Mock) GetConsoleOutput(ctx context.Context, instanceID string) ([]byte, error) {
	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	if !inst.engineBacked {
		return nil, nil
	}

	return computeengine.ConsoleOutput(ctx, m.opts.ComputeEngine, instanceID)
}

func (m *Mock) DescribeInstances(
	_ context.Context, instanceIDs []string, filters []driver.DescribeFilter, opts ...driver.DescribeInstancesOptions,
) ([]driver.Instance, error) {
	var includeManaged bool
	if len(opts) > 0 {
		includeManaged = opts[0].IncludeManagedResources
	}

	hidden := m.visibility() == visibilityHidden

	candidates, err := m.describeCandidates(instanceIDs, hidden, includeManaged)
	if err != nil {
		return nil, err
	}

	results := make([]driver.Instance, 0)

	for _, inst := range candidates {
		if hiddenManaged(inst, hidden, includeManaged) {
			continue
		}

		if matchesFilters(inst, filters) {
			results = append(results, toInstance(inst, hidden))
		}
	}

	return results, nil
}

// describeCandidates gathers the instances a describe should consider. For an
// explicit-ID request naming a hidden managed instance, real EC2 reports the
// id as non-existent (InvalidInstanceID.NotFound) rather than silently
// omitting it, so we surface a NotFound error there. The unfiltered list path
// keeps silently omitting hidden managed instances.
func (m *Mock) describeCandidates(instanceIDs []string, hidden, includeManaged bool) ([]*instanceData, error) {
	if len(instanceIDs) == 0 {
		var candidates []*instanceData
		for _, inst := range m.instances.All() {
			candidates = append(candidates, inst)
		}

		return candidates, nil
	}

	candidates := make([]*instanceData, 0, len(instanceIDs))

	for _, id := range instanceIDs {
		inst, ok := m.instances.Get(id)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "instance %q not found", id)
		}

		if hiddenManaged(inst, hidden, includeManaged) {
			return nil, cerrors.Newf(cerrors.NotFound, "instance %q not found", id)
		}

		candidates = append(candidates, inst)
	}

	return candidates, nil
}

// hiddenManaged reports whether a managed instance must be hidden from a
// describe result. Managed instances are hidden when the account's
// managed-resource-visibility is "hidden" (hidden) and the caller did not opt
// in with IncludeManagedResources.
func hiddenManaged(inst *instanceData, hidden, includeManaged bool) bool {
	return isManaged(inst) && hidden && !includeManaged
}

// isManaged reports whether an instance is a service-provider-managed resource.
func isManaged(inst *instanceData) bool {
	return inst.Operator != nil && inst.Operator.Managed
}

// visibility returns the account-level managed-resource-visibility under a
// read lock.
func (m *Mock) visibility() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.managedResourceVisibility
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

// LaunchManaged provisions a service-managed instance (Operator.Managed=true,
// the given Principal) and returns its instance id. It lets another service
// (e.g. ECS registering a container instance) surface a real managed EC2
// instance, so managed-resource visibility (#300) applies to it. Satisfies the
// ecs provider's ManagedInstanceLauncher interface.
func (m *Mock) LaunchManaged(instanceType, principal string, tags map[string]string) (string, error) {
	instances, err := m.RunInstances(context.Background(), driver.InstanceConfig{
		InstanceType: instanceType,
		Managed:      true,
		Principal:    principal,
		Tags:         tags,
	}, 1)
	if err != nil {
		return "", err
	}

	return instances[0].ID, nil
}

// SetManagedResourceVisibility sets the account's managed-resource-visibility.
// v must be exactly "visible" or "hidden"; any other value (e.g. a typo like
// "hiden") is rejected with an InvalidArgument error rather than silently
// meaning "visible". When "hidden", managed instances are omitted from
// DescribeInstances unless the request sets IncludeManagedResources.
func (m *Mock) SetManagedResourceVisibility(v string) error {
	if v != visibilityVisible && v != visibilityHidden {
		return cerrors.Newf(cerrors.InvalidArgument,
			"invalid managed-resource-visibility %q: must be %q or %q",
			v, visibilityVisible, visibilityHidden)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.managedResourceVisibility = v

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
		IOPS:             cfg.IOPS,
		Throughput:       cfg.Throughput,
		Tier:             cfg.Tier,
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

// DescribeVolumes returns volumes matching the given IDs. An explicit ID that
// does not exist yields InvalidVolume.NotFound, matching real EC2 (an empty
// success would break existence checks and Terraform drift detection).
func (m *Mock) DescribeVolumes(_ context.Context, ids []string) ([]driver.VolumeInfo, error) {
	for _, id := range ids {
		if !m.volumes.Has(id) {
			return nil, cerrors.Newf(cerrors.NotFound, "volume %q not found", id)
		}
	}

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
