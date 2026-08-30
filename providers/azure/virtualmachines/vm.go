// Package virtualmachines provides an in-memory mock implementation of Azure Virtual Machines.
package virtualmachines

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"

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

// Compile-time checks that Mock implements driver.Compute and, when a real
// compute engine is wired, the optional driver.ConsoleReader capability.
var (
	_ driver.Compute            = (*Mock)(nil)
	_ driver.ConsoleReader      = (*Mock)(nil)
	_ driver.AzureVMController  = (*Mock)(nil)
	_ driver.AzureDiskUpdater   = (*Mock)(nil)
	_ driver.KeyPairGenerator   = (*Mock)(nil)
	_ driver.AzureDiskAccessor  = (*Mock)(nil)
	_ driver.AzureSSHKeyUpdater = (*Mock)(nil)
)

const (
	ipSegmentSize  = 256
	stateAvailable = "available"
	stateInUse     = "in-use"
	// identityTypeNone mirrors the ARM ResourceIdentityType "None" value: no
	// managed identity attached.
	identityTypeNone = "None"
	// emulatorTenantID is the single Azure AD directory all emulated
	// resources belong to.
	emulatorTenantID = "11111111-1111-1111-1111-111111111111"
)

type lifecycleTransition struct {
	intermediateState string
	finalState        string
	metricValues      []float64
	errVerb           string
	// powerState is the Azure power state the instance settles into after the
	// transition ("running"/"stopped"/"deallocated"). Empty leaves it unchanged.
	powerState string
	// idempotentPowerStates are the Azure PowerState values the instance may
	// already be in for which this action is a true no-op. Real Azure
	// documents Start/PowerOff/Deallocate as always succeeding (200/202) —
	// no state-conflict error is documented for calling them on a VM already
	// at (or past) the target power level (MS Learn: rest/api/compute/
	// virtual-machines/start, .../deallocate). Nil for actions (Reboot,
	// Terminate) that are not idempotent on the settled state.
	idempotentPowerStates []string
}

var (
	runningMetricValues = []float64{25.0, 1024.0, 512.0, 100.0, 50.0} //nolint:gochecknoglobals // package-level test fixtures
	zeroMetricValues    = []float64{0.0, 0.0, 0.0, 0.0, 0.0}          //nolint:gochecknoglobals // package-level test fixtures

	startTransition = lifecycleTransition{ //nolint:gochecknoglobals // package-level config
		intermediateState:     compute.StatePending,
		finalState:            compute.StateRunning,
		metricValues:          runningMetricValues,
		errVerb:               "start",
		powerState:            powerStateRunning,
		idempotentPowerStates: []string{powerStateRunning},
	}
	stopTransition = lifecycleTransition{ //nolint:gochecknoglobals // package-level config
		intermediateState:     compute.StateStopping,
		finalState:            compute.StateStopped,
		metricValues:          zeroMetricValues,
		errVerb:               "stop",
		powerState:            powerStateDeallocated,
		idempotentPowerStates: []string{powerStateDeallocated},
	}
	powerOffTransition = lifecycleTransition{ //nolint:gochecknoglobals // package-level config
		intermediateState:     compute.StateStopping,
		finalState:            compute.StateStopped,
		metricValues:          zeroMetricValues,
		errVerb:               "power off",
		powerState:            powerStateStopped,
		idempotentPowerStates: []string{powerStateStopped, powerStateDeallocated},
	}
	deallocateTransition = lifecycleTransition{ //nolint:gochecknoglobals // package-level config
		intermediateState:     compute.StateStopping,
		finalState:            compute.StateStopped,
		metricValues:          zeroMetricValues,
		errVerb:               "deallocate",
		powerState:            powerStateDeallocated,
		idempotentPowerStates: []string{powerStateDeallocated},
	}
	rebootTransition = lifecycleTransition{ //nolint:gochecknoglobals // package-level config
		intermediateState: compute.StateRestarting,
		finalState:        compute.StateRunning,
		metricValues:      runningMetricValues,
		errVerb:           "reboot",
		powerState:        powerStateRunning,
	}
	terminateTransition = lifecycleTransition{ //nolint:gochecknoglobals // package-level config
		intermediateState: compute.StateShuttingDown,
		finalState:        compute.StateTerminated,
		metricValues:      zeroMetricValues,
		errVerb:           "terminate",
	}
)

// Azure power states we track distinctly from the lifecycle state, so a
// PowerOff'd VM (stopped, still allocated) is reported apart from a Deallocated
// one (compute released).
const (
	powerStateRunning     = "running"
	powerStateStopped     = "stopped"
	powerStateDeallocated = "deallocated"
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
	OSType         string
	Priority       string
	LicenseType    string
	Zones          []string
	Region         string
	ResourceGroup  string
	// PowerState is the Azure power state ("running"/"stopped"/"deallocated").
	PowerState string
	// Generalized is true once the VM has been generalized (Azure Generalize
	// action), a precondition for capturing it into a reusable image.
	Generalized bool
	// Identity is the resolved managed-identity block, nil when no identity
	// is attached.
	Identity *driver.ManagedIdentity
	// engineBacked is true when a real config.ComputeEngine backs this
	// instance, so Terminate deprovisions it and console output is read from
	// the engine rather than synthesized.
	engineBacked bool
	// NICRefs are the network interfaces this instance was attached to at
	// launch (networkProfile.networkInterfaces), so Terminate knows which NICs
	// to detach (clearing their properties.virtualMachine back-reference).
	// Empty when no NICAttacher is wired or the VM referenced no NICs.
	NICRefs []driver.AzureNICRef
}

type asgData struct {
	config   driver.AutoScalingGroup
	policies *memstore.Store[driver.ScalingPolicy]
}

// Mock is an in-memory mock implementation of the Azure Virtual Machines service.
type Mock struct {
	instances    *memstore.Store[*instanceData]
	asgs         *memstore.Store[*asgData]
	spotRequests *memstore.Store[*driver.SpotInstanceRequest]
	templates    *memstore.Store[*driver.LaunchTemplate]
	volumes      *memstore.Store[*driver.VolumeInfo]
	snapshots    *memstore.Store[*driver.SnapshotInfo]
	images       *memstore.Store[*driver.ImageInfo]
	keyPairs     *memstore.Store[*driver.KeyPairInfo]
	scaleSets    *memstore.Store[*ScaleSet]
	diskAccess   *memstore.Store[string]
	sm           *statemachine.Machine
	opts         *config.Options
	ipCounter    atomic.Int64
	volCounter   atomic.Int64
	snapCounter  atomic.Int64
	imgCounter   atomic.Int64
	monitoring   mondriver.Monitoring
	// nicAttacher keeps a network interface's virtualMachine back-reference in
	// sync with the VM lifecycle (attach on create, detach on terminate). nil
	// until wired by the provider factory, in which case NIC attachment is
	// silently skipped (matching the pre-wiring behavior of every other
	// optional cross-service hook in this package).
	nicAttacher NICAttacher
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// armNameTag mirrors server/azure/virtualmachines.armNameTag: the tag key the
// ARM wire handler stores the caller's ARM resource name under (the driver
// itself indexes instances by an internally generated id, not the ARM name).
// It is duplicated here, not imported, because the driver layer must not
// depend on the wire server layer.
const armNameTag = "cloudemu:azureName"

// armResourceID builds the full Azure ARM resource id for inst, the same
// shape (and value) that a real armmonitor client sees in a metrics query's
// resourceUri path. Metrics are stamped with this as the "resourceId"
// dimension so Microsoft.Insights/metrics can scope a query to one resource.
// When inst was created directly through the portable driver (no ARM name
// tag recorded), the internal instance id is used instead.
func (m *Mock) armResourceID(inst *instanceData) string {
	name := inst.Tags[armNameTag]
	if name == "" {
		name = inst.ID
	}

	return idgen.AzureID(m.opts.AccountID, inst.ResourceGroup, "Microsoft.Compute", "virtualMachines", name)
}

func (m *Mock) emitInstanceMetrics(ctx context.Context, inst *instanceData) {
	if m.monitoring == nil {
		return
	}

	lt, err := time.Parse("2006-01-02T15:04:05Z", inst.LaunchTime)
	if err != nil {
		lt = m.opts.Clock.Now()
	}

	metrics := []string{"Percentage CPU", "Network In Total", "Network Out Total", "Disk Read Operations/Sec", "Disk Write Operations/Sec"}
	values := []float64{25.0, 1024.0, 512.0, 100.0, 50.0}
	resourceID := m.armResourceID(inst)

	var data []mondriver.MetricDatum

	// Backfill the 5 datapoints going backward from launch time so they land in
	// the recent past. Forward-dating would place them in the future, where a
	// metrics query ending at "now" filters them out.
	for i, metricName := range metrics {
		for j := 0; j < 5; j++ {
			ts := lt.Add(-time.Duration(j) * time.Minute)
			data = append(data, mondriver.MetricDatum{
				Namespace:  "Microsoft.Compute/virtualMachines",
				MetricName: metricName,
				Value:      values[i],
				Unit:       "None",
				Dimensions: map[string]string{"resourceId": resourceID},
				Timestamp:  ts,
			})
		}
	}

	_ = m.monitoring.PutMetricData(ctx, data)
}

func (m *Mock) emitLifecycleMetrics(ctx context.Context, inst *instanceData, values []float64) {
	if m.monitoring == nil {
		return
	}

	metrics := []string{"Percentage CPU", "Network In Total", "Network Out Total", "Disk Read Operations/Sec", "Disk Write Operations/Sec"}
	now := m.opts.Clock.Now()
	resourceID := m.armResourceID(inst)
	data := make([]mondriver.MetricDatum, len(metrics))

	for i, metricName := range metrics {
		data[i] = mondriver.MetricDatum{
			Namespace:  "Microsoft.Compute/virtualMachines",
			MetricName: metricName,
			Value:      values[i],
			Unit:       "None",
			Dimensions: map[string]string{"resourceId": resourceID},
			Timestamp:  now,
		}
	}

	_ = m.monitoring.PutMetricData(ctx, data)
}

// New creates a new Azure Virtual Machines mock.
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
		scaleSets:    memstore.New[*ScaleSet](),
		diskAccess:   memstore.New[string](),
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
		OSType: d.OSType, Priority: d.Priority, LicenseType: d.LicenseType,
		Zones:  append([]string(nil), d.Zones...),
		Region: d.Region, ResourceGroup: d.ResourceGroup,
		PowerState:  d.PowerState,
		Generalized: d.Generalized,
		Identity:    copyIdentity(d.Identity),
	}
}

// copyIdentity deep-copies a ManagedIdentity so a caller holding the returned
// driver.Instance cannot mutate the stored instanceData through it.
func copyIdentity(in *driver.ManagedIdentity) *driver.ManagedIdentity {
	if in == nil {
		return nil
	}

	out := *in

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]driver.UserAssignedIdentity, len(in.UserAssigned))
		for k, v := range in.UserAssigned {
			out.UserAssigned[k] = v
		}
	}

	return &out
}

// resolveIdentity normalizes a caller-supplied managed identity: for a
// system-assigned identity it synthesizes a deterministic principal/tenant
// GUID pair (as Azure does on assignment), and for each user-assigned
// identity it synthesizes a deterministic principal/client GUID pair, keyed
// by the seed (the owning instance's id) so the same instance always reports
// the same identity across GET/List. A nil or "None" identity resolves to
// nil (no identity attached).
func resolveIdentity(in *driver.ManagedIdentity, seed string) *driver.ManagedIdentity {
	if in == nil || in.Type == "" || strings.EqualFold(in.Type, identityTypeNone) {
		return nil
	}

	out := &driver.ManagedIdentity{Type: in.Type}

	if strings.Contains(strings.ToLower(in.Type), "systemassigned") {
		out.PrincipalID = idgen.SyntheticGUID("principal/vm/" + seed)
		out.TenantID = emulatorTenantID
	}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]driver.UserAssignedIdentity, len(in.UserAssigned))

		for id := range in.UserAssigned {
			out.UserAssigned[id] = driver.UserAssignedIdentity{
				PrincipalID: idgen.SyntheticGUID("principal/uai/" + id),
				ClientID:    idgen.SyntheticGUID("client/uai/" + id),
			}
		}
	}

	return out
}

// RunInstances creates and starts the specified number of virtual machine instances.
//
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
	// containers and half-tracked instances (real Azure creates the VM or none).
	created := make([]*instanceData, 0, count)

	for i := 0; i < count; i++ {
		id := idgen.GenerateID("vm-")

		tags := make(map[string]string, len(cfg.Tags))

		for k, v := range cfg.Tags {
			tags[k] = v
		}

		sg := make([]string, len(cfg.SecurityGroups))
		copy(sg, cfg.SecurityGroups)

		zones := append([]string(nil), cfg.Zones...)

		inst := &instanceData{
			ID: id, ImageID: cfg.ImageID, InstanceType: cfg.InstanceType,
			State: compute.StatePending, PrivateIP: m.nextIP(), SubnetID: cfg.SubnetID,
			SecurityGroups: sg, Tags: tags,
			LaunchTime:    m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
			OSType:        cfg.OSType,
			Priority:      cfg.Priority,
			LicenseType:   cfg.LicenseType,
			Zones:         zones,
			Region:        cfg.Region,
			ResourceGroup: cfg.ResourceGroup,
			PowerState:    powerStateRunning,
		}

		inst.Identity = resolveIdentity(cfg.Identity, id)

		// Back the instance with a real compute engine when one is configured.
		// The engine runs the decoded customData as the boot script and may
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

		if err := m.attachNICs(ctx, inst, cfg.NetworkInterfaces); err != nil {
			m.rollbackInstances(ctx, created)

			return nil, err
		}

		m.instances.Set(id, inst)
		m.sm.SetState(id, compute.StatePending)
		_ = m.sm.Transition(id, compute.StateRunning)
		inst.State = compute.StateRunning
		results = append(results, toInstance(inst))
		created = append(created, inst)
		m.emitInstanceMetrics(ctx, inst)
	}

	return results, nil
}

// attachNICs attaches each of refs to inst, setting the NIC's
// properties.virtualMachine back-reference to inst's ARM resource id, and
// records what was attached on inst.NICRefs so a later Terminate knows what
// to detach. It is a no-op when no NICAttacher is wired. A failure partway
// through (e.g. a NIC already attached to a different VM) rolls back the NICs
// already attached during this call before returning the error, so a failed
// RunInstances never leaves a NIC dangling on a VM that was never created.
func (m *Mock) attachNICs(ctx context.Context, inst *instanceData, refs []driver.AzureNICRef) error {
	if m.nicAttacher == nil || len(refs) == 0 {
		return nil
	}

	vmID := m.armResourceID(inst)
	attached := make([]driver.AzureNICRef, 0, len(refs))

	for _, ref := range refs {
		if err := m.nicAttacher.AttachNetworkInterface(ctx, ref.ResourceGroup, ref.Name, vmID); err != nil {
			for _, done := range attached {
				_ = m.nicAttacher.DetachNetworkInterface(ctx, done.ResourceGroup, done.Name, vmID)
			}

			return err
		}

		attached = append(attached, ref)
	}

	inst.NICRefs = attached

	return nil
}

// detachNICs clears the virtualMachine back-reference of every NIC inst was
// attached to at launch, best-effort: every ref is attempted and failures are
// aggregated rather than stopping partway, so a Terminate always releases
// every NIC it can. It is a no-op when no NICAttacher is wired.
func (m *Mock) detachNICs(ctx context.Context, inst *instanceData) error {
	if m.nicAttacher == nil || len(inst.NICRefs) == 0 {
		return nil
	}

	vmID := m.armResourceID(inst)

	var errs []error

	for _, ref := range inst.NICRefs {
		if err := m.nicAttacher.DetachNetworkInterface(ctx, ref.ResourceGroup, ref.Name, vmID); err != nil {
			errs = append(errs, err)
		}
	}

	inst.NICRefs = nil

	return errors.Join(errs...)
}

// reconcileNICs brings inst's attached network interfaces into line with the
// desired set a PUT CreateOrUpdate supplied in networkProfile.networkInterfaces
// (Azure's full-replace on the network profile). NICs newly present in desired
// are attached (setting their properties.virtualMachine back-reference); NICs
// inst still holds but that are no longer desired are detached (clearing it, so
// they return to Unattached and can be deleted); NICs in both are left as-is
// (attach is idempotent on the same VM). New NICs are attached first — with
// rollback of just those on failure — before any detach runs, so a failed
// attach (e.g. a NIC already attached to another VM) leaves the VM's existing
// attachments untouched. inst.NICRefs is then set to the desired set, whose
// first entry stays the primary NIC. A no-op when no NICAttacher is wired.
func (m *Mock) reconcileNICs(ctx context.Context, inst *instanceData, desired []driver.AzureNICRef) error {
	if m.nicAttacher == nil {
		return nil
	}

	vmID := m.armResourceID(inst)
	desiredSet := nicRefSet(desired)

	if err := m.attachNewNICs(ctx, vmID, nicRefSet(inst.NICRefs), desired); err != nil {
		return err
	}

	if err := m.detachRemovedNICs(ctx, vmID, inst.NICRefs, desiredSet); err != nil {
		return err
	}

	inst.NICRefs = append([]driver.AzureNICRef(nil), desired...)

	return nil
}

// nicRefSet indexes a slice of NIC references as a set, for membership tests
// during reconcile.
func nicRefSet(refs []driver.AzureNICRef) map[driver.AzureNICRef]bool {
	set := make(map[driver.AzureNICRef]bool, len(refs))
	for _, ref := range refs {
		set[ref] = true
	}

	return set
}

// attachNewNICs attaches every desired NIC not already present in current
// (deduping repeats within desired), rolling back only the NICs it attached
// during this call if one fails — so a failed attach leaves the VM's existing
// attachments untouched.
func (m *Mock) attachNewNICs(
	ctx context.Context, vmID string, current map[driver.AzureNICRef]bool, desired []driver.AzureNICRef,
) error {
	seen := make(map[driver.AzureNICRef]bool, len(desired))
	attached := make([]driver.AzureNICRef, 0, len(desired))

	for _, ref := range desired {
		if seen[ref] || current[ref] {
			seen[ref] = true

			continue
		}

		seen[ref] = true

		if err := m.nicAttacher.AttachNetworkInterface(ctx, ref.ResourceGroup, ref.Name, vmID); err != nil {
			for _, done := range attached {
				_ = m.nicAttacher.DetachNetworkInterface(ctx, done.ResourceGroup, done.Name, vmID)
			}

			return err
		}

		attached = append(attached, ref)
	}

	return nil
}

// detachRemovedNICs detaches every currently-attached NIC absent from
// desiredSet, best-effort: every removal is attempted so one failure doesn't
// strand the rest still holding a stale back-reference.
func (m *Mock) detachRemovedNICs(
	ctx context.Context, vmID string, current []driver.AzureNICRef, desiredSet map[driver.AzureNICRef]bool,
) error {
	var errs []error

	for _, ref := range current {
		if desiredSet[ref] {
			continue
		}

		if err := m.nicAttacher.DetachNetworkInterface(ctx, ref.ResourceGroup, ref.Name, vmID); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// rollbackInstances best-effort tears down instances already provisioned earlier
// in a RunInstances batch that then failed: every NIC the instance attached at
// launch is detached (so no NIC keeps a virtualMachine back-reference to a VM
// that's about to vanish, which would strand it as permanently "in use"), each
// engine-backed instance is deprovisioned (so no live container remains) and
// every instance is dropped from the store and the state machine (so no
// half-tracked state remains).
func (m *Mock) rollbackInstances(ctx context.Context, created []*instanceData) {
	engine := m.opts.ComputeEngine

	for _, inst := range created {
		_ = m.detachNICs(ctx, inst)

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

		// The lifecycle state machine is already settled at this action's
		// target state (e.g. Start on an already-running VM, or PowerOff
		// after Deallocate — both settle at compute.StateStopped). Walking
		// the FSM again would hit an illegal same-state edge, but real Azure
		// treats the action as idempotent, so short-circuit instead of
		// erroring.
		if inst.State == t.finalState && isIdempotentPowerState(t) {
			if !isIdempotent(inst.PowerState, t.idempotentPowerStates) && t.powerState != "" {
				inst.PowerState = t.powerState
				m.emitLifecycleMetrics(ctx, inst, t.metricValues)
			}

			continue
		}

		if err := m.sm.Transition(id, t.intermediateState); err != nil {
			return cerrors.Newf(cerrors.FailedPrecondition, "cannot %s instance %q: %v", t.errVerb, id, err)
		}

		inst.State = t.intermediateState
		_ = m.sm.Transition(id, t.finalState)
		inst.State = t.finalState

		if t.powerState != "" {
			inst.PowerState = t.powerState
		}

		m.emitLifecycleMetrics(ctx, inst, t.metricValues)
	}

	return nil
}

// isIdempotentPowerState reports whether t is one of the power actions
// (Start/Stop/PowerOff/Deallocate) that Azure documents as idempotent on the
// settled state, as opposed to Reboot/Terminate which always act.
func isIdempotentPowerState(t *lifecycleTransition) bool {
	return len(t.idempotentPowerStates) > 0
}

func isIdempotent(state string, idempotentStates []string) bool {
	for _, s := range idempotentStates {
		if state == s {
			return true
		}
	}

	return false
}

// StartInstances starts the specified stopped virtual machine instances.
func (m *Mock) StartInstances(ctx context.Context, instanceIDs []string) error {
	return m.transitionInstances(ctx, instanceIDs, &startTransition)
}

// StopInstances stops the specified running virtual machine instances.
func (m *Mock) StopInstances(ctx context.Context, instanceIDs []string) error {
	return m.transitionInstances(ctx, instanceIDs, &stopTransition)
}

// PowerOff stops the guest OS of an instance while keeping the VM allocated
// (Azure PowerState/stopped). Unlike Deallocate, the compute is not released.
func (m *Mock) PowerOff(ctx context.Context, instanceID string) error {
	return m.transitionInstances(ctx, []string{instanceID}, &powerOffTransition)
}

// Deallocate stops the guest OS and releases the allocated compute of an
// instance (Azure PowerState/deallocated).
func (m *Mock) Deallocate(ctx context.Context, instanceID string) error {
	return m.transitionInstances(ctx, []string{instanceID}, &deallocateTransition)
}

// UpdateInstance overwrites the mutable configuration of an existing instance
// in place, preserving its ID, launch time, IP, and current power state. It
// backs the idempotent ARM CreateOrUpdate PUT so a repeated PUT updates the VM
// rather than provisioning a duplicate.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) UpdateInstance(ctx context.Context, instanceID string, cfg driver.InstanceConfig) error {
	inst, found := m.instances.Get(instanceID)
	if !found {
		return cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	// Reconcile networkProfile.networkInterfaces before mutating the rest of the
	// config, so an unattachable NIC (already attached to another VM) aborts the
	// whole PUT rather than half-applying it. Only when the request actually
	// supplied a networkProfile (>=1 NIC): an omitted/empty set on a PUT leaves
	// the existing NICs untouched (no wipe), mirroring applyDataDisks'
	// present-vs-omitted discriminator for data disks.
	if len(cfg.NetworkInterfaces) > 0 {
		if err := m.reconcileNICs(ctx, inst, cfg.NetworkInterfaces); err != nil {
			return err
		}
	}

	if ok := m.instances.Update(instanceID, func(inst *instanceData) *instanceData {
		applyMutableConfig(inst, cfg, instanceID)

		return inst
	}); !ok {
		return cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	return nil
}

// applyMutableConfig overwrites inst's mutable fields from a PUT CreateOrUpdate
// cfg (the whole-body replace UpdateInstance backs). Empty scalars leave the
// corresponding field untouched; Priority/LicenseType and Tags are always
// replaced. A nil cfg.Identity preserves the existing identity (the request
// omitted the block); a non-nil one (including an explicit "None") replaces it.
// NIC reconciliation is handled separately by reconcileNICs before this runs.
//
//nolint:gocritic // hugeParam: cfg mirrors the driver-interface config shape.
func applyMutableConfig(inst *instanceData, cfg driver.InstanceConfig, instanceID string) {
	if cfg.InstanceType != "" {
		inst.InstanceType = cfg.InstanceType
	}

	if cfg.ImageID != "" {
		inst.ImageID = cfg.ImageID
	}

	if cfg.SubnetID != "" {
		inst.SubnetID = cfg.SubnetID
	}

	if cfg.OSType != "" {
		inst.OSType = cfg.OSType
	}

	inst.Priority = cfg.Priority
	inst.LicenseType = cfg.LicenseType

	if len(cfg.Zones) > 0 {
		inst.Zones = append([]string(nil), cfg.Zones...)
	}

	if cfg.Region != "" {
		inst.Region = cfg.Region
	}

	inst.Tags = copyTags(cfg.Tags)

	if cfg.Identity != nil {
		inst.Identity = resolveIdentity(cfg.Identity, instanceID)
	}
}

// PatchInstance applies an ARM PATCH Update (BeginUpdate) merge-patch to an
// existing instance. Only the fields the request supplied are applied: a
// non-empty VMSize resizes the VM, a non-nil Tags map is MERGED into the
// existing tags (adding/overwriting supplied keys, leaving omitted keys —
// including the internal ARM-name tag — in place), and a non-nil Identity
// replaces the identity block. Everything else (priority, licenseType, image,
// zones, …) is left untouched, unlike UpdateInstance's full-config replace.
func (m *Mock) PatchInstance(_ context.Context, instanceID string, patch driver.AzureVMPatch) error {
	ok := m.instances.Update(instanceID, func(inst *instanceData) *instanceData {
		if patch.VMSize != "" {
			inst.InstanceType = patch.VMSize
		}

		if patch.Tags != nil {
			if inst.Tags == nil {
				inst.Tags = make(map[string]string, len(patch.Tags))
			}

			for k, v := range patch.Tags {
				inst.Tags[k] = v
			}
		}

		if patch.Identity != nil {
			inst.Identity = resolveIdentity(patch.Identity, instanceID)
		}

		return inst
	})

	if !ok {
		return cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	return nil
}

// GeneralizeInstance marks an instance as generalized (Azure Generalize
// action), a precondition for capturing it into a reusable image. Real Azure
// requires the VM to be stopped or deallocated first: generalizing a running VM
// is rejected. It is otherwise idempotent — generalizing an already-generalized
// (and still stopped/deallocated) VM succeeds.
func (m *Mock) GeneralizeInstance(_ context.Context, instanceID string) error {
	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	if inst.PowerState == powerStateRunning {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"instance %q must be stopped or deallocated before it can be generalized", instanceID)
	}

	inst.Generalized = true

	return nil
}

// RebootInstances reboots the specified running virtual machine instances.
func (m *Mock) RebootInstances(ctx context.Context, instanceIDs []string) error {
	return m.transitionInstances(ctx, instanceIDs, &rebootTransition)
}

// TerminateInstances terminates the specified virtual machine instances.
func (m *Mock) TerminateInstances(ctx context.Context, instanceIDs []string) error {
	if err := m.transitionInstances(ctx, instanceIDs, &terminateTransition); err != nil {
		return err
	}

	// Tear down the real backing for any engine-backed instances. Every id is now
	// Terminated (transitionInstances verified they exist), so this is best-effort:
	// continue through the whole batch and aggregate errors, otherwise one
	// instance's Deprovision failure would strand the rest with a live backing and
	// no API path to clean it up. The cleared flag is persisted back into the store.
	engine := m.opts.ComputeEngine

	var errs []error

	for _, id := range instanceIDs {
		inst, ok := m.instances.Get(id)
		if !ok {
			continue
		}

		if err := m.detachNICs(ctx, inst); err != nil {
			errs = append(errs, err)
		}

		if !inst.engineBacked {
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
// captured for the instance's boot script — the boot-diagnostics serial log
// analog. It returns a nil slice when the instance is not engine-backed (no
// real backing produced console output).
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

// DescribeInstances returns instances matching the given IDs and filters.
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

// ModifyInstance modifies attributes of a stopped virtual machine instance.
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

//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateVolume(_ context.Context, cfg driver.VolumeConfig) (*driver.VolumeInfo, error) {
	id := fmt.Sprintf("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/disks/disk-%d",
		m.volCounter.Add(1))

	volType := cfg.VolumeType
	if volType == "" {
		volType = "Premium_LRS"
	}

	vol := &driver.VolumeInfo{
		ID: id, Size: cfg.Size, VolumeType: volType, State: stateAvailable,
		AvailabilityZone: cfg.AvailabilityZone,
		CreatedAt:        m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Tags:             copyTags(cfg.Tags),
		IOPS:             cfg.IOPS,
		Throughput:       cfg.Throughput,
		Tier:             cfg.Tier,
		Location:         cfg.Location,
	}
	m.volumes.Set(id, vol)

	result := *vol

	return &result, nil
}

// UpdateVolume mutates an existing managed disk in place (ARM Disks
// CreateOrUpdate on a disk that already exists). It preserves the volume's ID,
// CreatedAt, and current attachment (State/AttachedTo/Device) — so the derived
// uniqueId and timeCreated stay stable and an attached disk is not duplicated —
// while updating the mutable cost fields from cfg. A non-zero Size, non-empty
// VolumeType/Tier are applied; IOPS/Throughput and Tags are replaced from cfg
// (PUT is a full resource replacement).
//
//nolint:gocritic // hugeParam: cfg mirrors the driver-interface signature.
func (m *Mock) UpdateVolume(_ context.Context, id string, cfg driver.VolumeConfig) (*driver.VolumeInfo, error) {
	vol, ok := m.volumes.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "disk %q not found", id)
	}

	if cfg.Size != 0 {
		vol.Size = cfg.Size
	}

	if cfg.VolumeType != "" {
		vol.VolumeType = cfg.VolumeType
	}

	if cfg.Tier != "" {
		vol.Tier = cfg.Tier
	}

	if cfg.Location != "" {
		vol.Location = cfg.Location
	}

	vol.IOPS = cfg.IOPS
	vol.Throughput = cfg.Throughput
	vol.Tags = copyTags(cfg.Tags)

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

func (m *Mock) AttachVolume(_ context.Context, volumeID, instanceID, device string) error {
	vol, ok := m.volumes.Get(volumeID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "disk %q not found", volumeID)
	}

	if vol.State == stateInUse {
		return cerrors.Newf(cerrors.FailedPrecondition, "disk %q already attached", volumeID)
	}

	if _, ok := m.instances.Get(instanceID); !ok {
		return cerrors.Newf(cerrors.NotFound, "VM %q not found", instanceID)
	}

	vol.State = stateInUse
	vol.AttachedTo = instanceID
	vol.Device = device

	return nil
}

// DetachVolume detaches a managed disk. instanceID/device are accepted for
// driver-interface parity with AWS; Azure detaches by disk id alone.
func (m *Mock) DetachVolume(_ context.Context, volumeID, _, _ string) error {
	vol, ok := m.volumes.Get(volumeID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "disk %q not found", volumeID)
	}

	if vol.State != "in-use" {
		return cerrors.Newf(cerrors.FailedPrecondition, "disk %q is not attached", volumeID)
	}

	vol.State = stateAvailable
	vol.AttachedTo = ""
	vol.Device = ""

	return nil
}

// GrantDiskAccess issues a time-bounded SAS URI granting the requested access
// level to a managed disk (Azure Disks beginGetAccess). The synthesized URI
// mirrors the shape of a real managed-disk export SAS: an md-* blob host, a
// signed-start (st) and signed-expiry (se) window derived from durationSeconds,
// and a signed-permission (sp) reflecting the access level.
func (m *Mock) GrantDiskAccess(_ context.Context, volumeID, access string, durationSeconds int) (string, error) {
	vol, ok := m.volumes.Get(volumeID)
	if !ok {
		return "", cerrors.Newf(cerrors.NotFound, "disk %q not found", volumeID)
	}

	now := m.opts.Clock.Now().UTC()

	sum := sha256.Sum256([]byte(vol.ID))
	host := "md-" + hex.EncodeToString(sum[:6])
	token := hex.EncodeToString(sum[6:12])

	sas := fmt.Sprintf(
		"https://%s.blob.storage.azure.net/%s/abcd?sv=2018-03-28&sr=b&si=&sig=cloudemu&st=%s&se=%s&sp=%s",
		host, token,
		now.Format("2006-01-02T15:04:05Z"),
		now.Add(time.Duration(durationSeconds)*time.Second).Format("2006-01-02T15:04:05Z"),
		sasPermission(access),
	)

	m.diskAccess.Set(volumeID, sas)

	return sas, nil
}

// RevokeDiskAccess revokes any SAS access previously granted to a managed disk
// (Azure Disks endGetAccess).
func (m *Mock) RevokeDiskAccess(_ context.Context, volumeID string) error {
	if _, ok := m.volumes.Get(volumeID); !ok {
		return cerrors.Newf(cerrors.NotFound, "disk %q not found", volumeID)
	}

	m.diskAccess.Delete(volumeID)

	return nil
}

// sasPermission maps an Azure disk AccessLevel to the SAS signed-permission
// (sp) code: Write grants read+write ("rw"), everything else read-only ("r").
func sasPermission(access string) string {
	if access == "Write" {
		return "rw"
	}

	return "r"
}

func (m *Mock) CreateSnapshot(_ context.Context, cfg driver.SnapshotConfig) (*driver.SnapshotInfo, error) {
	vol, ok := m.volumes.Get(cfg.VolumeID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "disk %q not found", cfg.VolumeID)
	}

	id := fmt.Sprintf("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/snapshots/snap-%d",
		m.snapCounter.Add(1))

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

//nolint:gocritic // hugeParam: cfg mirrors the driver-interface signature.
func (m *Mock) CreateImage(_ context.Context, cfg driver.ImageConfig) (*driver.ImageInfo, error) {
	switch {
	case cfg.InstanceID != "":
		if _, ok := m.instances.Get(cfg.InstanceID); !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "VM %q not found", cfg.InstanceID)
		}
	case cfg.OSDiskID != "":
		// Disk-sourced image: OSDiskID is the source disk's ARM ID, not a driver
		// volume key, so the wire handler owns disk-existence validation.
	default:
		return nil, cerrors.New(cerrors.InvalidArgument, "image requires a source VM or OS disk")
	}

	id := fmt.Sprintf("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/images/img-%d",
		m.imgCounter.Add(1))

	img := &driver.ImageInfo{
		ID: id, Name: cfg.Name, State: stateAvailable, Description: cfg.Description,
		CreatedAt:  m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Tags:       copyTags(cfg.Tags),
		OSDiskID:   cfg.OSDiskID,
		OSType:     cfg.OSType,
		OSState:    cfg.OSState,
		DiskSizeGB: cfg.DiskSizeGB,
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
		ID:          idgen.AzureID("sub", "rg", "Microsoft.Compute", "sshPublicKeys", cfg.Name),
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

// GenerateKeyPair generates a fresh RSA key pair server-side for an existing
// sshPublicKey resource (Azure generateKeyPair action). It stores the generated
// public key on the resource and returns both the public key (OpenSSH
// authorized_keys form) and the private key (PEM PKCS#1) — the one time the
// private key is disclosed.
func (m *Mock) GenerateKeyPair(_ context.Context, name string) (*driver.KeyPairInfo, error) {
	kp, ok := m.keyPairs.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "sshPublicKey %q not found", name)
	}

	pub, priv, err := generateRSAKeyPair()
	if err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "generate key pair: %v", err)
	}

	kp.PublicKey = pub
	kp.PrivateKey = priv
	kp.Fingerprint = "fp-" + name
	m.keyPairs.Set(name, kp)

	result := *kp

	return &result, nil
}

// rsaKeyBits is the modulus size Azure uses for generated SSH key pairs.
const rsaKeyBits = 2048

// generateRSAKeyPair returns a fresh RSA key pair: the public key in OpenSSH
// authorized_keys form and the private key in PEM (PKCS#1) form.
func generateRSAKeyPair() (publicKey, privateKey string, err error) {
	key, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return "", "", err
	}

	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	sshPub, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		return "", "", err
	}

	return string(ssh.MarshalAuthorizedKey(sshPub)), string(privPEM), nil
}

// UpdateKeyPair updates the public key and/or tags of an existing key pair
// (Azure sshPublicKeys PATCH Update). A nil publicKey leaves the key material
// unchanged; a non-nil tags map replaces the resource's tags.
func (m *Mock) UpdateKeyPair(_ context.Context, name string, publicKey *string, tags map[string]string) (*driver.KeyPairInfo, error) {
	kp, ok := m.keyPairs.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "sshPublicKey %q not found", name)
	}

	if publicKey != nil {
		kp.PublicKey = *publicKey
	}

	if tags != nil {
		kp.Tags = copyTags(tags)
	}

	m.keyPairs.Set(name, kp)

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
