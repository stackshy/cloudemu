package compute

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
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
}

// RunInstances launches instances, creating a VNIC for each in the requested
// subnet and a boot volume from the image.
//
//nolint:gocritic // hugeParam: the driver interface fixes the signature.
func (m *Mock) RunInstances(ctx context.Context, cfg driver.InstanceConfig, count int) ([]driver.Instance, error) {
	if err := validateLaunch(cfg, count); err != nil {
		return nil, err
	}

	net := m.networking()
	mon := m.monitoring()
	out := make([]driver.Instance, 0, count)

	for range count {
		// The VNIC is created before m.mu is taken: the VCN mock locks itself
		// and this must not hold Compute's lock across another service.
		at, err := m.place(ctx, net, cfg.SubnetID, cfg.Tags[TagDisplayName], "", cfg.SecurityGroups, cfg.Tags)
		if err != nil {
			return nil, err
		}

		inst := m.storeInstance(cfg, at)
		out = append(out, toInstance(inst))

		m.emitMetrics(ctx, mon, inst.ID, runningMetrics)
	}

	return out, nil
}

// validateLaunch rejects a launch CloudEmu's OCI mock cannot honor. KeyName
// is refused rather than dropped: OCI models no key pair resource, so a
// caller naming one would otherwise get an instance with no key on it.
//
//nolint:gocritic // hugeParam: mirrors RunInstances.
func validateLaunch(cfg driver.InstanceConfig, count int) error {
	if count <= 0 {
		return cerrors.New(cerrors.InvalidArgument, "count must be greater than 0")
	}

	if count > maxLaunchCount {
		return cerrors.Newf(cerrors.InvalidArgument,
			"count %d exceeds the maximum of %d per call", count, maxLaunchCount)
	}

	if cfg.KeyName != "" {
		return cerrors.New(cerrors.InvalidArgument,
			"OCI has no key pair resource; pass the public key as the ssh_authorized_keys instance metadata entry")
	}

	if cfg.Priority != "" && cfg.Priority != prioritySpot && cfg.Priority != priorityRegular {
		return cerrors.Newf(cerrors.InvalidArgument,
			"unsupported priority %q: OCI offers %q (preemptible) and %q", cfg.Priority, prioritySpot, priorityRegular)
	}

	return nil
}

// storeInstance writes an instance and everything OCI creates with it: the
// boot volume, its attachment, and the VNIC attachment holding the VNIC.
//
//nolint:gocritic // hugeParam: mirrors RunInstances.
func (m *Mock) storeInstance(cfg driver.InstanceConfig, at placement) *instanceData {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := m.newOCID(typeInstance)
	ad := firstOr(cfg.Zones, m.defaultAD())

	inst := &instanceData{
		ID:              id,
		ImageID:         cfg.ImageID,
		Shape:           cfg.InstanceType,
		State:           compute.StateRunning,
		PrivateIP:       at.PrivateIP,
		PublicIP:        at.PublicIP,
		SubnetID:        at.SubnetID,
		VCNID:           at.VCNID,
		NSGIDs:          copyStrings(cfg.SecurityGroups),
		SecurityListIDs: copyStrings(at.SecurityListIDs),
		Tags:            copyTags(cfg.Tags),
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
		SourceDetails:      SourceDetails{SourceType: sourceImage, ID: cfg.ImageID},
		IsPreemptible:      cfg.Priority == prioritySpot,
		VNICID:             at.VNICID,
	}

	details.BootVolumeID = m.newBootVolume(ad, cfg.ImageID, details.DisplayName)
	m.attachBootVolume(id, details.BootVolumeID, ad)

	if at.VNICID != "" {
		m.addVNICAttachment(id, at, details.DisplayName)
	}

	m.details.Set(id, details)

	return inst
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
		[]string{compute.StateStopped}, []string{compute.StateRunning}, runningMetrics)
}

// StopInstances stops running instances.
func (m *Mock) StopInstances(ctx context.Context, instanceIDs []string) error {
	return m.transition(ctx, instanceIDs, compute.StateStopped, "stop",
		[]string{compute.StateRunning}, []string{compute.StateStopped}, stoppedMetrics)
}

// RebootInstances resets instances, OCI's RESET and SOFTRESET actions.
func (m *Mock) RebootInstances(ctx context.Context, instanceIDs []string) error {
	return m.transition(ctx, instanceIDs, compute.StateRunning, "reset",
		[]string{compute.StateRunning, compute.StateStopped}, nil, runningMetrics)
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

	vnicID, err := m.removeInstance(id, preserveBootVolume)
	if err != nil {
		return err
	}

	unplace(ctx, net, vnicID)
	m.emitMetrics(ctx, m.monitoring(), id, stoppedMetrics)

	return nil
}

// removeInstance drops an instance and the resources OCI takes down with it,
// returning the VNIC to delete once m.mu is released.
func (m *Mock) removeInstance(id string, preserveBootVolume bool) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.instances.Has(id) {
		return "", instanceNotFound(id)
	}

	details, _ := m.details.Get(id)

	for _, a := range m.volAttach.All() {
		if a.InstanceID == id {
			m.volAttach.Delete(a.ID)
			m.forget(a.ID)
			m.markVolumeDetached(a.VolumeID)
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

	return details.VNICID, nil
}

// transition moves instances into a state, rejecting the transitions OCI
// refuses. idempotent names the states where the action is a documented no-op.
func (m *Mock) transition(
	ctx context.Context, instanceIDs []string, to, verb string, from, idempotent []string, metrics []float64,
) error {
	mon := m.monitoring()
	moved := make([]string, 0, len(instanceIDs))

	if err := m.applyTransition(instanceIDs, to, verb, from, idempotent, &moved); err != nil {
		return err
	}

	for _, id := range moved {
		m.emitMetrics(ctx, mon, id, metrics)
	}

	return nil
}

func (m *Mock) applyTransition(instanceIDs []string, to, verb string, from, idempotent []string, moved *[]string) error {
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

		if !contains(from, inst.State) {
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
	_ context.Context, instanceIDs []string, filters []driver.DescribeFilter, _ ...driver.DescribeInstancesOptions,
) ([]driver.Instance, error) {
	if err := validateFilters(filters); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	all := describeResources(m.instances, instanceIDs, func(v *instanceData) *instanceData { return v })
	out := make([]driver.Instance, 0, len(all))

	for _, inst := range all {
		if matchesFilters(inst, filters) {
			out = append(out, toInstance(inst))
		}
	}

	return out, nil
}

// ModifyInstance changes an instance's shape and freeform tags, OCI's
// UpdateInstance. Reshaping requires a stopped instance, as OCI does.
func (m *Mock) ModifyInstance(_ context.Context, instanceID string, input driver.ModifyInstanceInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return instanceNotFound(instanceID)
	}

	if input.InstanceType != "" && input.InstanceType != inst.Shape && inst.State != compute.StateStopped {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"instance %q must be stopped to change shape", instanceID)
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
