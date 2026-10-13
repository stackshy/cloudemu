package compute

import (
	"context"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/statemachine"
	"github.com/stackshy/cloudemu/v2/services/compute"
	"github.com/stackshy/cloudemu/v2/services/compute/computeengine"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// maxLaunchCount bounds one launch call, so an oversized count cannot drive an
// unbounded allocation.
const maxLaunchCount = 1000

// Metric values reported for a running and a stopped instance.
var (
	runningMetrics = []float64{25.0, 1024.0, 512.0, 100.0, 50.0} //nolint:gochecknoglobals // fixture
	stoppedMetrics = []float64{0.0, 0.0, 0.0, 0.0, 0.0}          //nolint:gochecknoglobals // fixture
)

// Priority values the portable InstanceConfig carries. Spot is OCI's
// preemptible instance.
const (
	prioritySpot    = "Spot"
	priorityRegular = "Regular"
)

// defaultBootVolumeGBs is the boot volume size OCI's platform images ship.
const defaultBootVolumeGBs = 50

type instanceData struct {
	ID        string
	ImageID   string
	Shape     string
	State     string
	PrivateIP string
	PublicIP  string
	SubnetID  string
	VCNID     string
	NSGIDs    []string
	// SecurityListIDs are the subnet's security lists. OCI evaluates them
	// alongside the VNIC's NSGs, so both are reported as the instance's
	// security groups and connectivity analysis sees the union.
	SecurityListIDs []string
	Tags            map[string]string
	LaunchTime      string
	OSType          string
	Priority        string
	LicenseType     string
	AD              string
	Managed         bool
	Principal       string
	// EngineBacked is true when a real config.ComputeEngine backs this
	// instance, so its address came from the engine and terminating it has to
	// tear the backing down. Exported so it survives a snapshot.
	EngineBacked bool
}

// RunInstances launches instances, creating a VNIC for each in the requested
// subnet and a boot volume from the image.
//
//nolint:gocritic // hugeParam: the driver interface fixes the signature.
func (m *Mock) RunInstances(ctx context.Context, cfg driver.InstanceConfig, count int) ([]driver.Instance, error) {
	// count is caller-controlled and sizes the allocations below, so it is
	// bounded here rather than in validateLaunch: the guard has to dominate
	// the allocation it protects.
	if count <= 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "count must be greater than 0")
	}

	if count > maxLaunchCount {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"count %d exceeds the maximum of %d per call", count, maxLaunchCount)
	}

	if err := validateLaunch(cfg); err != nil {
		return nil, err
	}

	if err := m.resolveLaunchSource(cfg); err != nil {
		return nil, err
	}

	net := m.networking()
	mon := m.monitoring()
	out := make([]driver.Instance, 0, count)
	// created holds what this batch has already built, so a failure part-way
	// through tears it down instead of orphaning instances, VNICs and boot
	// volumes: real OCI launches the whole batch or none of it.
	created := make([]*instanceData, 0, count)

	for range count {
		// The VNIC is created before m.mu is taken: the VCN mock locks itself
		// and this must not hold Compute's lock across another service.
		at, err := m.place(ctx, net, cfg.SubnetID, cfg.Tags[TagDisplayName], "", cfg.SecurityGroups, cfg.Tags)
		if err != nil {
			m.rollbackLaunch(ctx, net, created)

			return nil, err
		}

		inst, err := m.storeInstance(cfg, at)
		if err != nil {
			unplace(ctx, net, at.VNICID)
			m.rollbackLaunch(ctx, net, created)

			return nil, err
		}

		created = append(created, inst)

		if err := m.provision(ctx, &cfg, inst); err != nil {
			m.rollbackLaunch(ctx, net, created)

			return nil, err
		}

		out = append(out, toInstance(inst))

		m.emitMetrics(ctx, mon, inst.ID, m.compartmentOf(inst.ID), runningMetrics)
	}

	return out, nil
}

// provision backs the instance with the configured compute engine, which runs
// the instance's UserData and may surface a reachable address that replaces
// the synthetic one. A no-op when no engine is wired.
func (m *Mock) provision(ctx context.Context, cfg *driver.InstanceConfig, inst *instanceData) error {
	engine := m.opts.ComputeEngine
	if engine == nil {
		return nil
	}

	m.mu.RLock()
	di := driver.Instance{ID: inst.ID, ImageID: inst.ImageID, PrivateIP: inst.PrivateIP}
	m.mu.RUnlock()

	// The engine is a caller-supplied backend, so it runs without m.mu held.
	if err := computeengine.Provision(ctx, engine, &di, cfg); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	inst.PrivateIP = di.PrivateIP
	inst.EngineBacked = true

	return nil
}

// rollbackLaunch best-effort undoes the instances a failed batch already
// created: each engine backing is torn down and each instance is dropped with
// the boot volume, attachments and VNIC it was launched with. A boot volume
// the caller launched from was theirs before the launch and is kept.
func (m *Mock) rollbackLaunch(ctx context.Context, net Networking, created []*instanceData) {
	for _, inst := range created {
		d, _ := m.InstanceDetails(inst.ID)

		vnicID, backed, err := m.removeInstance(inst.ID, d.SourceDetails.SourceType == sourceBootVolume)
		if err != nil {
			continue
		}

		if backed {
			_ = computeengine.Deprovision(ctx, m.opts.ComputeEngine, &driver.Instance{ID: inst.ID})
		}

		unplace(ctx, net, vnicID)
	}
}

// validateLaunch rejects a launch CloudEmu's OCI mock cannot honor. KeyName
// is refused rather than dropped: OCI models no key pair resource, so a
// caller naming one would otherwise get an instance with no key on it.
//
//nolint:gocritic // hugeParam: mirrors RunInstances.
func validateLaunch(cfg driver.InstanceConfig) error {
	if cfg.KeyName != "" {
		return cerrors.New(cerrors.InvalidArgument,
			"OCI has no key pair resource; pass the public key as the ssh_authorized_keys instance metadata entry")
	}

	if cfg.Priority != "" && cfg.Priority != prioritySpot && cfg.Priority != priorityRegular {
		return cerrors.Newf(cerrors.InvalidArgument,
			"unsupported priority %q: OCI offers %q (preemptible) and %q", cfg.Priority, prioritySpot, priorityRegular)
	}

	_, err := launchBootVolumeSize(cfg.Tags)

	return err
}

// launchBootVolumeSize reads the boot volume size a launch asked for, or
// OCI's default when it named none.
func launchBootVolumeSize(tags map[string]string) (int, error) {
	raw, ok := tags[TagBootVolumeSizeInGBs]
	if !ok {
		return defaultBootVolumeGBs, nil
	}

	size, err := strconv.Atoi(raw)
	if err != nil {
		return 0, cerrors.Newf(cerrors.InvalidArgument, "boot volume size %q is not a number", raw)
	}

	if err := checkBootVolumeSize(size); err != nil {
		return 0, err
	}

	return size, nil
}

// resolveLaunchSource checks a launch names a shape and a source that exist:
// an image, or the boot volume of a terminated instance. OCI answers 404 for
// either, so the library path refuses what the wire path does.
//
//nolint:gocritic // hugeParam: mirrors RunInstances.
func (m *Mock) resolveLaunchSource(cfg driver.InstanceConfig) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.shapes.Has(cfg.InstanceType) {
		return cerrors.Newf(cerrors.NotFound, "shape %q not found", cfg.InstanceType)
	}

	if isBootVolumeID(cfg.ImageID) {
		return m.bootVolumeFree(cfg.ImageID)
	}

	if !m.images.Has(cfg.ImageID) {
		return cerrors.Newf(cerrors.NotFound, "image %q not found", cfg.ImageID)
	}

	return nil
}

// isBootVolumeID reports whether a launch source names a boot volume rather
// than an image.
func isBootVolumeID(id string) bool {
	return strings.HasPrefix(id, "ocid1."+typeBootVolume+".")
}

// bootVolumeFree checks a boot volume exists and backs no instance, since a
// boot volume can be attached to only one. The caller holds m.mu.
func (m *Mock) bootVolumeFree(id string) error {
	if !m.bootVolumes.Has(id) {
		return bootVolumeNotFound(id)
	}

	for _, a := range m.bootAttach.All() {
		if a.BootVolumeID == id {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"boot volume %q is already attached to instance %q", id, a.InstanceID)
		}
	}

	return nil
}

// storeInstance writes an instance and everything OCI creates with it: the
// boot volume, its attachment, and the VNIC attachment holding the VNIC. A
// launch from a boot volume attaches that volume instead of creating one.
//
//nolint:gocritic // hugeParam: mirrors RunInstances.
func (m *Mock) storeInstance(cfg driver.InstanceConfig, at placement) (*instanceData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	source := SourceDetails{SourceType: sourceImage, ID: cfg.ImageID}
	imageID := cfg.ImageID

	if isBootVolumeID(cfg.ImageID) {
		if err := m.bootVolumeFree(cfg.ImageID); err != nil {
			return nil, err
		}

		bv, _ := m.bootVolumes.Get(cfg.ImageID)
		source = SourceDetails{SourceType: sourceBootVolume, ID: bv.ID}
		imageID = bv.ImageID
	}

	bootSize, err := launchBootVolumeSize(cfg.Tags)
	if err != nil {
		return nil, err
	}

	tags := copyTags(cfg.Tags)
	delete(tags, TagBootVolumeSizeInGBs)
	delete(tags, TagCompartmentID)

	id := m.newOCID(typeInstance)
	ad := firstOr(cfg.Zones, m.defaultAD())

	inst := &instanceData{
		ID:              id,
		ImageID:         imageID,
		Shape:           cfg.InstanceType,
		State:           compute.StateRunning,
		PrivateIP:       at.PrivateIP,
		PublicIP:        at.PublicIP,
		SubnetID:        at.SubnetID,
		VCNID:           at.VCNID,
		NSGIDs:          copyStrings(cfg.SecurityGroups),
		SecurityListIDs: copyStrings(at.SecurityListIDs),
		Tags:            tags,
		LaunchTime:      m.now(),
		OSType:          cfg.OSType,
		Priority:        cfg.Priority,
		LicenseType:     cfg.LicenseType,
		AD:              ad,
		Managed:         cfg.Managed,
		Principal:       cfg.Principal,
	}

	m.instances.Set(id, inst)
	m.record(id)

	details := InstanceDetails{
		DisplayName:        cfg.Tags[TagDisplayName],
		AvailabilityDomain: ad,
		Metadata:           launchMetadata(cfg.UserData),
		SourceDetails:      source,
		IsPreemptible:      cfg.Priority == prioritySpot,
		VNICID:             at.VNICID,
	}

	if source.SourceType == sourceBootVolume {
		details.BootVolumeID = source.ID
	} else {
		details.BootVolumeID = m.newBootVolume(ad, imageID, details.DisplayName, bootSize)
	}

	m.attachBootVolume(id, details.BootVolumeID, ad)

	if at.VNICID != "" {
		m.addVNICAttachment(id, at, details.DisplayName)
	}

	m.details.Set(id, details)

	if c := cfg.Tags[TagCompartmentID]; c != "" {
		for _, rid := range append(m.launchedIDsLocked(id), id) {
			m.scopes.Set(rid, scope.Scope{Compartment: c})
		}
	}

	return inst, nil
}

// launchMetadata carries the portable UserData into OCI's instance metadata,
// which is where cloud-init reads it from.
func launchMetadata(userData string) map[string]string {
	if userData == "" {
		return map[string]string{}
	}

	return map[string]string{"user_data": userData}
}

// StartInstances starts stopped instances. Starting a running one is a no-op,
// as OCI's START action is.
func (m *Mock) StartInstances(ctx context.Context, instanceIDs []string) error {
	return m.transition(ctx, instanceIDs, compute.StateRunning, "start",
		[]string{compute.StateRunning}, runningMetrics)
}

// StopInstances stops running instances. OCI's STOP action on an already
// stopped instance is an IncorrectState conflict rather than the no-op START
// is, so a stopped instance is rejected instead of skipped.
func (m *Mock) StopInstances(ctx context.Context, instanceIDs []string) error {
	return m.transition(ctx, instanceIDs, compute.StateStopped, "stop", nil, stoppedMetrics)
}

// RebootInstances resets instances, OCI's RESET and SOFTRESET actions.
func (m *Mock) RebootInstances(ctx context.Context, instanceIDs []string) error {
	return m.transition(ctx, instanceIDs, compute.StateRunning, "reset", nil, runningMetrics)
}

// TerminateInstances terminates instances, taking their boot volumes with
// them. Use TerminateInstance to keep the boot volume.
func (m *Mock) TerminateInstances(ctx context.Context, instanceIDs []string) error {
	for _, id := range instanceIDs {
		if err := m.TerminateInstance(ctx, id, false); err != nil {
			return err
		}
	}

	return nil
}

// TerminateInstance terminates one instance, deleting its VNIC and — unless
// the caller preserves it — its boot volume. It is the OCI-shaped terminate:
// the portable one cannot express preserveBootVolume.
func (m *Mock) TerminateInstance(ctx context.Context, id string, preserveBootVolume bool) error {
	net := m.networking()
	compartmentID := m.compartmentOf(id)

	vnicID, backed, err := m.removeInstance(id, preserveBootVolume)
	if err != nil {
		return err
	}

	unplace(ctx, net, vnicID)
	m.emitMetrics(ctx, m.monitoring(), id, compartmentID, stoppedMetrics)

	if backed {
		return computeengine.Deprovision(ctx, m.opts.ComputeEngine, &driver.Instance{ID: id})
	}

	return nil
}

// launchedResourceCount is what a launch creates besides the instance: the boot
// volume and its attachment, and the VNIC attachment.
const launchedResourceCount = 3

// LaunchedResourceIDs returns the resources OCI creates alongside an instance:
// its boot volume and the boot-volume and VNIC attachments. A wire caller
// places them in the instance's compartment, which a launch cannot know here.
func (m *Mock) LaunchedResourceIDs(instanceID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.launchedIDsLocked(instanceID)
}

// launchedIDsLocked is LaunchedResourceIDs for a caller holding m.mu.
func (m *Mock) launchedIDsLocked(instanceID string) []string {
	out := make([]string, 0, launchedResourceCount)

	if d, ok := m.details.Get(instanceID); ok && d.BootVolumeID != "" {
		out = append(out, d.BootVolumeID)
	}

	for _, a := range m.bootAttach.All() {
		if a.InstanceID == instanceID {
			out = append(out, a.ID)
		}
	}

	for _, a := range m.vnicAttach.All() {
		if a.InstanceID == instanceID {
			out = append(out, a.ID)
		}
	}

	return out
}

// GetConsoleOutput returns the console output the configured compute engine
// captured for the instance's boot script. It is empty when no engine backs
// the instance, which is every instance until one is wired.
func (m *Mock) GetConsoleOutput(ctx context.Context, instanceID string) ([]byte, error) {
	m.mu.RLock()
	inst, ok := m.instances.Get(instanceID)
	backed := ok && inst.EngineBacked
	m.mu.RUnlock()

	if !ok {
		return nil, instanceNotFound(instanceID)
	}

	if !backed {
		return nil, nil
	}

	return computeengine.ConsoleOutput(ctx, m.opts.ComputeEngine, instanceID)
}

// removeInstance drops an instance and the resources OCI takes down with it,
// returning the VNIC to delete and whether a real engine backed it, both to
// be acted on once m.mu is released.
func (m *Mock) removeInstance(id string, preserveBootVolume bool) (vnicID string, engineBacked bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(id)
	if !ok {
		return "", false, instanceNotFound(id)
	}

	details, _ := m.details.Get(id)

	for _, a := range m.volAttach.All() {
		if a.InstanceID == id {
			m.volAttach.Delete(a.ID)
			m.forget(a.ID)
			m.refreshVolumeAttachment(a.VolumeID)
		}
	}

	for _, a := range m.vnicAttach.All() {
		if a.InstanceID == id {
			m.vnicAttach.Delete(a.ID)
			m.forget(a.ID)
		}
	}

	for _, a := range m.bootAttach.All() {
		if a.InstanceID == id {
			m.bootAttach.Delete(a.ID)
			m.forget(a.ID)
		}
	}

	if !preserveBootVolume && details.BootVolumeID != "" {
		m.bootVolumes.Delete(details.BootVolumeID)
		m.forget(details.BootVolumeID)
	}

	m.instances.Delete(id)
	m.details.Delete(id)
	m.forget(id)

	return details.VNICID, inst.EngineBacked, nil
}

// vmTransitions are the portable VM lifecycle's legal moves, the one source of
// truth EC2 and Azure VMs check against too.
var vmTransitions = statemachine.NewTransitionMap(compute.VMTransitions()) //nolint:gochecknoglobals // immutable table

// reachable reports whether an instance can move from one state to another,
// directly or through the transient state OCI passes on the way (STOPPING,
// STARTING, a reset's reboot), which CloudEmu settles synchronously.
func reachable(from, to string) bool {
	if vmTransitions.IsAllowed(from, to) {
		return true
	}

	for _, via := range vmTransitions[from] {
		if vmTransitions.IsAllowed(via, to) {
			return true
		}
	}

	return false
}

// transition moves instances into a state, rejecting the transitions OCI
// refuses. idempotent names the states where the action is a documented no-op.
func (m *Mock) transition(
	ctx context.Context, instanceIDs []string, to, verb string, idempotent []string, metrics []float64,
) error {
	mon := m.monitoring()
	moved := make([]string, 0, len(instanceIDs))

	if err := m.applyTransition(instanceIDs, to, verb, idempotent, &moved); err != nil {
		return err
	}

	for _, id := range moved {
		m.emitMetrics(ctx, mon, id, m.compartmentOf(id), metrics)
	}

	return nil
}

func (m *Mock) applyTransition(instanceIDs []string, to, verb string, idempotent []string, moved *[]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, id := range instanceIDs {
		inst, ok := m.instances.Get(id)
		if !ok {
			return instanceNotFound(id)
		}

		if contains(idempotent, inst.State) {
			continue
		}

		if !reachable(inst.State, to) {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"cannot %s instance %q in state %q", verb, id, inst.State)
		}

		m.instances.Update(id, func(v *instanceData) *instanceData {
			v.State = to

			return v
		})

		*moved = append(*moved, id)
	}

	return nil
}

// DescribeInstances returns instances matching the given OCIDs, or all if
// empty, narrowed by filters.
func (m *Mock) DescribeInstances(
	ctx context.Context, instanceIDs []string, filters []driver.DescribeFilter, _ ...driver.DescribeInstancesOptions,
) ([]driver.Instance, error) {
	if err := validateFilters(filters); err != nil {
		return nil, err
	}

	net := m.networking()

	m.mu.RLock()

	all := describeResources(m.instances, instanceIDs, func(v *instanceData) *instanceData { return v })
	out := make([]driver.Instance, 0, len(all))
	nsgs := make([][]string, 0, len(all))

	for _, inst := range all {
		if matchesFilters(inst, filters) {
			out = append(out, toInstance(inst))
			nsgs = append(nsgs, copyStrings(inst.NSGIDs))
		}
	}

	m.mu.RUnlock()

	// OCI evaluates a subnet's security lists as they are now, so a list
	// added to the subnet after launch governs the instance too. The VCN mock
	// locks itself, so this runs after m.mu is released.
	for i := range out {
		if lists, ok := currentSecurityLists(ctx, net, out[i].SubnetID); ok {
			groups := make([]string, 0, len(nsgs[i])+len(lists))
			groups = append(groups, nsgs[i]...)
			groups = append(groups, lists...)
			out[i].SecurityGroups = groups
		}
	}

	return out, nil
}

// currentSecurityLists reads the security lists a subnet carries now. It
// reports false when there is no networking to ask or the subnet cannot be
// read, and the caller keeps what it recorded at launch.
func currentSecurityLists(ctx context.Context, net Networking, subnetID string) ([]string, bool) {
	if net == nil || subnetID == "" {
		return nil, false
	}

	subnets, err := net.DescribeSubnets(ctx, []string{subnetID})
	if err != nil || len(subnets) == 0 {
		return nil, false
	}

	return securityListsOf(net, &subnets[0]), true
}

// ModifyInstance changes an instance's shape and freeform tags, OCI's
// UpdateInstance. A running instance may be reshaped: OCI reboots it into the
// new shape, which CloudEmu settles before returning.
func (m *Mock) ModifyInstance(_ context.Context, instanceID string, input driver.ModifyInstanceInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.instances.Has(instanceID) {
		return instanceNotFound(instanceID)
	}

	if input.InstanceType != "" && !m.shapes.Has(input.InstanceType) {
		return cerrors.Newf(cerrors.NotFound, "shape %q not found", input.InstanceType)
	}

	m.instances.Update(instanceID, func(v *instanceData) *instanceData {
		if input.InstanceType != "" {
			v.Shape = input.InstanceType
		}

		if input.Tags != nil {
			v.Tags = copyTags(input.Tags)
		}

		return v
	})

	return nil
}

// InstanceDetails returns the OCI-only attributes of an instance.
func (m *Mock) InstanceDetails(id string) (InstanceDetails, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	d, ok := m.details.Get(id)

	return d, ok
}

// SetInstanceDetails records the OCI-only attributes a launch or update
// carried, which the portable InstanceConfig has no field for.
//
//nolint:gocritic // hugeParam: InstanceDetails is the value type being stored.
func (m *Mock) SetInstanceDetails(id string, d InstanceDetails) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	held, ok := m.details.Get(id)
	if !ok {
		return instanceNotFound(id)
	}

	// The launch-time facts the caller cannot change are carried over.
	d.BootVolumeID = held.BootVolumeID
	d.VNICID = held.VNICID
	m.details.Set(id, d)

	if d.AvailabilityDomain != "" {
		m.instances.Update(id, func(v *instanceData) *instanceData {
			v.AD = d.AvailabilityDomain

			return v
		})
	}

	return nil
}

// InstancesInCompartment returns the instances in a compartment, which is how
// OCI's ListInstances is scoped.
func (m *Mock) InstancesInCompartment(compartmentID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]string, 0)

	for _, inst := range m.instances.SortedValues() {
		if s, _ := m.scopes.Get(inst.ID); s.Compartment == compartmentID {
			out = append(out, inst.ID)
		}
	}

	return out
}

func toInstance(d *instanceData) driver.Instance {
	var operator *driver.OperatorInfo
	if d.Managed {
		operator = &driver.OperatorInfo{Managed: true, Principal: d.Principal}
	}

	groups := make([]string, 0, len(d.NSGIDs)+len(d.SecurityListIDs))
	groups = append(groups, d.NSGIDs...)
	groups = append(groups, d.SecurityListIDs...)

	zones := []string(nil)
	if d.AD != "" {
		zones = []string{d.AD}
	}

	return driver.Instance{
		ID:             d.ID,
		ImageID:        d.ImageID,
		InstanceType:   d.Shape,
		State:          d.State,
		PrivateIP:      d.PrivateIP,
		PublicIP:       d.PublicIP,
		SubnetID:       d.SubnetID,
		VPCID:          d.VCNID,
		SecurityGroups: groups,
		Tags:           copyTags(d.Tags),
		LaunchTime:     d.LaunchTime,
		OSType:         d.OSType,
		Priority:       d.Priority,
		LicenseType:    d.LicenseType,
		Zones:          zones,
		Operator:       operator,
	}
}

// validateFilters rejects a filter this mock does not evaluate, rather than
// returning every instance as though the caller had narrowed nothing.
func validateFilters(filters []driver.DescribeFilter) error {
	for _, f := range filters {
		switch {
		case strings.HasPrefix(f.Name, "tag:"):
		case f.Name == filterInstanceID, f.Name == filterShape,
			f.Name == filterState, f.Name == filterAD, f.Name == filterSubnet:
		default:
			return cerrors.Newf(cerrors.InvalidArgument, "unsupported instance filter %q", f.Name)
		}
	}

	return nil
}

// Filters DescribeInstances evaluates.
const (
	filterInstanceID = "instance-id"
	filterShape      = "shape"
	filterState      = "lifecycle-state"
	filterAD         = "availability-domain"
	filterSubnet     = "subnet-id"
)

func matchesFilters(inst *instanceData, filters []driver.DescribeFilter) bool {
	for _, f := range filters {
		if !matchesFilter(inst, f) {
			return false
		}
	}

	return true
}

func matchesFilter(inst *instanceData, f driver.DescribeFilter) bool {
	switch f.Name {
	case filterInstanceID:
		return contains(f.Values, inst.ID)
	case filterShape:
		return contains(f.Values, inst.Shape)
	case filterState:
		return contains(f.Values, inst.State)
	case filterAD:
		return contains(f.Values, inst.AD)
	case filterSubnet:
		return contains(f.Values, inst.SubnetID)
	default:
		return contains(f.Values, inst.Tags[strings.TrimPrefix(f.Name, "tag:")])
	}
}

func instanceNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "instance %q not found", id)
}

func notFoundf(format string, args ...any) error {
	return cerrors.Newf(cerrors.NotFound, format, args...)
}

func contains(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}

	return false
}

func firstOr(values []string, fallback string) string {
	if len(values) > 0 && values[0] != "" {
		return values[0]
	}

	return fallback
}
