// Package compute provides an in-memory mock implementation of Google Compute Engine.
package compute

import (
	"context"
	"errors"
	"fmt"
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

// Compile-time check that Mock implements driver.Compute and, when a real
// compute engine is wired, serves console output via driver.ConsoleReader.
var (
	_ driver.Compute       = (*Mock)(nil)
	_ driver.ConsoleReader = (*Mock)(nil)
)

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
	// idempotentStates are states where the operation is a no-op rather than
	// an error. Real GCE documents instances.start/instances.stop as
	// returning a completed zone operation (no error) when the instance is
	// already at the requested power state, rather than an invalid-state
	// error.
	idempotentStates []string
}

var (
	runningMetricValues = []float64{0.25, 1024.0, 512.0, 100.0, 50.0} //nolint:gochecknoglobals // package-level test fixtures
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
	// engineBacked is true when a real config.ComputeEngine backs this
	// instance, so delete deprovisions it and console output is read from the
	// engine rather than synthesized.
	engineBacked bool
}

type asgData struct {
	config   driver.AutoScalingGroup
	policies *memstore.Store[driver.ScalingPolicy]
}

// Mock is an in-memory mock implementation of Google Compute Engine.
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
	imgCounter   atomic.Int64
	monitoring   mondriver.Monitoring
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func gcpMetricNames() []string {
	return []string{
		"instance/cpu/utilization",
		"instance/network/received_bytes_count",
		"instance/network/sent_bytes_count",
		"instance/disk/read_ops_count",
		"instance/disk/write_ops_count",
	}
}

// gcpZoneTagKey mirrors the wire layer's zone tag (server/gcp/compute
// instance_state.go keyZone): a GCE instance's launch zone is round-tripped
// through its tags because the driver Instance model has no zone field. The
// metric emitters read it so the gce_instance monitored resource carries a zone
// label (see metricDimensions).
const gcpZoneTagKey = "cloudemu:gcp:zone"

// metricDimensions builds the gce_instance monitored-resource labels stamped on
// every emitted metric datum: project_id and instance_id are always present,
// zone when the launch zone is known. Cloud Monitoring resource filters
// (resource.labels.zone=…, resource.labels.project_id=…) match on these, so all
// three must be emitted for a filtered timeSeries.list to return the series.
func (m *Mock) metricDimensions(instanceID, zone string) map[string]string {
	dims := map[string]string{
		"instance_id": instanceID,
		"project_id":  m.opts.ProjectID,
	}

	if zone != "" {
		dims["zone"] = zone
	}

	return dims
}

func (m *Mock) emitInstanceMetrics(ctx context.Context, instanceID, launchTime, zone string) {
	if m.monitoring == nil {
		return
	}

	lt, err := time.Parse("2006-01-02T15:04:05Z", launchTime)
	if err != nil {
		lt = m.opts.Clock.Now()
	}

	metrics := gcpMetricNames()
	values := []float64{0.25, 1024.0, 512.0, 100.0, 50.0}
	dims := m.metricDimensions(instanceID, zone)

	var data []mondriver.MetricDatum

	// Backfill the 5 datapoints going backward from launch time so they land in
	// the recent past. Forward-dating would place them in the future, where a
	// metrics query ending at "now" filters them out.
	for i, metricName := range metrics {
		for j := 0; j < 5; j++ {
			ts := lt.Add(-time.Duration(j) * time.Minute)
			data = append(data, mondriver.MetricDatum{
				Namespace:  "compute.googleapis.com",
				MetricName: metricName,
				Value:      values[i],
				Unit:       "None",
				Dimensions: dims,
				Timestamp:  ts,
			})
		}
	}

	_ = m.monitoring.PutMetricData(ctx, data)
}

func (m *Mock) emitLifecycleMetrics(ctx context.Context, instanceID, zone string, values []float64) {
	if m.monitoring == nil {
		return
	}

	metrics := gcpMetricNames()
	now := m.opts.Clock.Now()
	dims := m.metricDimensions(instanceID, zone)
	data := make([]mondriver.MetricDatum, len(metrics))

	for i, metricName := range metrics {
		data[i] = mondriver.MetricDatum{
			Namespace:  "compute.googleapis.com",
			MetricName: metricName,
			Value:      values[i],
			Unit:       "None",
			Dimensions: dims,
			Timestamp:  now,
		}
	}

	_ = m.monitoring.PutMetricData(ctx, data)
}

// New creates a new GCE mock.
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

	return fmt.Sprintf("10.128.%d.%d", n/ipSegmentSize, n%ipSegmentSize)
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

	// Bound the requested count so an oversized MaxCount can't drive an
	// unbounded slice allocation (real providers cap instances per call).
	const maxRunInstances = 1000
	if count > maxRunInstances {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "count %d exceeds the maximum of %d per call", count, maxRunInstances)
	}

	results := make([]driver.Instance, 0, count)

	// created tracks the instances already fully provisioned in this batch, so a
	// mid-batch engine failure can roll them back rather than orphaning live
	// containers and half-tracked instances (real GCE launches the whole batch or
	// none of it).
	created := make([]*instanceData, 0, count)

	for i := 0; i < count; i++ {
		id := idgen.GCPID(m.opts.ProjectID, "instances", idgen.GenerateID("gce-"))

		tags := make(map[string]string, len(cfg.Tags))

		for k, v := range cfg.Tags {
			tags[k] = v
		}

		sg := make([]string, len(cfg.SecurityGroups))
		copy(sg, cfg.SecurityGroups)

		// Honor an explicit private IP (the wire layer resolves the client's
		// networkIP or an address from the referenced subnet's CIDR) for the
		// first instance; the rest of a multi-count batch fall back to the
		// synthetic allocator so they never collide on the same address.
		privateIP := m.nextIP()
		if cfg.PrivateIP != "" && i == 0 {
			privateIP = cfg.PrivateIP
		}

		inst := &instanceData{
			ID: id, ImageID: cfg.ImageID, InstanceType: cfg.InstanceType,
			State: compute.StatePending, PrivateIP: privateIP, SubnetID: cfg.SubnetID,
			SecurityGroups: sg, Tags: tags,
			LaunchTime: m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		}

		// Back the instance with a real compute engine when one is configured.
		// The engine runs the startup-script (carried in cfg.UserData) as the
		// boot script and may surface a reachable IP that overrides the
		// synthetic private IP.
		if engine := m.opts.ComputeEngine; engine != nil {
			di := driver.Instance{ID: inst.ID, ImageID: inst.ImageID, PrivateIP: inst.PrivateIP}
			if err := computeengine.Provision(ctx, engine, &di, &cfg); err != nil {
				m.rollbackInstances(ctx, created)

				return nil, err
			}

			inst.PrivateIP = di.PrivateIP
			inst.engineBacked = true
		}

		// Settle to Running on the record BEFORE publishing it to the store, so a
		// concurrent reader (e.g. disks.list ranging DescribeInstances) never
		// observes an in-place field write on the stored pointer. The state machine
		// is keyed by id, independent of the record pointer.
		m.sm.SetState(id, compute.StatePending)
		_ = m.sm.Transition(id, compute.StateRunning)
		inst.State = compute.StateRunning

		m.instances.Set(id, inst)
		results = append(results, toInstance(inst))
		created = append(created, inst)
		m.emitInstanceMetrics(ctx, id, inst.LaunchTime, tags[gcpZoneTagKey])
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

func (m *Mock) transitionInstances(ctx context.Context, instanceIDs []string, t *lifecycleTransition) error {
	for _, id := range instanceIDs {
		inst, ok := m.instances.Get(id)
		if !ok {
			return cerrors.Newf(cerrors.NotFound, "instance %q not found", id)
		}

		// Real GCE documents start/stop as idempotent on the target state.
		// Skip the state machine and return success without changing state
		// when we're already there.
		if isIdempotent(inst.State, t.idempotentStates) {
			continue
		}

		if err := m.sm.Transition(id, t.intermediateState); err != nil {
			return cerrors.Newf(cerrors.FailedPrecondition, "cannot %s instance %q: %v", t.errVerb, id, err)
		}

		inst.State = t.intermediateState
		_ = m.sm.Transition(id, t.finalState)
		inst.State = t.finalState

		m.emitLifecycleMetrics(ctx, id, inst.Tags[gcpZoneTagKey], t.metricValues)
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
	return m.transitionInstances(ctx, instanceIDs, &startTransition)
}

func (m *Mock) StopInstances(ctx context.Context, instanceIDs []string) error {
	return m.transitionInstances(ctx, instanceIDs, &stopTransition)
}

func (m *Mock) RebootInstances(ctx context.Context, instanceIDs []string) error {
	return m.transitionInstances(ctx, instanceIDs, &rebootTransition)
}

func (m *Mock) TerminateInstances(ctx context.Context, instanceIDs []string) error {
	if err := m.transitionInstances(ctx, instanceIDs, &terminateTransition); err != nil {
		return err
	}

	// Tear down the real backing for any engine-backed instances. Every id is now
	// Terminated (transitionInstances verified they exist); this is best-effort so
	// one instance's Deprovision failure doesn't strand the rest with a live
	// backing and no API path to clean it up. The cleared flag is persisted back.
	var errs []error

	for _, id := range instanceIDs {
		if err := m.deprovisionBacking(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// deprovisionBacking tears down the real compute-engine backing for an
// engine-backed instance and clears its engineBacked flag. It is a no-op for an
// unknown or non-engine-backed instance, so callers can invoke it for every id.
func (m *Mock) deprovisionBacking(ctx context.Context, id string) error {
	inst, ok := m.instances.Get(id)
	if !ok || !inst.engineBacked {
		return nil
	}

	di := driver.Instance{ID: inst.ID}
	if err := computeengine.Deprovision(ctx, m.opts.ComputeEngine, &di); err != nil {
		return err
	}

	inst.engineBacked = false
	m.instances.Set(id, inst)

	return nil
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

// RemoveInstance hard-deletes an instance, mirroring GCP's instances.delete
// (which removes the resource, unlike EC2 terminate which leaves a TERMINATED
// tombstone). GCP-specific; reached via a type assertion from the GCE handler.
func (m *Mock) RemoveInstance(ctx context.Context, instanceID string) error {
	if !m.instances.Has(instanceID) {
		return cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	// Tear down the real backing before dropping the instance, so a hard delete
	// leaves no orphaned container behind.
	if err := m.deprovisionBacking(ctx, instanceID); err != nil {
		return err
	}

	// Honor GCP's autoDelete cascade: a disk attached with autoDelete=true is
	// deleted with the instance, one with autoDelete=false is detached and
	// survives (matching real instances.delete).
	m.settleInstanceDisks(instanceID)

	m.instances.Delete(instanceID)
	m.sm.Remove(instanceID)

	return nil
}

// settleInstanceDisks applies GCP's instance-delete disk cascade to every disk
// attached to instanceID: a disk with DeleteOnTermination=true (autoDelete) is
// DELETED, and one with DeleteOnTermination=false is returned to `available`
// with its attachment cleared. The decision and mutation happen under one store
// lock (UpdateOrDelete) so a concurrent DetachVolume that clears the attachment
// first is observed and the disk is not wrongly deleted.
func (m *Mock) settleInstanceDisks(instanceID string) {
	for _, volID := range m.volumes.Keys() {
		m.volumes.UpdateOrDelete(volID, func(v *driver.VolumeInfo) (*driver.VolumeInfo, bool) {
			if v.AttachedTo != instanceID {
				return v, true
			}

			if v.DeleteOnTermination {
				return v, false
			}

			cp := *v
			cp.State = stateAvailable
			cp.AttachedTo = ""
			cp.Device = ""
			cp.DeleteOnTermination = false

			return &cp, true
		})
	}
}

// MutateInstanceGCP applies a GCP-specific instance mutation used by the GCE
// wire handler for setLabels/setMetadata/setTags/setMachineType/attachDisk/
// detachDisk. Unlike ModifyInstance (which requires a stopped instance), these
// GCP verbs apply to running VMs. set entries are merged into the tag map,
// remove keys are deleted, and machineType replaces the instance type when
// non-empty. GCP-specific; reached via a type assertion from the GCE handler.
//
// The read-modify-write runs inside memstore.Store.Update (write-locked) and is
// copy-on-write, so a concurrent DescribeInstances (which ranges the tag map)
// never races with an in-place mutation.
func (m *Mock) MutateInstanceGCP(instanceID string, set map[string]string, remove []string, machineType string) error {
	if !m.instances.Update(instanceID, func(old *instanceData) *instanceData {
		clone := *old
		clone.Tags = mergeTags(clone.Tags, set, remove)

		if machineType != "" {
			clone.InstanceType = machineType
		}

		return &clone
	}) {
		return cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	return nil
}

func (m *Mock) DescribeInstances(
	_ context.Context, instanceIDs []string, filters []driver.DescribeFilter, _ ...driver.DescribeInstancesOptions,
) ([]driver.Instance, error) {
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

//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateVolume(_ context.Context, cfg driver.VolumeConfig) (*driver.VolumeInfo, error) {
	id := fmt.Sprintf("projects/%s/zones/%s/disks/disk-%d",
		m.opts.ProjectID, m.opts.Region, m.volCounter.Add(1))

	volType := cfg.VolumeType
	if volType == "" {
		volType = "pd-ssd"
	}

	vol := &driver.VolumeInfo{
		ID: id, Size: cfg.Size, VolumeType: volType, State: stateAvailable,
		AvailabilityZone: cfg.AvailabilityZone,
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

func (m *Mock) DeleteVolume(_ context.Context, id string) error {
	vol, ok := m.volumes.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "disk %q not found", id)
	}

	if vol.State == stateInUse {
		return cerrors.Newf(cerrors.FailedPrecondition, "disk %q is attached", id)
	}

	m.volumes.Delete(id)

	return nil
}

func (m *Mock) DescribeVolumes(_ context.Context, ids []string) ([]driver.VolumeInfo, error) {
	return describeResources(m.volumes, ids), nil
}

// ResizeVolumeGCP grows the disk to sizeGb (a no-op when already that large).
// GCP forbids shrinking, so the wire handler rejects smaller sizes before this
// is reached. GCP-specific; reached via a type assertion from the GCE handler.
func (m *Mock) ResizeVolumeGCP(volumeID string, sizeGb int) error {
	if !m.volumes.Update(volumeID, func(old *driver.VolumeInfo) *driver.VolumeInfo {
		clone := *old
		if sizeGb > clone.Size {
			clone.Size = sizeGb
		}

		return &clone
	}) {
		return cerrors.Newf(cerrors.NotFound, "disk %q not found", volumeID)
	}

	return nil
}

func (m *Mock) AttachVolume(_ context.Context, volumeID, instanceID, device string) error {
	return m.AttachDiskGCP(instanceID, volumeID, device, false)
}

// AttachDiskGCP attaches an existing disk to an instance, flipping the driver
// volume to in-use and recording the attachment-scoped auto-delete flag
// (GCP autoDelete ⟷ VolumeInfo.DeleteOnTermination). It is the single attach
// path the GCE wire handler drives for instances.insert boot/data disks and for
// instances.attachDisk, so a disk's driver state and the instance's disks[] view
// never diverge. GCP-specific; reached via a type assertion from the GCE handler.
// The read-modify-write runs inside memstore.Store.Update (write-locked) and is
// copy-on-write, so a concurrent DescribeVolumes never races the mutation.
func (m *Mock) AttachDiskGCP(instanceID, volumeID, device string, autoDelete bool) error {
	var opErr error

	found := m.volumes.Update(volumeID, func(old *driver.VolumeInfo) *driver.VolumeInfo {
		if old.State == stateInUse {
			opErr = cerrors.Newf(cerrors.FailedPrecondition, "disk %q already attached", volumeID)
			return old
		}

		if _, ok := m.instances.Get(instanceID); !ok {
			opErr = cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
			return old
		}

		clone := *old
		clone.State = stateInUse
		clone.AttachedTo = instanceID
		clone.Device = device
		clone.DeleteOnTermination = autoDelete

		return &clone
	})
	if !found {
		return cerrors.Newf(cerrors.NotFound, "disk %q not found", volumeID)
	}

	return opErr
}

// DetachVolume detaches a persistent disk. instanceID/device are accepted for
// driver-interface parity with AWS; GCP detaches by disk id alone.
func (m *Mock) DetachVolume(_ context.Context, volumeID, _, _ string) error {
	var opErr error

	found := m.volumes.Update(volumeID, func(old *driver.VolumeInfo) *driver.VolumeInfo {
		if old.State != stateInUse {
			opErr = cerrors.Newf(cerrors.FailedPrecondition, "disk %q is not attached", volumeID)
			return old
		}

		clone := *old
		clone.State = stateAvailable
		clone.AttachedTo = ""
		clone.Device = ""
		clone.DeleteOnTermination = false

		return &clone
	})
	if !found {
		return cerrors.Newf(cerrors.NotFound, "disk %q not found", volumeID)
	}

	return opErr
}

func (m *Mock) CreateSnapshot(_ context.Context, cfg driver.SnapshotConfig) (*driver.SnapshotInfo, error) {
	vol, ok := m.volumes.Get(cfg.VolumeID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "disk %q not found", cfg.VolumeID)
	}

	id := fmt.Sprintf("projects/%s/global/snapshots/snap-%d",
		m.opts.ProjectID, m.snapCounter.Add(1))

	snap := &driver.SnapshotInfo{
		ID: id, VolumeID: cfg.VolumeID, State: "completed", Description: cfg.Description,
		Size: vol.Size, CreatedAt: m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Tags: copyTags(cfg.Tags),
	}
	m.snapshots.Set(id, snap)

	result := *snap

	return &result, nil
}

func (m *Mock) DeleteSnapshot(_ context.Context, id string) error {
	if !m.snapshots.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "snapshot %q not found", id)
	}

	return nil
}

func (m *Mock) DescribeSnapshots(_ context.Context, ids []string) ([]driver.SnapshotInfo, error) {
	return describeResources(m.snapshots, ids), nil
}

// SetVolumeLabelsGCP replaces a disk's user labels: set entries are written and
// remove keys deleted on the disk's tag map (internal cloudemu tags are left
// untouched — the wire layer only passes user labels). GCP-specific; reached via
// a type assertion from the GCE wire handler for disks.setLabels.
func (m *Mock) SetVolumeLabelsGCP(volumeID string, set map[string]string, remove []string) error {
	return setStoreLabels(m.volumes, volumeID, func(v *driver.VolumeInfo) *map[string]string { return &v.Tags },
		set, remove, "disk")
}

// SetImageLabelsGCP replaces an image's user labels. GCP-specific; reached via a
// type assertion from the GCE wire handler for images.setLabels.
func (m *Mock) SetImageLabelsGCP(imageID string, set map[string]string, remove []string) error {
	return setStoreLabels(m.images, imageID, func(v *driver.ImageInfo) *map[string]string { return &v.Tags },
		set, remove, "image")
}

// SetSnapshotLabelsGCP replaces a snapshot's user labels. GCP-specific; reached
// via a type assertion from the GCE wire handler for snapshots.setLabels.
func (m *Mock) SetSnapshotLabelsGCP(snapshotID string, set map[string]string, remove []string) error {
	return setStoreLabels(m.snapshots, snapshotID, func(v *driver.SnapshotInfo) *map[string]string { return &v.Tags },
		set, remove, "snapshot")
}

// setStoreLabels replaces the user labels on the store record identified by id:
// set entries are written and remove keys deleted on the tag map returned by
// tagsPtr. Returns NotFound (kind names the resource) when no record matches.
//
// The read-modify-write runs inside memstore.Store.Update (write-locked) and is
// copy-on-write: it clones the record and installs a fresh tag map, so a
// concurrent reader holding the previous pointer never observes an in-place map
// mutation (which would trip Go's concurrent map access under go test -race).
func setStoreLabels[T any](
	store *memstore.Store[*T], id string, tagsPtr func(*T) *map[string]string,
	set map[string]string, remove []string, kind string,
) error {
	if !store.Update(id, func(old *T) *T {
		clone := *old
		tp := tagsPtr(&clone)
		*tp = mergeTags(*tp, set, remove)

		return &clone
	}) {
		return cerrors.Newf(cerrors.NotFound, "%s %q not found", kind, id)
	}

	return nil
}

// mergeTags returns a NEW map: a copy of src with set entries written and remove
// keys deleted. src is never mutated (copy-on-write), so a reader still holding
// it is unaffected. The result is always non-nil.
func mergeTags(src, set map[string]string, remove []string) map[string]string {
	out := make(map[string]string, len(src)+len(set))

	for k, v := range src {
		out[k] = v
	}

	for k, v := range set {
		out[k] = v
	}

	for _, k := range remove {
		delete(out, k)
	}

	return out
}

//nolint:gocritic // hugeParam: cfg mirrors the driver-interface signature.
func (m *Mock) CreateImage(_ context.Context, cfg driver.ImageConfig) (*driver.ImageInfo, error) {
	// GCP images are created from a disk, snapshot, or import — not from a
	// source instance. An empty InstanceID is one of those source-based paths,
	// so only validate when a specific instance was named (the EC2-style path).
	if cfg.InstanceID != "" {
		if _, ok := m.instances.Get(cfg.InstanceID); !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "instance %q not found", cfg.InstanceID)
		}
	}

	id := fmt.Sprintf("projects/%s/global/images/img-%d",
		m.opts.ProjectID, m.imgCounter.Add(1))

	img := &driver.ImageInfo{
		ID: id, Name: cfg.Name, State: stateAvailable, Description: cfg.Description,
		CreatedAt: m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Tags:      copyTags(cfg.Tags),
	}
	m.images.Set(id, img)

	result := *img

	return &result, nil
}

func (m *Mock) DeregisterImage(_ context.Context, id string) error {
	if !m.images.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "image %q not found", id)
	}

	return nil
}

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
		ID:          idgen.GCPID(m.opts.ProjectID, "sshKeys", cfg.Name),
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
