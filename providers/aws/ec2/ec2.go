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

	// operationTypeRemove is the ModifySnapshotAttribute / ModifyImageAttribute
	// OperationType that revokes (rather than grants) a permission.
	operationTypeRemove = "remove"

	// visibilityHidden and visibilityVisible are the account-level
	// managed-resource-visibility settings.
	visibilityHidden  = "hidden"
	visibilityVisible = "visible"

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
	// mu guards every field below that is mutated after the instance is first
	// published to m.instances (State, settle, Tags, InstanceType,
	// SecurityGroups, VPCID and the ModifyInstanceAttribute-backed flags). Any
	// path that reads or writes those fields on a stored instance MUST hold mu
	// for the duration — memstore only makes the map lookup atomic, not the
	// pointed-to struct (see docs/architecture.md, "Concurrency & thread
	// safety"). The exemplar is providers/aws/sqs.queueData.mu.
	mu           sync.Mutex
	ID           string
	ImageID      string
	InstanceType string
	// State is the readable instance state. The authoritative transition
	// validator is m.sm (internal/statemachine, itself locked); State is the
	// synchronized mirror rendered by Describe, and is only ever written under
	// mu in lockstep with an m.sm transition.
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
	// disableAPIStop is the stop-protection flag set via
	// ModifyInstanceAttribute(DisableApiStop), read back by
	// DescribeInstanceAttribute(disableApiStop). Defaults false.
	disableAPIStop bool
	// shutdownBehavior is the instance-initiated shutdown behavior
	// ("stop"/"terminate") set via ModifyInstanceAttribute; empty means the
	// default "stop".
	shutdownBehavior string
	// reservationID groups all instances launched by one RunInstances call under
	// a shared AWS reservation (r-xxxx).
	reservationID string
	// keyName is the key pair the instance was launched with, echoed as keyName.
	keyName string
	// monitoring is the CloudWatch detailed-monitoring state
	// ("disabled"/"enabled"); defaults to "disabled" at launch.
	monitoring string
	// metadataOptions is the instance's IMDS configuration, defaulted at launch
	// and changed by ModifyInstanceMetadataOptions.
	metadataOptions driver.MetadataOptions
	// iamInstanceProfile is the IAM instance profile attached at launch (resolved
	// ARN + ID), nil when the instance was launched without one.
	iamInstanceProfile *driver.IamInstanceProfile
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

// terminationProtected reports the DisableApiTermination flag under mu.
func (d *instanceData) terminationProtected() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.disableAPITermination
}

// readState returns the instance's current lifecycle state under mu.
func (d *instanceData) readState() string {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.State
}

// isEngineBacked reports whether a real compute engine backs this instance,
// read under mu.
func (d *instanceData) isEngineBacked() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.engineBacked
}

// clearEngineBacked drops the engine-backed flag under mu, after the backing
// has been deprovisioned.
func (d *instanceData) clearEngineBacked() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.engineBacked = false
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
	volSettle *memstore.Store[settle.Window]
	snapshots *memstore.Store[*driver.SnapshotInfo]
	images    *memstore.Store[*driver.ImageInfo]
	keyPairs  *memstore.Store[*driver.KeyPairInfo]
	// placementGroups holds EC2 placement groups keyed by group name. Placement
	// groups are AWS-specific, so this store backs the PlacementGroups capability.
	placementGroups *memstore.Store[*driver.PlacementGroup]
	// iamProfileAssociations holds IAM instance-profile associations keyed by
	// association id (iip-assoc-...). Each launched-with-a-profile or post-launch
	// AssociateIamInstanceProfile call records one here, backing the
	// IamInstanceProfileAssociator capability. AWS-specific.
	iamProfileAssociations *memstore.Store[*iamProfileAssociation]
	pgCounter              atomic.Int64
	sm                     *statemachine.Machine
	opts                   *config.Options
	ipCounter              atomic.Int64
	volCounter             atomic.Int64
	snapCounter            atomic.Int64
	amiCounter             atomic.Int64
	keyCounter             atomic.Int64
	monitoring             mondriver.Monitoring
	// subnetResolver derives an instance's VPC from its subnet at launch, so
	// instances created with a --subnet-id carry the VPCID that connectivity
	// analysis and VPC teardown depend on. nil until wired by the provider.
	subnetResolver SubnetResolver
	// instanceProfileResolver resolves an IamInstanceProfile reference (Arn/Name)
	// supplied to RunInstances into the profile's canonical ARN and ID, so the
	// role->profile->instance chain reads back on DescribeInstances. nil until
	// wired by the provider.
	instanceProfileResolver InstanceProfileResolver
	// networking materializes an instance's primary (eth0) ENI at launch and
	// releases it on terminate, so the VPC subnet / security-group delete guards
	// see a running instance's interface. nil until wired by the provider.
	networking Networking
	// mu guards managedResourceVisibility, clientTokens, clientTokenInflight and
	// subnetIPCounters, which are scalar/map shared state that (unlike the
	// memstores) has no internal locking of their own.
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
	// clientTokenInflight tracks ClientTokens whose launch is still provisioning,
	// so two concurrent RunInstances carrying the same token cannot both provision
	// (one reserves the token and launches; the others wait on its result). An
	// entry is removed once the launch completes and its ids move to clientTokens.
	clientTokenInflight map[string]*clientTokenLaunch
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

	// Backfill the 5 datapoints going backward from launch time so they land in
	// the recent past. Forward-dating would place them in the future, where a
	// GetMetricStatistics query ending at "now" filters them out.
	for i, metricName := range metrics {
		for j := 0; j < 5; j++ {
			ts := lt.Add(-time.Duration(j) * time.Minute)
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
		instances:              memstore.New[*instanceData](),
		asgs:                   memstore.New[*asgData](),
		spotRequests:           memstore.New[*driver.SpotInstanceRequest](),
		templates:              memstore.New[*driver.LaunchTemplate](),
		templateVersions:       memstore.New[*driver.LaunchTemplateVersion](),
		volumes:                memstore.New[*driver.VolumeInfo](),
		volSettle:              memstore.New[settle.Window](),
		snapshots:              memstore.New[*driver.SnapshotInfo](),
		images:                 memstore.New[*driver.ImageInfo](),
		keyPairs:               memstore.New[*driver.KeyPairInfo](),
		placementGroups:        memstore.New[*driver.PlacementGroup](),
		iamProfileAssociations: memstore.New[*iamProfileAssociation](),
		sm:                     statemachine.New(compute.VMTransitions()),
		opts:                   opts,

		managedResourceVisibility: visibilityVisible,
		clientTokens:              make(map[string][]string),
		clientTokenInflight:       make(map[string]*clientTokenLaunch),
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
	d.mu.Lock()
	defer d.mu.Unlock()

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

	var iamProfile *driver.IamInstanceProfile

	if d.iamInstanceProfile != nil {
		p := *d.iamInstanceProfile
		iamProfile = &p
	}

	return driver.Instance{
		ID: d.ID, ImageID: d.ImageID, InstanceType: d.InstanceType, State: d.settle.Observe(now, d.State),
		PrivateIP: d.PrivateIP, PublicIP: d.PublicIP, SubnetID: d.SubnetID, VPCID: d.VPCID,
		SecurityGroups: sg, Tags: tags, LaunchTime: d.LaunchTime,
		ReservationID: d.reservationID, KeyName: d.keyName, Monitoring: d.monitoring,
		Operator: operator, MetadataOptions: d.metadataOptions,
		IamInstanceProfile: iamProfile,
	}
}

// defaultMetadataOptions is the IMDS configuration a freshly launched instance
// carries, matching real EC2 defaults (IMDSv1+v2 optional, one hop, enabled).
func defaultMetadataOptions() driver.MetadataOptions {
	return driver.MetadataOptions{
		State:                   "applied",
		HTTPTokens:              "optional",
		HTTPPutResponseHopLimit: 1,
		HTTPEndpoint:            "enabled",
		HTTPProtocolIPv6:        "disabled",
		InstanceMetadataTags:    "disabled",
	}
}

// ModifyInstanceMetadataOptions updates an instance's IMDS settings
// (ec2:ModifyInstanceMetadataOptions). A zero-value field leaves that setting
// unchanged; it returns the resulting options.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) ModifyInstanceMetadataOptions(
	_ context.Context, instanceID string, update driver.MetadataOptions,
) (*driver.MetadataOptions, error) {
	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	inst.mu.Lock()
	defer inst.mu.Unlock()

	opts := inst.metadataOptions
	if opts.State == "" {
		opts = defaultMetadataOptions()
	}

	if update.HTTPTokens != "" {
		opts.HTTPTokens = update.HTTPTokens
	}

	if update.HTTPPutResponseHopLimit != 0 {
		opts.HTTPPutResponseHopLimit = update.HTTPPutResponseHopLimit
	}

	if update.HTTPEndpoint != "" {
		opts.HTTPEndpoint = update.HTTPEndpoint
	}

	if update.HTTPProtocolIPv6 != "" {
		opts.HTTPProtocolIPv6 = update.HTTPProtocolIPv6
	}

	if update.InstanceMetadataTags != "" {
		opts.InstanceMetadataTags = update.InstanceMetadataTags
	}

	opts.State = "applied"
	inst.metadataOptions = opts

	result := opts

	return &result, nil
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

	// Idempotency: a ClientToken serializes same-token launches so a retry (or a
	// concurrent duplicate) returns the same instances rather than provisioning a
	// second set. The empty token opts out.
	if cfg.ClientToken == "" {
		return m.launchInstances(ctx, cfg, count)
	}

	return m.runInstancesIdempotent(ctx, cfg, count)
}

// clientTokenLaunch is the shared result of an in-flight tokened RunInstances:
// waiters block on done, then read ids/err (written before done is closed).
type clientTokenLaunch struct {
	done chan struct{}
	ids  []string
	err  error
}

// runInstancesIdempotent provisions at most one instance set per ClientToken.
// The first caller reserves the token under m.mu and launches; concurrent
// callers with the same token find the reservation and wait on its result, and
// a later retry finds the recorded ids — so a token never provisions twice.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) runInstancesIdempotent(ctx context.Context, cfg driver.InstanceConfig, count int) ([]driver.Instance, error) {
	token := cfg.ClientToken

	m.mu.Lock()
	if ids, ok := m.clientTokens[token]; ok {
		m.mu.Unlock()

		return m.renderInstancesByID(ids), nil
	}

	if inflight, ok := m.clientTokenInflight[token]; ok {
		m.mu.Unlock()
		<-inflight.done

		if inflight.err != nil {
			return nil, inflight.err
		}

		return m.renderInstancesByID(inflight.ids), nil
	}

	launch := &clientTokenLaunch{done: make(chan struct{})}
	m.clientTokenInflight[token] = launch
	m.mu.Unlock()

	results, err := m.launchInstances(ctx, cfg, count)

	m.mu.Lock()
	delete(m.clientTokenInflight, token)

	if err == nil {
		ids := make([]string, len(results))
		for i := range results {
			ids[i] = results[i].ID
		}

		m.clientTokens[token] = ids
		launch.ids = ids
	} else {
		launch.err = err
	}
	m.mu.Unlock()
	close(launch.done)

	return results, err
}

// launchInstances does the actual provisioning for RunInstances once count and
// idempotency have already been validated.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) launchInstances(ctx context.Context, cfg driver.InstanceConfig, count int) ([]driver.Instance, error) {
	results := make([]driver.Instance, 0, count)
	hidden := m.visibility() == visibilityHidden

	// One reservation groups every instance launched by this call (AWS r-xxxx).
	reservationID := idgen.GenerateID(reservationPrefix)

	// Resolve the target subnet once so both the VPC id and the CIDR-scoped
	// private-IP allocation reuse a single lookup.
	vpcID, subnetCIDR := m.resolveSubnet(ctx, cfg.SubnetID)

	// Resolve the IamInstanceProfile reference once for the whole batch; every
	// instance launched by this call shares the same profile association. A
	// Name/ARN that doesn't resolve rejects the whole call before anything is
	// launched, matching real EC2.
	iamProfile, err := m.resolveInstanceProfile(ctx, &cfg)
	if err != nil {
		return nil, err
	}

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
			LaunchTime:         m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
			sourceDestCheck:    true,
			reservationID:      reservationID,
			keyName:            cfg.KeyName,
			userData:           cfg.UserData,
			monitoring:         monitoringDisabled,
			metadataOptions:    defaultMetadataOptions(),
			iamInstanceProfile: iamProfile,
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

		m.sm.SetState(id, compute.StatePending)
		_ = m.sm.Transition(id, compute.StateRunning)
		inst.State = compute.StateRunning
		inst.settle = settle.Pending(compute.StatePending, m.opts.Clock.Now(),
			m.opts.SettleDuration(settle.DefaultInstanceSettle))
		// Publish only after the instance is fully initialized so a concurrent
		// Describe can never observe a half-written State/settle.
		m.instances.Set(id, inst)
		results = append(results, toInstance(inst, hidden, m.opts.Clock.Now()))
		created = append(created, inst)

		// Record a backing profile association so DescribeIamInstanceProfileAssociations
		// lists a launched-with-a-profile instance, matching real EC2 (each instance
		// gets its own association id).
		if iamProfile != nil {
			m.recordProfileAssociation(id, iamProfile)
		}

		// Managed (service-owned) instances are hidden from Describe, so
		// emitting instance-dimensioned CloudWatch metrics for them would leak
		// their existence. Suppress metrics for managed instances.
		if !isManaged(inst) {
			m.emitInstanceMetrics(ctx, id, inst.LaunchTime)
		}

		// Materialize the primary (eth0) ENI real EC2 attaches at launch, so the
		// VPC subnet / security-group delete guards see this running instance's
		// interface. vpcID is non-empty only when the subnet resolved, which is
		// exactly when the networking mock can place the interface.
		if vpcID != "" {
			m.materializePrimaryENI(ctx, id, cfg.SubnetID, sg)
		}
	}

	return results, nil
}

// renderInstancesByID renders the current state of the given instance ids fresh
// from the store, skipping any that no longer exist. It backs the idempotent
// ClientToken paths so a retry reflects the instances' latest state.
func (m *Mock) renderInstancesByID(ids []string) []driver.Instance {
	hidden := m.visibility() == visibilityHidden
	out := make([]driver.Instance, 0, len(ids))

	for _, id := range ids {
		if inst, found := m.instances.Get(id); found {
			out = append(out, toInstance(inst, hidden, m.opts.Clock.Now()))
		}
	}

	return out
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

// materializePrimaryENI asks the networking mock to create the instance's eth0
// interface. It is best-effort: the interface is a side effect of the launch,
// not a precondition, so a networking hiccup must not fail an otherwise-good
// RunInstances (mirroring the metric emission above). A no-op until wired.
func (m *Mock) materializePrimaryENI(ctx context.Context, instanceID, subnetID string, groups []string) {
	if m.networking == nil {
		return
	}

	_ = m.networking.CreatePrimaryNetworkInterface(ctx, instanceID, subnetID, groups)
}

// releasePrimaryENI asks the networking mock to drop the instance's primary
// ENI, so a terminated instance no longer blocks DeleteSubnet /
// DeleteSecurityGroup. A no-op until wired.
func (m *Mock) releasePrimaryENI(ctx context.Context, instanceID string) {
	if m.networking == nil {
		return
	}

	_ = m.networking.ReleaseInstanceNetworkInterfaces(ctx, instanceID)
}

// disassociateInstanceAddresses asks the networking mock to disassociate (not
// release) any elastic IP bound to the terminated instance, so the address
// survives as allocated-but-unassociated. A no-op until wired.
func (m *Mock) disassociateInstanceAddresses(ctx context.Context, instanceID string) {
	if m.networking == nil {
		return
	}

	_ = m.networking.DisassociateInstanceAddresses(ctx, instanceID)
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
		// Drop the primary ENI this instance may have materialized, so a
		// rolled-back launch leaves no orphaned interface holding its subnet.
		m.releasePrimaryENI(ctx, inst.ID)
		// Drop any backing profile association so a rolled-back launch leaves no
		// orphaned association behind.
		m.deleteAssociationsForInstance(inst.ID)
	}
}

//nolint:gocritic // t is a small read-only config; copying once per call is fine.
func (m *Mock) transitionInstances(ctx context.Context, instanceIDs []string, t lifecycleTransition) error {
	for _, id := range instanceIDs {
		inst, ok := m.instances.Get(id)
		if !ok {
			return cerrors.Newf(cerrors.NotFound, "instance %q not found", id)
		}

		changed, err := m.transitionOne(inst, id, t)
		if err != nil {
			return err
		}

		// Managed instances are hidden from Describe; keep them out of metrics
		// too so a hidden instance isn't observable via CloudWatch. Emitted
		// outside inst.mu so a metrics callback can't deadlock against it.
		if changed && !isManaged(inst) {
			m.emitLifecycleMetrics(ctx, id, t.metricValues)
		}
	}

	return nil
}

// transitionOne applies one lifecycle transition to inst under its own lock,
// keeping inst.State in lockstep with the m.sm transition. It reports whether
// the state actually changed (false when the request was idempotent).
//
//nolint:gocritic // t is a small read-only config; copying once per call is fine.
func (m *Mock) transitionOne(inst *instanceData, id string, t lifecycleTransition) (bool, error) {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	// Real AWS EC2 documents Start/Stop as idempotent on the target state. Skip
	// the state machine and return success without changing state when we're
	// already there.
	if isIdempotent(inst.State, t.idempotentStates) {
		return false, nil
	}

	if err := m.sm.Transition(id, t.intermediateState); err != nil {
		return false, cerrors.Newf(cerrors.FailedPrecondition, "cannot %s instance %q: %v", t.errVerb, id, err)
	}

	inst.State = t.intermediateState
	_ = m.sm.Transition(id, t.finalState)
	inst.State = t.finalState
	// A lifecycle transition supersedes any post-launch settle window, so the
	// instance reports its new terminal state rather than a stale "pending".
	inst.settle = settle.Window{}

	return true, nil
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
		if inst, ok := m.instances.Get(id); ok && inst.terminationProtected() {
			return cerrors.Newf(cerrors.PermissionDenied,
				"instance %q may not be terminated: termination protection is enabled", id)
		}
	}

	if err := m.transitionInstances(ctx, instanceIDs, terminateTransition); err != nil {
		return err
	}

	// Every attached EBS volume must be released, otherwise it stays in-use
	// against a dead instance forever and can never be deleted (VolumeInUse).
	m.detachTerminatedVolumes(instanceIDs)

	// A terminated instance no longer holds its IAM instance profile, so drop any
	// backing association (real EC2 removes it, and the id stops appearing in
	// DescribeIamInstanceProfileAssociations).
	for _, id := range instanceIDs {
		m.deleteAssociationsForInstance(id)
	}

	// Release each instance's primary (eth0) ENI, matching real EC2's
	// delete-on-termination default. Until this happens the interface keeps
	// residing in its subnet and referencing its security groups, which would
	// (correctly) block a subsequent DeleteSubnet / DeleteSecurityGroup — but a
	// terminated instance must no longer hold them.
	for _, id := range instanceIDs {
		m.releasePrimaryENI(ctx, id)
		m.disassociateInstanceAddresses(ctx, id)
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
		if !ok || !inst.isEngineBacked() {
			continue
		}

		di := driver.Instance{ID: inst.ID}
		// Deprovision is a potentially blocking engine call, so it runs outside
		// inst.mu; only the flag flip is taken under the lock.
		if err := computeengine.Deprovision(ctx, engine, &di); err != nil {
			errs = append(errs, err)

			continue
		}

		inst.clearEngineBacked()
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

	if !inst.isEngineBacked() {
		return nil, nil
	}

	return computeengine.ConsoleOutput(ctx, m.opts.ComputeEngine, instanceID)
}

func (m *Mock) DescribeInstances(
	_ context.Context, instanceIDs []string, filters []driver.DescribeFilter, opts ...driver.DescribeInstancesOptions,
) ([]driver.Instance, error) {
	if err := validateInstanceFilters(filters); err != nil {
		return nil, err
	}

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
	// Filters read mutable fields (State, settle, InstanceType, Tags), so match
	// under the instance's own lock to stay consistent with concurrent writers.
	inst.mu.Lock()
	defer inst.mu.Unlock()

	for _, f := range filters {
		if matched, _ := instanceFilterMatch(inst, f, now); !matched {
			return false
		}
	}

	return true
}

// validateInstanceFilters rejects filter names DescribeInstances does not model,
// so an unknown filter errors (surfaced as InvalidParameterValue) instead of
// silently matching every instance. It probes a zero instance for each name and
// consults only the "known" result, so the accepted set can never drift from
// what instanceFilterMatch actually honors (mirrors the VPC-family handlers).
func validateInstanceFilters(filters []driver.DescribeFilter) error {
	var probe instanceData

	for _, f := range filters {
		if _, known := instanceFilterMatch(&probe, f, time.Time{}); !known {
			return cerrors.Newf(cerrors.InvalidArgument, "The filter '%s' is invalid", f.Name)
		}
	}

	return nil
}

// instanceFilterMatch reports whether inst satisfies filter f (values within one
// filter are OR-ed) and whether f is a filter DescribeInstances recognizes.
// Scalar filters resolve to a single instance field; tag filters are matched
// separately.
func instanceFilterMatch(inst *instanceData, f driver.DescribeFilter, now time.Time) (matched, known bool) {
	if field, ok := instanceFilterField(inst, f.Name, now); ok {
		return containsValue(f.Values, field), true
	}

	return matchesTagFilter(inst, f)
}

// instanceFilterField maps a scalar DescribeInstances filter name to the
// instance value it selects, and reports whether the name is one the emulator
// models. The derived private-dns-name/availability-zone/architecture values
// mirror what the wire layer renders so a filter agrees with the described
// instance; instance-lifecycle is empty because the emulator launches only
// on-demand instances (so spot/scheduled never match).
func instanceFilterField(inst *instanceData, name string, now time.Time) (field string, known bool) {
	// Filter on the observed state so instance-state-name agrees with the state
	// Describe renders (both go through the settle overlay under AsyncSettle).
	byName := map[string]string{
		"instance-id":         inst.ID,
		"instance-type":       inst.InstanceType,
		"instance-state-name": inst.settle.Observe(now, inst.State),
		"vpc-id":              inst.VPCID,
		"subnet-id":           inst.SubnetID,
		"image-id":            inst.ImageID,
		"private-ip-address":  inst.PrivateIP,
		"private-dns-name":    privateDNSNameFor(inst.PrivateIP),
		"key-name":            inst.keyName,
		"reservation-id":      inst.reservationID,
		"availability-zone":   instanceZone,
		"architecture":        instanceArchitecture,
		"instance-lifecycle":  "",
	}

	v, ok := byName[name]

	return v, ok
}

// instanceZone and instanceArchitecture are the fixed placement zone and CPU
// architecture the emulator reports for every EC2 instance; they mirror the
// values the wire layer renders (defaultZone / archX86) so a filter on them
// agrees with DescribeInstances output.
const (
	instanceZone         = "us-east-1a"
	instanceArchitecture = "x86_64"
)

// privateDNSNameFor derives the internal DNS name from a private IPv4
// (10.0.0.5 -> ip-10-0-0-5.ec2.internal), mirroring the wire layer's render so a
// private-dns-name filter matches the described instance. Empty for no IP.
func privateDNSNameFor(ip string) string {
	if ip == "" {
		return ""
	}

	return "ip-" + strings.ReplaceAll(ip, ".", "-") + ".ec2.internal"
}

// matchesTagFilter evaluates "tag:<key>" and "tag-key" filters, returning
// whether inst matches and whether f was a tag filter at all (so a non-tag name
// is reported unknown to validateInstanceFilters).
func matchesTagFilter(inst *instanceData, f driver.DescribeFilter) (matched, known bool) {
	switch {
	case len(f.Name) > 4 && f.Name[:4] == "tag:":
		tagVal, ok := inst.Tags[f.Name[4:]]

		return ok && containsValue(f.Values, tagVal), true
	case f.Name == "tag-key":
		for k := range inst.Tags {
			if containsValue(f.Values, k) {
				return true, true
			}
		}

		return false, true
	default:
		return false, false
	}
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

	inst.mu.Lock()
	defer inst.mu.Unlock()

	if inst.State != compute.StateStopped {
		return cerrors.Newf(cerrors.FailedPrecondition, "instance %q must be stopped to modify", instanceID)
	}

	if input.InstanceType != "" {
		inst.InstanceType = input.InstanceType
	}

	if input.Tags != nil {
		if inst.Tags == nil {
			inst.Tags = map[string]string{}
		}

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

	attrInstanceInitiatedShutdownBehavior = "instanceInitiatedShutdownBehavior"
	attrDisableAPIStop                    = "disableApiStop"

	// shutdownBehaviorStop is the default instanceInitiatedShutdownBehavior
	// value, matching real EC2 (the alternative is "terminate").
	shutdownBehaviorStop = "stop"
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

	inst.mu.Lock()
	defer inst.mu.Unlock()

	switch name {
	case attrDisableAPITermination:
		inst.disableAPITermination = parseBool(value)
	case attrSourceDestCheck:
		inst.sourceDestCheck = parseBool(value)
	case attrEbsOptimized:
		inst.ebsOptimized = parseBool(value)
	case attrDisableAPIStop:
		inst.disableAPIStop = parseBool(value)
	case attrInstanceInitiatedShutdownBehavior:
		inst.shutdownBehavior = value
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

	inst.mu.Lock()
	inst.SecurityGroups = sg
	inst.mu.Unlock()

	return nil
}

// GetInstanceAttribute reads a single instance attribute, backing the AWS wire
// DescribeInstanceAttribute so ModifyInstanceAttribute changes are verifiable.
func (m *Mock) GetInstanceAttribute(_ context.Context, instanceID, name string) (string, error) {
	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return "", cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	inst.mu.Lock()
	defer inst.mu.Unlock()

	if v, ok := inst.boolAttribute(name); ok {
		return strconv.FormatBool(v), nil
	}

	if v, ok := inst.stringAttribute(name); ok {
		return v, nil
	}

	return "", cerrors.Newf(cerrors.InvalidArgument, "unsupported instance attribute %q", name)
}

// boolAttribute returns the boolean instance attribute named by name and whether
// name is a boolean attribute. Caller holds inst.mu.
func (d *instanceData) boolAttribute(name string) (value, ok bool) {
	switch name {
	case attrDisableAPITermination:
		return d.disableAPITermination, true
	case attrSourceDestCheck:
		return d.sourceDestCheck, true
	case attrEbsOptimized:
		return d.ebsOptimized, true
	case attrDisableAPIStop:
		return d.disableAPIStop, true
	default:
		return false, false
	}
}

// stringAttribute returns the string instance attribute named by name and whether
// name is a string attribute. instanceInitiatedShutdownBehavior defaults to
// "stop" when unset. Caller holds inst.mu.
func (d *instanceData) stringAttribute(name string) (value string, ok bool) {
	switch name {
	case attrInstanceInitiatedShutdownBehavior:
		if d.shutdownBehavior == "" {
			return shutdownBehaviorStop, true
		}

		return d.shutdownBehavior, true
	case attrInstanceType:
		return d.InstanceType, true
	case attrUserData:
		return d.userData, true
	case attrMonitoring:
		return d.monitoring, true
	default:
		return "", false
	}
}

// SetInstanceVPC sets the VPC ID on an existing instance. This is a test
// helper since RunInstances does not automatically resolve VPC from subnet.
func (m *Mock) SetInstanceVPC(instanceID, vpcID string) error {
	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	inst.mu.Lock()
	inst.VPCID = vpcID
	inst.mu.Unlock()

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
		kmsKeyID = idgen.AWSARN("kms", m.opts.Region, m.opts.AccountID, "key/"+defaultEBSKeyID)
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
	// The target instance must exist and be in a state that can take an
	// attachment. Real EC2 rejects attaching to a pending/shutting-down/
	// terminated instance with IncorrectInstanceState — a volume can only
	// attach to a running or stopped instance.
	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	if state := inst.readState(); state != compute.StateRunning && state != compute.StateStopped {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"IncorrectInstanceState: instance %q is in state %q; a volume can only attach to a running or stopped instance",
			instanceID, state)
	}

	// Instances aren't placed in an explicit zone here, so they occupy the
	// region's default zone, which is what CreateVolume also defaults to.
	instZone := m.opts.Region + "a"

	// Atomic check-and-set: the "is this volume available?" test and the "mark
	// it attached" write happen together under the store lock, so two concurrent
	// attaches of the same volume have exactly one winner (the loser sees
	// VolumeInUse). The fn returns a fresh pointer (copy-on-write) so a
	// concurrent DescribeVolumes, which dereferences the stored pointer outside
	// the store lock, never races with the field writes.
	var attachErr error

	found := m.volumes.Update(volumeID, func(v *driver.VolumeInfo) *driver.VolumeInfo {
		if v.State == stateInUse {
			attachErr = cerrors.Newf(cerrors.FailedPrecondition,
				"VolumeInUse: volume %q is attached to instance %q", volumeID, v.AttachedTo)

			return v
		}

		// A volume can only attach to an instance in the SAME Availability Zone
		// (real EC2 InvalidVolume.ZoneMismatch); an explicit cross-AZ volume mismatches.
		if v.AvailabilityZone != "" && v.AvailabilityZone != instZone {
			attachErr = cerrors.Newf(cerrors.FailedPrecondition,
				"ZoneMismatch: volume %q is in availability zone %q but instance %q is in %q",
				volumeID, v.AvailabilityZone, instanceID, instZone)

			return v
		}

		cp := *v
		cp.State = stateInUse
		cp.AttachedTo = instanceID
		cp.Device = device

		return &cp
	})
	if !found {
		return cerrors.Newf(cerrors.NotFound, "volume %q not found", volumeID)
	}

	return attachErr
}

// DetachVolume detaches a volume from an instance. A non-empty instanceID or
// device must match the volume's current attachment; real EC2 answers
// InvalidAttachment.NotFound when the volume is not attached to the named
// instance/device.
func (m *Mock) DetachVolume(_ context.Context, volumeID, instanceID, device string) error {
	var detachErr error

	found := m.volumes.Update(volumeID, func(v *driver.VolumeInfo) *driver.VolumeInfo {
		if v.State != stateInUse {
			detachErr = cerrors.Newf(cerrors.FailedPrecondition, "volume %q is not attached", volumeID)

			return v
		}

		if (instanceID != "" && instanceID != v.AttachedTo) || (device != "" && device != v.Device) {
			detachErr = cerrors.Newf(cerrors.NotFound,
				"InvalidAttachment.NotFound: volume %q is not attached to instance %q as device %q",
				volumeID, instanceID, device)

			return v
		}

		cp := *v
		cp.State = stateAvailable
		cp.AttachedTo = ""
		cp.Device = ""

		return &cp
	})
	if !found {
		return cerrors.Newf(cerrors.NotFound, "volume %q not found", volumeID)
	}

	return detachErr
}

// detachTerminatedVolumes returns every EBS volume attached to a now-terminated
// instance to the `available` state (attachment cleared). Real EC2 detaches
// volumes with DeleteOnTermination=false back to available on terminate; user-
// attached volumes carry that default, so all of them detach here. Each volume
// is updated with a copy-on-write fresh pointer under the store lock so a
// concurrent DescribeVolumes/AttachVolume never races.
func (m *Mock) detachTerminatedVolumes(instanceIDs []string) {
	terminated := make(map[string]bool, len(instanceIDs))
	for _, id := range instanceIDs {
		terminated[id] = true
	}

	for _, volID := range m.volumes.Keys() {
		m.volumes.Update(volID, func(v *driver.VolumeInfo) *driver.VolumeInfo {
			if v.AttachedTo == "" || !terminated[v.AttachedTo] {
				return v
			}

			cp := *v
			cp.State = stateAvailable
			cp.AttachedTo = ""
			cp.Device = ""

			return &cp
		})
	}
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
		OwnerID:     m.opts.AccountID,
		Progress:    "100%",
		Encrypted:   vol.Encrypted,
	}

	m.snapshots.Set(id, snap)

	result := *snap

	return &result, nil
}

// DeleteSnapshot deletes a snapshot. A snapshot referenced by a registered
// AMI's block device mapping cannot be deleted until the AMI is deregistered
// (real EC2 InvalidSnapshot.InUse), so we guard against orphaning an AMI whose
// backing snapshot was removed.
func (m *Mock) DeleteSnapshot(_ context.Context, id string) error {
	if !m.snapshots.Has(id) {
		return cerrors.Newf(cerrors.NotFound, "snapshot %q not found", id)
	}

	if ami := m.imageUsingSnapshot(id); ami != "" {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"InUse: snapshot %q is currently in use by %s", id, ami)
	}

	m.snapshots.Delete(id)

	return nil
}

// imageUsingSnapshot returns the id of a registered AMI whose block device
// mapping references the snapshot, or "" if none does.
func (m *Mock) imageUsingSnapshot(snapshotID string) string {
	for _, img := range m.images.All() {
		for _, bdm := range img.BlockDeviceMappings {
			if bdm.SnapshotID == snapshotID {
				return img.ID
			}
		}
	}

	return ""
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

// ModifySnapshotAttribute adds or removes createVolumePermission grants on a
// snapshot (EC2 snapshot sharing). The grants persist so
// DescribeSnapshotVolumePermissions reads them back.
//
//nolint:dupl,gocritic // parallel snapshot/image attribute-modify; hugeParam interface signature is fixed
func (m *Mock) ModifySnapshotAttribute(_ context.Context, input driver.ModifySnapshotAttributeInput) error {
	snap, ok := m.snapshots.Get(input.SnapshotID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "snapshot %q not found", input.SnapshotID)
	}

	grants := make([]driver.SnapshotCreateVolumePermission, 0, len(input.Groups)+len(input.UserIDs))
	for _, g := range input.Groups {
		grants = append(grants, driver.SnapshotCreateVolumePermission{Group: g})
	}

	for _, u := range input.UserIDs {
		grants = append(grants, driver.SnapshotCreateVolumePermission{UserID: u})
	}

	if input.OperationType == operationTypeRemove {
		snap.CreateVolumePermissions = removePermissions(snap.CreateVolumePermissions, grants)
		return nil
	}

	snap.CreateVolumePermissions = addPermissions(snap.CreateVolumePermissions, grants)

	return nil
}

// DescribeSnapshotVolumePermissions returns the snapshot's persisted
// createVolumePermission grants.
func (m *Mock) DescribeSnapshotVolumePermissions(
	_ context.Context, snapshotID string,
) ([]driver.SnapshotCreateVolumePermission, error) {
	snap, ok := m.snapshots.Get(snapshotID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "snapshot %q not found", snapshotID)
	}

	return append([]driver.SnapshotCreateVolumePermission(nil), snap.CreateVolumePermissions...), nil
}

// addPermissions returns cur with each grant in add that it does not already
// hold appended. It is shared by snapshot createVolumePermission and image
// launchPermission (both comparable grant structs).
func addPermissions[T comparable](cur, add []T) []T {
	for _, g := range add {
		if !containsPermission(cur, g) {
			cur = append(cur, g)
		}
	}

	return cur
}

// removePermissions returns cur with every grant present in remove dropped.
func removePermissions[T comparable](cur, remove []T) []T {
	out := cur[:0]

	for _, g := range cur {
		if !containsPermission(remove, g) {
			out = append(out, g)
		}
	}

	return out
}

func containsPermission[T comparable](set []T, want T) bool {
	for _, g := range set {
		if g == want {
			return true
		}
	}

	return false
}

// CreateImage creates a machine image from an instance.
//
//nolint:gocritic // hugeParam: cfg mirrors the driver-interface signature.
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
		OwnerID:     m.opts.AccountID,
		Progress:    "100%",
	})

	img := &driver.ImageInfo{
		ID:                 id,
		Name:               cfg.Name,
		State:              stateAvailable,
		Description:        cfg.Description,
		CreatedAt:          now,
		Tags:               copyTags(cfg.Tags),
		OwnerID:            m.opts.AccountID,
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

// CopyImage creates a new AMI that is a copy of an existing source AMI
// (aws_ami_copy). The copy owns a fresh id and (optionally) a new name and
// description; all other attributes are inherited from the source.
func (m *Mock) CopyImage(_ context.Context, input driver.CopyImageInput) (*driver.ImageInfo, error) {
	src, ok := m.images.Get(input.SourceImageID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "image %q not found", input.SourceImageID)
	}

	id := fmt.Sprintf("ami-%012d", m.amiCounter.Add(1))

	copyImg := *src
	copyImg.ID = id
	copyImg.CreatedAt = m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z")
	copyImg.OwnerID = m.opts.AccountID
	copyImg.LaunchPermissions = nil
	copyImg.Tags = copyTags(input.Tags)

	copyImg.BlockDeviceMappings = append([]driver.ImageBlockDeviceMapping(nil), src.BlockDeviceMappings...)

	if input.Name != "" {
		copyImg.Name = input.Name
	}

	if input.Description != "" {
		copyImg.Description = input.Description
	}

	m.images.Set(id, &copyImg)

	result := copyImg

	return &result, nil
}

// ModifyImageAttribute adds or removes launchPermission grants on an AMI (AMI
// sharing). Grants persist so DescribeImageLaunchPermissions reads them back.
//
//nolint:dupl,gocritic // parallel snapshot/image attribute-modify; hugeParam interface signature is fixed
func (m *Mock) ModifyImageAttribute(_ context.Context, input driver.ModifyImageAttributeInput) error {
	img, ok := m.images.Get(input.ImageID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "image %q not found", input.ImageID)
	}

	grants := make([]driver.ImageLaunchPermission, 0, len(input.Groups)+len(input.UserIDs))
	for _, g := range input.Groups {
		grants = append(grants, driver.ImageLaunchPermission{Group: g})
	}

	for _, u := range input.UserIDs {
		grants = append(grants, driver.ImageLaunchPermission{UserID: u})
	}

	if input.OperationType == operationTypeRemove {
		img.LaunchPermissions = removePermissions(img.LaunchPermissions, grants)
		return nil
	}

	img.LaunchPermissions = addPermissions(img.LaunchPermissions, grants)

	return nil
}

// DescribeImageLaunchPermissions returns the AMI's persisted launchPermission grants.
func (m *Mock) DescribeImageLaunchPermissions(
	_ context.Context, imageID string,
) ([]driver.ImageLaunchPermission, error) {
	img, ok := m.images.Get(imageID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "image %q not found", imageID)
	}

	return append([]driver.ImageLaunchPermission(nil), img.LaunchPermissions...), nil
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

// DeleteKeyPair deletes a key pair by name. It is idempotent: deleting a key
// pair that does not exist still succeeds (real EC2 DeleteKeyPair returns
// <return>true</return> for a missing key), so Terraform destroy re-runs and
// cleanup scripts don't fail on an already-deleted key.
func (m *Mock) DeleteKeyPair(_ context.Context, name string) error {
	m.keyPairs.Delete(name)

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
		OwnerID:     m.opts.AccountID,
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
		OwnerID:             m.opts.AccountID,
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
