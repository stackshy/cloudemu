package ec2

import (
	"context"
	"crypto/ed25519"
	"crypto/md5" //nolint:gosec // AWS defines imported key-pair fingerprints as MD5 digests of the public key
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // AWS defines RSA key-pair fingerprints as SHA-1 digests
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/internal/statemachine"
	"github.com/stackshy/cloudemu/v2/services/compute"
	"github.com/stackshy/cloudemu/v2/services/compute/computeengine"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

var _ driver.Compute = (*Mock)(nil)

// The AWS EC2 mock also serves these optional AWS-only capabilities, which the
// wire handler discovers by type assertion (no Azure/GCP/OCI mirror).
var (
	_ driver.SnapshotCopier  = (*Mock)(nil)
	_ driver.ImageRegistrar  = (*Mock)(nil)
	_ driver.KeyPairImporter = (*Mock)(nil)
	_ driver.VolumeModifier  = (*Mock)(nil)
)

const (
	ipSegmentSize  = 256
	stateAvailable = "available"
	stateCreating  = "creating"
	stateInUse     = "in-use"

	// visibilityHidden and visibilityVisible are the account-level
	// managed-resource-visibility settings.
	visibilityHidden  = "hidden"
	visibilityVisible = "visible"

	// awsAccountID is the fixed owner account for resources this mock creates,
	// echoed as ownerId on volumes/snapshots/images.
	awsAccountID = "123456789012"
	// defaultEBSKeyID is the stub KMS key id used when an encrypted volume is
	// created without an explicit KmsKeyId (real EC2 substitutes the account's
	// default EBS key).
	defaultEBSKeyID = "abcd1234-a123-456a-a12b-a123b4cd56ef"
	// imageRootDeviceSize is the default root EBS volume size (GiB) recorded in
	// an AMI's block device mapping when created from a running instance.
	imageRootDeviceSize = 8
	// rsaKeyBits is the modulus size for generated RSA key pairs.
	rsaKeyBits = 2048
	// keyTypeRSA / keyTypeEd25519 are the two key-pair algorithms the mock
	// generates and imports.
	keyTypeRSA     = "rsa"
	keyTypeEd25519 = "ed25519"

	// copiedSnapshotVolumeID is the placeholder volume id real EC2 reports for a
	// snapshot created by CopySnapshot, which has no owning volume.
	copiedSnapshotVolumeID = "vol-ffffffff"

	// monitoringDisabled / monitoringEnabled are the stored detailed-monitoring
	// states for an instance.
	monitoringDisabled = "disabled"
	monitoringEnabled  = "enabled"

	// subnetFirstHost is the first assignable host offset within a subnet CIDR.
	// AWS reserves the first four addresses (.0-.3) of every subnet, so host
	// allocation starts at .4.
	subnetFirstHost = 4

	// reservationPrefix is the AWS reservation-id prefix (r-xxxx).
	reservationPrefix = "r-"
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
	// disableAPITermination is the termination-protection flag set via
	// ModifyInstanceAttribute(DisableApiTermination). When true, real EC2
	// rejects TerminateInstances with OperationNotPermitted.
	disableAPITermination bool
	// sourceDestCheck mirrors the instance's source/destination check flag
	// (default true), toggled via ModifyInstanceAttribute(SourceDestCheck).
	sourceDestCheck bool
	// userData is the base64 user-data blob set via
	// ModifyInstanceAttribute(UserData) and read back by
	// DescribeInstanceAttribute(userData).
	userData string
	// ebsOptimized is the EBS-optimized flag toggled via
	// ModifyInstanceAttribute(EbsOptimized).
	ebsOptimized bool
	// reservationID groups all instances launched by one RunInstances call under
	// a shared AWS reservation (r-xxxx).
	reservationID string
	// keyName is the key pair the instance was launched with, echoed as keyName.
	keyName string
	// monitoring is the CloudWatch detailed-monitoring state
	// ("disabled"/"enabled"); defaults to "disabled" at launch.
	monitoring string
	// settle overlays a post-launch "pending" window over the stored (running)
	// State on the Describe surface when AsyncSettle is enabled; zero-value
	// (default) reports the stored State immediately.
	settle settle.Window
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
	// templateVersions holds every launch-template version keyed by
	// "<name>#<version>" (see templateVersionKey). Launch-template versioning is
	// AWS-specific, so this store backs the LaunchTemplateVersioner capability.
	templateVersions *memstore.Store[*driver.LaunchTemplateVersion]
	volumes          *memstore.Store[*driver.VolumeInfo]
	// volSettle overlays a "creating" window over a volume's stored (available)
	// State on the Describe surface, keyed by volume id. It lives beside volumes
	// rather than on the shared driver.VolumeInfo struct (which azure/gcp also
	// use), keeping settling AWS-internal. Empty/absent = report State directly.
	volSettle   *memstore.Store[settle.Window]
	snapshots   *memstore.Store[*driver.SnapshotInfo]
	images      *memstore.Store[*driver.ImageInfo]
	keyPairs    *memstore.Store[*driver.KeyPairInfo]
	sm          *statemachine.Machine
	opts        *config.Options
	ipCounter   atomic.Int64
	volCounter  atomic.Int64
	snapCounter atomic.Int64
	amiCounter  atomic.Int64
	keyCounter  atomic.Int64
	monitoring  mondriver.Monitoring
	// subnetResolver derives an instance's VPC from its subnet at launch, so
	// instances created with a --subnet-id carry the VPCID that connectivity
	// analysis and VPC teardown depend on. nil until wired by the provider.
	subnetResolver SubnetResolver
	// mu guards managedResourceVisibility, clientTokens and subnetIPCounters,
	// which are scalar/map shared state that (unlike the memstores) has no
	// internal locking of their own.
	mu sync.RWMutex
	// managedResourceVisibility is "visible" or "hidden". When "hidden",
	// service-provider-managed instances are omitted from DescribeInstances
	// unless the request opts in with IncludeManagedResources.
	managedResourceVisibility string
	// clientTokens maps a RunInstances ClientToken to the instance ids it
	// launched, so a retry with the same token returns those instances instead
	// of double-provisioning (AWS idempotency). Permanent — an emulator needs no
	// expiry window.
	clientTokens map[string][]string
	// subnetIPCounters is the per-subnet host counter used to allocate private
	// IPv4 addresses from the subnet's CIDR range.
	subnetIPCounters map[string]int
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
		instances:        memstore.New[*instanceData](),
		asgs:             memstore.New[*asgData](),
		spotRequests:     memstore.New[*driver.SpotInstanceRequest](),
		templates:        memstore.New[*driver.LaunchTemplate](),
		templateVersions: memstore.New[*driver.LaunchTemplateVersion](),
		volumes:          memstore.New[*driver.VolumeInfo](),
		volSettle:        memstore.New[settle.Window](),
		snapshots:        memstore.New[*driver.SnapshotInfo](),
		images:           memstore.New[*driver.ImageInfo](),
		keyPairs:         memstore.New[*driver.KeyPairInfo](),
		sm:               statemachine.New(compute.VMTransitions()),
		opts:             opts,

		managedResourceVisibility: visibilityVisible,
		clientTokens:              make(map[string][]string),
		subnetIPCounters:          make(map[string]int),
	}
}

func (m *Mock) nextIP() string {
	n := m.ipCounter.Add(1)
	return fmt.Sprintf("10.0.%d.%d", n/ipSegmentSize, n%ipSegmentSize)
}

// toInstance converts stored instance data to the driver shape. hidden is the
// account-level managed-resource-visibility ("hidden") resolved once by the
// caller, so this never touches m.mu (avoiding nested read locks).
func toInstance(d *instanceData, hidden bool, now time.Time) driver.Instance {
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
		ID: d.ID, ImageID: d.ImageID, InstanceType: d.InstanceType, State: d.settle.Observe(now, d.State),
		PrivateIP: d.PrivateIP, PublicIP: d.PublicIP, SubnetID: d.SubnetID, VPCID: d.VPCID,
		SecurityGroups: sg, Tags: tags, LaunchTime: d.LaunchTime,
		ReservationID: d.reservationID, KeyName: d.keyName, Monitoring: d.monitoring,
		Operator: operator,
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

	// Idempotency: a retry carrying a ClientToken we've already served returns the
	// same instances rather than launching new ones (real EC2 RunInstances).
	if dup, ok := m.instancesForClientToken(cfg.ClientToken); ok {
		return dup, nil
	}

	results := make([]driver.Instance, 0, count)
	hidden := m.visibility() == visibilityHidden

	// One reservation groups every instance launched by this call (AWS r-xxxx).
	reservationID := idgen.GenerateID(reservationPrefix)

	// Resolve the target subnet once so both the VPC id and the CIDR-scoped
	// private-IP allocation reuse a single lookup.
	vpcID, subnetCIDR := m.resolveSubnet(ctx, cfg.SubnetID)

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
			State: compute.StatePending, PrivateIP: m.allocatePrivateIP(cfg.SubnetID, subnetCIDR),
			SubnetID: cfg.SubnetID, VPCID: vpcID,
			SecurityGroups: sg, Tags: tags,
			LaunchTime:      m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
			sourceDestCheck: true,
			reservationID:   reservationID,
			keyName:         cfg.KeyName,
			userData:        cfg.UserData,
			monitoring:      monitoringDisabled,
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
		inst.settle = settle.Pending(compute.StatePending, m.opts.Clock.Now(),
			m.opts.SettleDuration(settle.DefaultInstanceSettle))
		results = append(results, toInstance(inst, hidden, m.opts.Clock.Now()))
		created = append(created, inst)

		// Managed (service-owned) instances are hidden from Describe, so
		// emitting instance-dimensioned CloudWatch metrics for them would leak
		// their existence. Suppress metrics for managed instances.
		if !isManaged(inst) {
			m.emitInstanceMetrics(ctx, id, inst.LaunchTime)
		}
	}

	m.recordClientToken(cfg.ClientToken, created)

	return results, nil
}

// instancesForClientToken returns the instances previously launched under token
// (rendered fresh from the store) when token is non-empty and already recorded.
func (m *Mock) instancesForClientToken(token string) ([]driver.Instance, bool) {
	if token == "" {
		return nil, false
	}

	m.mu.RLock()
	ids, ok := m.clientTokens[token]
	m.mu.RUnlock()

	if !ok {
		return nil, false
	}

	hidden := m.visibility() == visibilityHidden
	out := make([]driver.Instance, 0, len(ids))

	for _, id := range ids {
		if inst, found := m.instances.Get(id); found {
			out = append(out, toInstance(inst, hidden, m.opts.Clock.Now()))
		}
	}

	return out, true
}

// recordClientToken remembers which instances a ClientToken launched so a retry
// is served idempotently. A no-op for the empty token.
func (m *Mock) recordClientToken(token string, created []*instanceData) {
	if token == "" {
		return
	}

	ids := make([]string, 0, len(created))
	for _, inst := range created {
		ids = append(ids, inst.ID)
	}

	m.mu.Lock()
	m.clientTokens[token] = ids
	m.mu.Unlock()
}

// resolveSubnet returns the VPC that owns subnetID and the subnet's CIDR block
// in a single resolver lookup. Both are "" when there is no subnet, no resolver,
// or the subnet can't be found.
func (m *Mock) resolveSubnet(ctx context.Context, subnetID string) (vpcID, cidr string) {
	if subnetID == "" || m.subnetResolver == nil {
		return "", ""
	}

	subs, err := m.subnetResolver.DescribeSubnets(ctx, []string{subnetID})
	if err != nil || len(subs) == 0 {
		return "", ""
	}

	return subs[0].VPCID, subs[0].CIDRBlock
}

// allocatePrivateIP hands out the next private IPv4 inside the subnet's CIDR
// (AWS allocates from the target subnet, e.g. 10.0.1.0/24 -> 10.0.1.x). It falls
// back to the global 10.0.<n> pool when there is no subnet CIDR to draw from.
func (m *Mock) allocatePrivateIP(subnetID, cidr string) string {
	if subnetID == "" || cidr == "" {
		return m.nextIP()
	}

	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return m.nextIP()
	}

	base := ipNet.IP.To4()
	if base == nil {
		return m.nextIP()
	}

	m.mu.Lock()
	offset := m.subnetIPCounters[subnetID] + subnetFirstHost
	m.subnetIPCounters[subnetID]++
	m.mu.Unlock()

	host := make(net.IP, len(base))
	copy(host, base)

	// Add offset into the low-order bytes of the network address.
	carry := offset
	for i := len(host) - 1; i >= 0 && carry > 0; i-- {
		sum := int(host[i]) + carry
		host[i] = byte(sum % ipSegmentSize) //nolint:gosec // sum%256 is always in [0,255]
		carry = sum / ipSegmentSize
	}

	if !ipNet.Contains(host) {
		return m.nextIP()
	}

	return host.String()
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
		// A lifecycle transition supersedes any post-launch settle window, so the
		// instance reports its new terminal state rather than a stale "pending".
		inst.settle = settle.Window{}

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
	// Termination protection: real EC2 rejects the whole call with
	// OperationNotPermitted if any target has DisableApiTermination set.
	for _, id := range instanceIDs {
		if inst, ok := m.instances.Get(id); ok && inst.disableAPITermination {
			return cerrors.Newf(cerrors.PermissionDenied,
				"instance %q may not be terminated: termination protection is enabled", id)
		}
	}

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

		if matchesFilters(inst, filters, m.opts.Clock.Now()) {
			results = append(results, toInstance(inst, hidden, m.opts.Clock.Now()))
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

func matchesFilters(inst *instanceData, filters []driver.DescribeFilter, now time.Time) bool {
	for _, f := range filters {
		if !matchesSingleFilter(inst, f, now) {
			return false
		}
	}

	return true
}

func matchesSingleFilter(inst *instanceData, f driver.DescribeFilter, now time.Time) bool {
	switch f.Name {
	case "instance-id":
		return containsValue(f.Values, inst.ID)
	case "instance-type":
		return containsValue(f.Values, inst.InstanceType)
	case "instance-state-name":
		// Filter on the observed state so it agrees with the state Describe
		// renders (both go through the settle overlay under AsyncSettle).
		return containsValue(f.Values, inst.settle.Observe(now, inst.State))
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

// Instance-attribute names honored by SetInstanceAttribute / GetInstanceAttribute.
// These match the ModifyInstanceAttribute / DescribeInstanceAttribute attribute
// element names on the AWS wire.
const (
	attrDisableAPITermination = "disableApiTermination"
	attrSourceDestCheck       = "sourceDestCheck"
	attrInstanceType          = "instanceType"
	attrUserData              = "userData"
	attrEbsOptimized          = "ebsOptimized"
	attrMonitoring            = "monitoring"
)

// SetInstanceAttribute updates a single instance attribute in place. It backs
// the AWS wire ModifyInstanceAttribute for attributes (DisableApiTermination,
// SourceDestCheck) that real EC2 permits on a running instance and that are not
// part of the portable Compute driver. Unlike ModifyInstance it does not
// require the instance to be stopped.
func (m *Mock) SetInstanceAttribute(_ context.Context, instanceID, name, value string) error {
	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	switch name {
	case attrDisableAPITermination:
		inst.disableAPITermination = parseBool(value)
	case attrSourceDestCheck:
		inst.sourceDestCheck = parseBool(value)
	case attrEbsOptimized:
		inst.ebsOptimized = parseBool(value)
	case attrUserData:
		inst.userData = value
	case attrMonitoring:
		inst.monitoring = value
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported instance attribute %q", name)
	}

	return nil
}

// parseBool interprets an attribute value as a boolean, treating an unparsable
// value as false (real EC2 rejects malformed values upstream at the wire layer).
func parseBool(value string) bool {
	b, _ := strconv.ParseBool(value)

	return b
}

// SetInstanceSecurityGroups overwrites an instance's security-group membership,
// backing ModifyInstanceAttribute(Groups) for VPC instances. It replaces the set
// rather than merging, matching AWS's GroupId.N semantics.
func (m *Mock) SetInstanceSecurityGroups(_ context.Context, instanceID string, groupIDs []string) error {
	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	sg := make([]string, len(groupIDs))
	copy(sg, groupIDs)
	inst.SecurityGroups = sg

	return nil
}

// GetInstanceAttribute reads a single instance attribute, backing the AWS wire
// DescribeInstanceAttribute so ModifyInstanceAttribute changes are verifiable.
func (m *Mock) GetInstanceAttribute(_ context.Context, instanceID, name string) (string, error) {
	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return "", cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	switch name {
	case attrDisableAPITermination:
		return strconv.FormatBool(inst.disableAPITermination), nil
	case attrSourceDestCheck:
		return strconv.FormatBool(inst.sourceDestCheck), nil
	case attrEbsOptimized:
		return strconv.FormatBool(inst.ebsOptimized), nil
	case attrInstanceType:
		return inst.InstanceType, nil
	case attrUserData:
		return inst.userData, nil
	case attrMonitoring:
		return inst.monitoring, nil
	default:
		return "", cerrors.Newf(cerrors.InvalidArgument, "unsupported instance attribute %q", name)
	}
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
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
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

	kmsKeyID := cfg.KmsKeyID
	if cfg.Encrypted && kmsKeyID == "" {
		kmsKeyID = idgen.AWSARN("kms", m.opts.Region, awsAccountID, "key/"+defaultEBSKeyID)
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
		Encrypted:        cfg.Encrypted,
		SnapshotID:       cfg.SnapshotID,
		KmsKeyID:         kmsKeyID,
	}

	m.volumes.Set(id, vol)

	now := m.opts.Clock.Now()
	window := settle.Pending(stateCreating, now, m.opts.SettleDuration(settle.DefaultVolumeSettle))
	m.volSettle.Set(id, window)

	result := *vol
	result.State = window.Observe(now, result.State)

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
	m.volSettle.Delete(id)

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

	out := describeResources(m.volumes, ids)

	now := m.opts.Clock.Now()

	for i := range out {
		if w, ok := m.volSettle.Get(out[i].ID); ok {
			out[i].State = w.Observe(now, out[i].State)
		}
	}

	return out, nil
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
		OwnerID:     awsAccountID,
		Progress:    "100%",
		Encrypted:   vol.Encrypted,
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

// DescribeSnapshots returns snapshots matching the given IDs. An explicit ID
// that does not exist yields InvalidSnapshot.NotFound, matching real EC2.
func (m *Mock) DescribeSnapshots(_ context.Context, ids []string) ([]driver.SnapshotInfo, error) {
	for _, id := range ids {
		if !m.snapshots.Has(id) {
			return nil, cerrors.Newf(cerrors.NotFound, "snapshot %q not found", id)
		}
	}

	return describeResources(m.snapshots, ids), nil
}

// CreateImage creates a machine image from an instance.
func (m *Mock) CreateImage(_ context.Context, cfg driver.ImageConfig) (*driver.ImageInfo, error) {
	if _, ok := m.instances.Get(cfg.InstanceID); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "instance %q not found", cfg.InstanceID)
	}

	id := fmt.Sprintf("ami-%012d", m.amiCounter.Add(1))
	now := m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z")

	// A real CreateImage snapshots the instance's root volume and references
	// that snapshot from the AMI's block device mapping.
	snapID := fmt.Sprintf("snap-%012d", m.snapCounter.Add(1))
	m.snapshots.Set(snapID, &driver.SnapshotInfo{
		ID:          snapID,
		State:       "completed",
		Description: "Created by CreateImage for " + id,
		Size:        imageRootDeviceSize,
		CreatedAt:   now,
		OwnerID:     awsAccountID,
		Progress:    "100%",
	})

	img := &driver.ImageInfo{
		ID:                 id,
		Name:               cfg.Name,
		State:              stateAvailable,
		Description:        cfg.Description,
		CreatedAt:          now,
		Tags:               copyTags(cfg.Tags),
		OwnerID:            awsAccountID,
		Architecture:       "x86_64",
		RootDeviceType:     "ebs",
		RootDeviceName:     "/dev/sda1",
		VirtualizationType: "hvm",
		Hypervisor:         "xen",
		ImageType:          "machine",
		PlatformDetails:    "Linux/UNIX",
		BlockDeviceMappings: []driver.ImageBlockDeviceMapping{{
			DeviceName:          "/dev/sda1",
			SnapshotID:          snapID,
			VolumeSize:          imageRootDeviceSize,
			VolumeType:          "gp2",
			DeleteOnTermination: true,
		}},
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

// DescribeImages returns images matching the given IDs. An explicit ID that
// does not exist yields InvalidAMIID.NotFound, matching real EC2.
func (m *Mock) DescribeImages(_ context.Context, ids []string) ([]driver.ImageInfo, error) {
	for _, id := range ids {
		if !m.images.Has(id) {
			return nil, cerrors.Newf(cerrors.NotFound, "image %q not found", id)
		}
	}

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
		keyType = keyTypeRSA
	}

	mat, err := generateKeyMaterial(keyType)
	if err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "generate key material: %v", err)
	}

	kp := &driver.KeyPairInfo{
		ID:          fmt.Sprintf("key-%016x", m.keyCounter.Add(1)),
		Name:        cfg.Name,
		Fingerprint: mat.fingerprint,
		KeyType:     keyType,
		PublicKey:   mat.publicPEM,
		PrivateKey:  mat.privatePEM,
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

	result := make([]driver.KeyPairInfo, 0, len(names))

	for _, name := range names {
		kp, ok := m.keyPairs.Get(name)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "key pair %q not found", name)
		}

		cp := *kp
		cp.PrivateKey = ""
		result = append(result, cp)
	}

	return result, nil
}

// CopySnapshot clones an existing snapshot into a new snap-id, matching AWS EC2
// CopySnapshot. The single-region emulator ignores SourceRegion but still
// requires the source snapshot to exist.
//
//nolint:gocritic // hugeParam: capability method signature is fixed by the interface.
func (m *Mock) CopySnapshot(_ context.Context, in driver.CopySnapshotInput) (*driver.SnapshotInfo, error) {
	src, ok := m.snapshots.Get(in.SourceSnapshotID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "snapshot %q not found", in.SourceSnapshotID)
	}

	id := fmt.Sprintf("snap-%012d", m.snapCounter.Add(1))

	snap := &driver.SnapshotInfo{
		ID: id,
		// A copied snapshot is not associated with the source volume, so real EC2
		// reports the decoupled placeholder rather than the source's volume id;
		// inheriting it would make a volume-id snapshot filter return the copy too.
		VolumeID:    copiedSnapshotVolumeID,
		State:       "completed",
		Description: in.Description,
		Size:        src.Size,
		CreatedAt:   m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Tags:        copyTags(in.Tags),
		OwnerID:     awsAccountID,
		Progress:    "100%",
		Encrypted:   src.Encrypted || in.Encrypted,
	}

	m.snapshots.Set(id, snap)

	result := *snap

	return &result, nil
}

// RegisterImage registers an AMI from caller-supplied block device mappings,
// matching AWS EC2 RegisterImage. Names are unique per account; a referenced
// snapshot must exist.
//
//nolint:gocritic // hugeParam: capability method signature is fixed by the interface.
func (m *Mock) RegisterImage(_ context.Context, in driver.RegisterImageInput) (*driver.ImageInfo, error) {
	if in.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "image name must not be empty")
	}

	for _, img := range m.images.All() {
		if img.Name == in.Name {
			return nil, cerrors.Newf(cerrors.AlreadyExists, "image name %q is already in use", in.Name)
		}
	}

	for _, b := range in.BlockDeviceMappings {
		if b.SnapshotID != "" && !m.snapshots.Has(b.SnapshotID) {
			return nil, cerrors.Newf(cerrors.NotFound, "snapshot %q not found", b.SnapshotID)
		}
	}

	img := &driver.ImageInfo{
		ID:                  fmt.Sprintf("ami-%012d", m.amiCounter.Add(1)),
		Name:                in.Name,
		State:               stateAvailable,
		Description:         in.Description,
		CreatedAt:           m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Tags:                copyTags(in.Tags),
		OwnerID:             awsAccountID,
		Architecture:        nonEmpty(in.Architecture, "x86_64"),
		RootDeviceType:      "ebs",
		RootDeviceName:      nonEmpty(in.RootDeviceName, "/dev/sda1"),
		VirtualizationType:  nonEmpty(in.VirtualizationType, "hvm"),
		Hypervisor:          "xen",
		ImageType:           "machine",
		PlatformDetails:     "Linux/UNIX",
		BlockDeviceMappings: append([]driver.ImageBlockDeviceMapping(nil), in.BlockDeviceMappings...),
	}

	m.images.Set(img.ID, img)

	result := *img

	return &result, nil
}

// ImportKeyPair stores an externally-generated public key, matching AWS EC2
// ImportKeyPair. Imported keys carry an MD5 fingerprint of the DER-encoded
// public key (distinct from CreateKeyPair's SHA-1 private-key fingerprint) and
// never expose private-key material.
func (m *Mock) ImportKeyPair(_ context.Context, in driver.ImportKeyPairInput) (*driver.KeyPairInfo, error) {
	if in.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "key pair name must not be empty")
	}

	if _, ok := m.keyPairs.Get(in.Name); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "key pair %q already exists", in.Name)
	}

	fingerprint, keyType, err := importedKeyFingerprint(in.PublicKeyMaterial)
	if err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid public key material: %v", err)
	}

	kp := &driver.KeyPairInfo{
		ID:          fmt.Sprintf("key-%016x", m.keyCounter.Add(1)),
		Name:        in.Name,
		Fingerprint: fingerprint,
		KeyType:     keyType,
		PublicKey:   string(in.PublicKeyMaterial),
		CreatedAt:   m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Tags:        copyTags(in.Tags),
	}

	m.keyPairs.Set(in.Name, kp)

	result := *kp

	return &result, nil
}

// ModifyVolume applies an elastic-volume change (size / IOPS / throughput /
// type), matching AWS EC2 ModifyVolume. Size may only grow. Original values are
// captured before mutation and returned alongside the requested targets.
func (m *Mock) ModifyVolume(_ context.Context, in driver.ModifyVolumeInput) (*driver.VolumeModification, error) {
	vol, ok := m.volumes.Get(in.VolumeID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "volume %q not found", in.VolumeID)
	}

	orig := *vol

	targetSize := orig.Size
	if in.Size != 0 {
		targetSize = in.Size
	}

	if targetSize < orig.Size {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"New size %d GiB is smaller than current size %d GiB", targetSize, orig.Size)
	}

	targetType := nonEmpty(in.VolumeType, orig.VolumeType)

	targetIOPS := orig.IOPS
	if in.IOPS != 0 {
		targetIOPS = in.IOPS
	}

	targetThroughput := orig.Throughput
	if in.Throughput != 0 {
		targetThroughput = in.Throughput
	}

	vol.Size = targetSize
	vol.VolumeType = targetType
	vol.IOPS = targetIOPS
	vol.Throughput = targetThroughput

	return &driver.VolumeModification{
		VolumeID:           in.VolumeID,
		ModificationState:  "modifying",
		StartTime:          m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Progress:           0,
		OriginalSize:       orig.Size,
		OriginalIOPS:       orig.IOPS,
		OriginalThroughput: orig.Throughput,
		OriginalVolumeType: orig.VolumeType,
		TargetSize:         targetSize,
		TargetIOPS:         targetIOPS,
		TargetThroughput:   targetThroughput,
		TargetVolumeType:   targetType,
	}, nil
}

// importedKeyFingerprint computes the AWS ImportKeyPair fingerprint. It accepts
// OpenSSH ("ssh-rsa AAAA...") and PEM (PKIX or PKCS1) public-key material, and
// fingerprints per AWS's per-algorithm rule (see importedPublicKeyFingerprint).
func importedKeyFingerprint(material []byte) (fingerprint, keyType string, err error) {
	if pub, _, _, _, perr := ssh.ParseAuthorizedKey(material); perr == nil {
		cpk, ok := pub.(ssh.CryptoPublicKey)
		if !ok {
			return "", "", cerrors.New(cerrors.InvalidArgument, "unsupported public key type")
		}

		return importedPublicKeyFingerprint(cpk.CryptoPublicKey())
	}

	if block, _ := pem.Decode(material); block != nil {
		if key, kerr := x509.ParsePKIXPublicKey(block.Bytes); kerr == nil {
			return importedPublicKeyFingerprint(key)
		}

		if key, kerr := x509.ParsePKCS1PublicKey(block.Bytes); kerr == nil {
			return importedPublicKeyFingerprint(key)
		}
	}

	return "", "", cerrors.New(cerrors.InvalidArgument, "unrecognized public key format")
}

// importedPublicKeyFingerprint marshals a parsed public key to PKIX DER and
// fingerprints it the way real EC2 ImportKeyPair does: an imported ed25519 key
// yields the base64-encoded SHA-256 digest of the DER, while an imported RSA key
// yields the MD5 digest of the DER as colon-separated hex.
func importedPublicKeyFingerprint(pub any) (fingerprint, keyType string, err error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", "", err
	}

	if _, ok := pub.(ed25519.PublicKey); ok {
		sum := sha256.Sum256(der)

		return base64.StdEncoding.EncodeToString(sum[:]), keyTypeEd25519, nil
	}

	sum := md5.Sum(der) //nolint:gosec // AWS defines imported RSA key fingerprints as MD5 of the public key DER

	return colonHex(sum[:]), keyTypeRSA, nil
}

// keyMaterial bundles the PEM-encoded key pair and its fingerprint.
type keyMaterial struct {
	privatePEM  string
	publicPEM   string
	fingerprint string
}

// generateKeyMaterial returns a PEM-encoded key pair and its fingerprint. RSA
// fingerprints are the SHA-1 digest of the DER private key as colon-separated
// hex, matching CreateKeyPair on real EC2; ed25519 keys use a SHA-256 digest.
func generateKeyMaterial(keyType string) (keyMaterial, error) {
	if keyType == keyTypeEd25519 {
		pub, priv, gerr := ed25519.GenerateKey(rand.Reader)
		if gerr != nil {
			return keyMaterial{}, gerr
		}

		der, merr := x509.MarshalPKCS8PrivateKey(priv)
		if merr != nil {
			return keyMaterial{}, merr
		}

		sum := sha256.Sum256(der)

		return keyMaterial{
			privatePEM:  pemEncode("PRIVATE KEY", der),
			publicPEM:   pkixPublicPEM(pub),
			fingerprint: colonHex(sum[:]),
		}, nil
	}

	key, gerr := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if gerr != nil {
		return keyMaterial{}, gerr
	}

	der := x509.MarshalPKCS1PrivateKey(key)
	sum := sha1.Sum(der) //nolint:gosec // AWS defines RSA key-pair fingerprints as SHA-1 digests

	return keyMaterial{
		privatePEM:  pemEncode("RSA PRIVATE KEY", der),
		publicPEM:   pkixPublicPEM(key.Public()),
		fingerprint: colonHex(sum[:]),
	}, nil
}

func pemEncode(blockType string, der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}))
}

// pkixPublicPEM PEM-encodes a public key, returning "" if it cannot be marshaled.
func pkixPublicPEM(pub any) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return ""
	}

	return pemEncode("PUBLIC KEY", der)
}

// nonEmpty returns s when it is non-empty, otherwise fallback.
func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}

	return s
}

// colonHex formats bytes as colon-separated lowercase hex (aa:bb:cc), the
// shape AWS uses for key fingerprints.
func colonHex(b []byte) string {
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02x", x)
	}

	return strings.Join(parts, ":")
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
