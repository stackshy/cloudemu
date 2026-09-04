// Package eks provides an in-memory mock of AWS EKS: the control plane
// (clusters, managed node groups, Fargate profiles, and add-ons) plus a live
// Kubernetes data plane. When a shared kubernetes.APIServer is wired in,
// CreateCluster registers a real in-memory apiserver and DescribeCluster
// advertises its endpoint + CA so client-go and kubectl operate end-to-end;
// without one, the Endpoint and CertificateAuthority fields fall back to
// placeholder values so kubeconfig generation still works syntactically.
//
// The mock implements eks/driver.EKS so the same backend serves both the
// SDK-compat HTTP handler in server/aws/eks and any direct programmatic
// access from Go test code.
package eks

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Wave 1 placeholder for the cluster API server endpoint. Wave 2 will swap
// in a real per-cluster apiserver address.
const (
	wavePlaceholderEndpoint  = "https://EKS-DATAPLANE-NOT-IMPLEMENTED.cloudemu.local"
	defaultPlatformVersion   = "eks.1"
	defaultKubernetesVersion = "1.29"
	namespaceEKS             = "AWS/EKS"

	// Managed-nodegroup defaults real EKS applies when the caller omits them.
	defaultNodegroupInstanceType = "t3.medium"
	defaultNodegroupAmiType      = "AL2_x86_64"
	defaultNodegroupDiskSize     = 20

	// Cluster networking and access-config defaults real EKS applies when the
	// caller omits them. authenticationMode defaults to CONFIG_MAP on the
	// EKS API / SDK / CloudFormation path (only the AWS console defaults to
	// API_AND_CONFIG_MAP); Terraform's aws_eks_cluster uses the SDK path.
	defaultServiceIPv4CIDR    = "10.100.0.0/16"
	defaultServiceIPv6CIDR    = "fd00::/108"
	ipFamilyIPv4              = "ipv4"
	ipFamilyIPv6              = "ipv6"
	defaultAuthenticationMode = "CONFIG_MAP"
)

// CloudWatch-style metric values emitted on cluster create. The numbers are
// arbitrary running-cluster defaults; the goal is for monitoring assertions
// to find populated datapoints, not to model real load.
const (
	metricClusterCPU       = 25.0
	metricClusterMemory    = 40.0
	metricClusterPods      = 5.0
	metricClusterNodes     = 2.0
	metricClusterAPIErrors = 0.0
)

var _ eksdriver.EKS = (*Mock)(nil)

// Mock is the in-memory AWS EKS implementation.
type Mock struct {
	mu sync.RWMutex

	clusters        *memstore.Store[eksdriver.Cluster]
	nodegroups      *memstore.Store[eksdriver.Nodegroup]
	fargateProfiles *memstore.Store[eksdriver.FargateProfile]
	addons          *memstore.Store[eksdriver.Addon]
	updates         *memstore.Store[eksdriver.ClusterUpdate]

	opts           *config.Options
	monitoring     mondriver.Monitoring
	subnetResolver SubnetResolver

	// k8sAPI is the shared in-memory Kubernetes data-plane server. When set,
	// CreateCluster registers a fresh ClusterState with it and DescribeCluster
	// returns an Endpoint pointing at that state. When nil, the Wave 1
	// behavior is preserved: Endpoint stays the *-DATAPLANE-NOT-IMPLEMENTED
	// sentinel.
	k8sAPI *kubernetes.APIServer
	// k8sUIDs maps cluster name → the UID we registered with k8sAPI.
	k8sUIDs map[string]string

	// clusterSettle overlays a transient CREATING/UPDATING window (keyed by
	// cluster name) over a cluster's stored ACTIVE status, matching real EKS
	// (CreateCluster / UpdateClusterVersion / UpdateClusterConfig -> transient
	// -> ACTIVE). It is a no-op unless config.Options.AsyncSettle is set
	// (SettleDuration returns 0 -> inactive window -> the historical
	// synchronous behavior). The Set has its own lock.
	clusterSettle *settle.Set
	// nodegroupSettle is the nodegroup analog of clusterSettle, keyed by
	// nodegroupKey(clusterName, nodegroupName).
	nodegroupSettle *settle.Set
}

// New creates a new AWS EKS mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:        memstore.New[eksdriver.Cluster](),
		nodegroups:      memstore.New[eksdriver.Nodegroup](),
		fargateProfiles: memstore.New[eksdriver.FargateProfile](),
		addons:          memstore.New[eksdriver.Addon](),
		updates:         memstore.New[eksdriver.ClusterUpdate](),
		opts:            opts,
		k8sUIDs:         make(map[string]string),
		clusterSettle:   settle.NewSet(),
		nodegroupSettle: settle.NewSet(),
	}
}

// SetMonitoring wires a CloudWatch-style backend for auto-metric emission.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// SetK8sAPI wires a shared in-memory Kubernetes data-plane server. After
// this is set, CreateCluster registers a fresh ClusterState with api and
// DescribeCluster returns an Endpoint that points at it. Pass the same
// *kubernetes.APIServer as awsserver.Drivers.K8sAPI when constructing the
// SDK-compat server, so kubeconfigs land on the right backend.
func (m *Mock) SetK8sAPI(api *kubernetes.APIServer) {
	m.mu.Lock()
	m.k8sAPI = api
	m.mu.Unlock()
}

// nodegroupKey uniquely identifies a nodegroup across clusters.
func nodegroupKey(clusterName, nodegroupName string) string {
	return clusterName + "/" + nodegroupName
}

// fargateKey uniquely identifies a Fargate profile across clusters.
func fargateKey(clusterName, profileName string) string {
	return clusterName + "/" + profileName
}

// addonKey uniquely identifies an add-on across clusters.
func addonKey(clusterName, addonName string) string {
	return clusterName + "/" + addonName
}

// stubCertificate returns the base64 PEM CA blob DescribeCluster advertises.
//
// It is a REAL self-signed certificate, not a placeholder string. The previous
// stub assumed "SDK clients only base64-decode it for the kubeconfig", which
// holds for the raw SDK but not for anything that then builds a TLS config:
// client-go calls AppendCertsFromPEM and fails with "unable to parse bytes as
// PEM block" — an error raised at kubernetes.NewForConfig, far from EKS, that
// gives no hint the CA was synthetic. Any tool deriving a kubeconfig from
// DescribeCluster (which is the documented way to reach an EKS cluster) hits
// this immediately.
//
// Generated once per process and cached: certificate generation is not free,
// and a stable CA across calls matches real EKS, where a cluster's CA does not
// change between DescribeCluster invocations.

// generateSelfSignedCA builds a throwaway CA certificate and returns it
// base64-encoded, matching the shape real EKS returns. Failure falls back to
// an empty string rather than panicking: an empty CA makes client-go use the
// system roots, which is a recoverable state, whereas a panic would take down
// an emulator whose whole purpose is to keep tests running.

func newUpdateID() string {
	var b [16]byte

	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read on Linux/macOS uses getrandom(2)/arc4random(3); both are
		// effectively infallible. Falling back to a fixed string keeps the
		// caller signature simple if it ever does fail.
		return "00000000-0000-0000-0000-000000000000"
	}

	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

// recordUpdate stores an update so DescribeUpdate/ListUpdates can find it later,
// returning a copy. Callers must hold m.mu.
func (m *Mock) recordUpdate(u *eksdriver.ClusterUpdate) *eksdriver.ClusterUpdate {
	m.updates.Set(u.ID, *u)

	out := *u

	return &out
}

func (m *Mock) clusterARN(region, name string) string {
	return idgen.AWSARN("eks", region, m.opts.AccountID, "cluster/"+name)
}

// arnRegion returns the region field of an EKS ARN
// (arn:aws:eks:<region>:<account>:<resource>), or fallback when the ARN is
// malformed. A cluster's stored ARN is the source of truth for the region of
// its child resources (nodegroups, Fargate profiles, addons), so a child always
// shares its cluster's region.
func arnRegion(arn, fallback string) string {
	const regionField, minFields = 3, 6

	parts := strings.Split(arn, ":")
	if len(parts) < minFields || parts[regionField] == "" {
		return fallback
	}

	return parts[regionField]
}

// oidcIssuer derives a stable OpenID Connect issuer URL for a cluster, matching
// the real EKS shape https://oidc.eks.<region>.amazonaws.com/id/<32-hex>. The
// id is a deterministic hash of account + cluster name so it does not change
// between DescribeCluster calls.
func (m *Mock) oidcIssuer(region, name string) string {
	sum := sha256.Sum256([]byte(m.opts.AccountID + "/" + name))
	id := strings.ToUpper(hex.EncodeToString(sum[:16]))

	return fmt.Sprintf("https://oidc.eks.%s.amazonaws.com/id/%s", region, id)
}

// clusterSecurityGroupID synthesizes the deterministic cluster security group
// id real EKS creates for a cluster ("sg-<hash>"). It is stable across
// DescribeCluster calls so downstream references (worker attach, SG rules) stay
// fixed.
func (m *Mock) clusterSecurityGroupID(name string) string {
	sum := sha256.Sum256([]byte("eks-cluster-sg/" + m.opts.AccountID + "/" + name))

	return "sg-" + hex.EncodeToString(sum[:8])
}

func (m *Mock) nodegroupARN(region, clusterName, nodegroupName string) string {
	return idgen.AWSARN("eks", region, m.opts.AccountID,
		"nodegroup/"+clusterName+"/"+nodegroupName)
}

func (m *Mock) fargateARN(region, clusterName, profileName string) string {
	return idgen.AWSARN("eks", region, m.opts.AccountID,
		"fargateprofile/"+clusterName+"/"+profileName)
}

func (m *Mock) addonARN(region, clusterName, addonName string) string {
	return idgen.AWSARN("eks", region, m.opts.AccountID,
		"addon/"+clusterName+"/"+addonName)
}

func copyTags(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}

	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}

func copyStrings(src []string) []string {
	if src == nil {
		return nil
	}

	out := make([]string, len(src))
	copy(out, src)

	return out
}

// allClusterLogTypes is the full EKS control-plane log-type set, in the order
// AWS reports them.
func allClusterLogTypes() []string {
	return []string{"api", "audit", "authenticator", "controllerManager", "scheduler"}
}

// resolveClusterLogging echoes the caller's cluster logging, or applies the AWS
// default when it is omitted: all control-plane log types present but disabled.
func resolveClusterLogging(in []eksdriver.ClusterLogging) []eksdriver.ClusterLogging {
	if len(in) == 0 {
		return []eksdriver.ClusterLogging{{Types: allClusterLogTypes(), Enabled: false}}
	}

	out := make([]eksdriver.ClusterLogging, 0, len(in))
	for _, l := range in {
		out = append(out, eksdriver.ClusterLogging{Types: copyStrings(l.Types), Enabled: l.Enabled})
	}

	return out
}

// resolveNetworkConfig fills in the EKS networking defaults: ipFamily "ipv4",
// and a service CIDR for the chosen family when the caller omits it (real EKS
// auto-assigns one so DescribeCluster is always populated).
func resolveNetworkConfig(in eksdriver.NetworkConfig) eksdriver.NetworkConfig {
	out := in
	if out.IPFamily == "" {
		out.IPFamily = ipFamilyIPv4
	}

	switch out.IPFamily {
	case ipFamilyIPv6:
		if out.ServiceIPv6CIDR == "" {
			out.ServiceIPv6CIDR = defaultServiceIPv6CIDR
		}
	default:
		if out.ServiceIPv4CIDR == "" {
			out.ServiceIPv4CIDR = defaultServiceIPv4CIDR
		}
	}

	return out
}

// resolveAccessConfig fills in the EKS access-config defaults: authentication
// mode CONFIG_MAP and bootstrapClusterCreatorAdminPermissions true when the
// caller omits them.
func resolveAccessConfig(in eksdriver.AccessConfigRequest) eksdriver.AccessConfig {
	mode := in.AuthenticationMode
	if mode == "" {
		mode = defaultAuthenticationMode
	}

	bootstrap := true
	if in.BootstrapClusterCreatorAdminPermissions != nil {
		bootstrap = *in.BootstrapClusterCreatorAdminPermissions
	}

	return eksdriver.AccessConfig{
		AuthenticationMode:                      mode,
		BootstrapClusterCreatorAdminPermissions: bootstrap,
	}
}

func copyTaints(src []eksdriver.Taint) []eksdriver.Taint {
	if len(src) == 0 {
		return nil
	}

	out := make([]eksdriver.Taint, len(src))
	copy(out, src)

	return out
}

// taintKey identifies a taint for merge/remove; real EKS treats Key+Effect as
// the identity, so add/update replaces a matching pair and remove deletes it.
func taintKey(t eksdriver.Taint) string {
	return t.Key + "\x00" + t.Effect
}

// applyTaints overlays add/update taints onto cur and removes any matching the
// remove set, preserving order. A nil result is returned when nothing remains.
func applyTaints(cur, addOrUpdate, remove []eksdriver.Taint) []eksdriver.Taint {
	byKey := make(map[string]eksdriver.Taint, len(cur)+len(addOrUpdate))
	order := make([]string, 0, len(cur)+len(addOrUpdate))

	upsert := func(t eksdriver.Taint) {
		k := taintKey(t)
		if _, ok := byKey[k]; !ok {
			order = append(order, k)
		}

		byKey[k] = t
	}

	for _, t := range cur {
		upsert(t)
	}

	for _, t := range addOrUpdate {
		upsert(t)
	}

	for _, t := range remove {
		delete(byKey, taintKey(t))
	}

	out := make([]eksdriver.Taint, 0, len(byKey))

	for _, k := range order {
		if t, ok := byKey[k]; ok {
			out = append(out, t)
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// mergeLabels overlays addOrUpdate onto cur and deletes the remove keys,
// returning a fresh map (nil when empty). This is the merge semantics real EKS
// applies, unlike a wholesale replace.
func mergeLabels(cur, addOrUpdate map[string]string, remove []string) map[string]string {
	if len(cur) == 0 && len(addOrUpdate) == 0 {
		return cur
	}

	out := copyTags(cur)
	if out == nil {
		out = make(map[string]string, len(addOrUpdate))
	}

	for k, v := range addOrUpdate {
		out[k] = v
	}

	for _, k := range remove {
		delete(out, k)
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// validateScaling rejects an inconsistent scaling config the way real EKS does
// (InvalidParameterException): minSize must not exceed maxSize and desiredSize
// must fall within [minSize, maxSize]. An all-zero config (scaling omitted) is
// valid and left to defaults.
func validateScaling(s eksdriver.NodegroupScalingConfig) error {
	if s.MinSize > s.MaxSize {
		return cerrors.Newf(cerrors.InvalidArgument,
			"minSize (%d) must not be greater than maxSize (%d)", s.MinSize, s.MaxSize)
	}

	if s.DesiredSize < s.MinSize || s.DesiredSize > s.MaxSize {
		return cerrors.Newf(cerrors.InvalidArgument,
			"desiredSize (%d) must be between minSize (%d) and maxSize (%d)",
			s.DesiredSize, s.MinSize, s.MaxSize)
	}

	return nil
}

func (m *Mock) emitClusterMetrics(name string) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	dims := map[string]string{"ClusterName": name}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{
		{
			Namespace: namespaceEKS, MetricName: "CPUUtilization", Value: metricClusterCPU,
			Unit: "Percent", Dimensions: dims, Timestamp: now,
		},
		{
			Namespace: namespaceEKS, MetricName: "MemoryUtilization", Value: metricClusterMemory,
			Unit: "Percent", Dimensions: dims, Timestamp: now,
		},
		{
			Namespace: namespaceEKS, MetricName: "cluster_node_count", Value: metricClusterNodes,
			Unit: "Count", Dimensions: dims, Timestamp: now,
		},
		{
			Namespace: namespaceEKS, MetricName: "cluster_pod_count", Value: metricClusterPods,
			Unit: "Count", Dimensions: dims, Timestamp: now,
		},
		{
			Namespace: namespaceEKS, MetricName: "apiserver_request_total", Value: metricClusterAPIErrors,
			Unit: "Count", Dimensions: dims, Timestamp: now,
		},
	})
}

// CreateCluster creates a new cluster.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateCluster(ctx context.Context, cfg eksdriver.ClusterConfig) (*eksdriver.Cluster, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "cluster name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.clusters.Get(cfg.Name); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "cluster %q already exists", cfg.Name)
	}

	version := cfg.Version
	if version == "" {
		// Real EKS defaults to the latest supported Kubernetes version when the
		// caller omits it, rather than returning a null version.
		version = defaultKubernetesVersion
	}

	region := regionctx.RegionOr(ctx, m.opts.Region)
	cluster := eksdriver.Cluster{
		Name:                 cfg.Name,
		ARN:                  m.clusterARN(region, cfg.Name),
		Version:              version,
		PlatformVersion:      defaultPlatformVersion,
		RoleArn:              cfg.RoleArn,
		Endpoint:             wavePlaceholderEndpoint,
		CertificateAuthority: stubCertificate(),
		Status:               eksdriver.ClusterStatusActive,
		OIDCIssuer:           m.oidcIssuer(region, cfg.Name),
		VPCConfig: eksdriver.VPCConfig{
			SubnetIDs:              copyStrings(cfg.VPCConfig.SubnetIDs),
			SecurityGroupIDs:       copyStrings(cfg.VPCConfig.SecurityGroupIDs),
			EndpointPublicAccess:   cfg.VPCConfig.EndpointPublicAccess,
			EndpointPrivateAccess:  cfg.VPCConfig.EndpointPrivateAccess,
			PublicAccessCidrs:      copyStrings(cfg.VPCConfig.PublicAccessCidrs),
			ClusterSecurityGroupID: m.clusterSecurityGroupID(cfg.Name),
			VpcID:                  m.resolveVpcID(ctx, cfg.VPCConfig.SubnetIDs),
		},
		Logging:       resolveClusterLogging(cfg.Logging),
		NetworkConfig: resolveNetworkConfig(cfg.NetworkConfig),
		AccessConfig:  resolveAccessConfig(cfg.AccessConfig),
		Tags:          copyTags(cfg.Tags),
		CreatedAt:     m.opts.Clock.Now().UTC(),
	}

	// Wave 2: if a Kubernetes data-plane server is wired, register a fresh
	// ClusterState and remember the UID so DescribeCluster can return an
	// Endpoint that actually answers.
	if m.k8sAPI != nil {
		uid, _ := m.k8sAPI.RegisterCluster()
		m.k8sUIDs[cfg.Name] = uid
	}

	m.clusters.Set(cfg.Name, cluster)

	m.emitClusterMetrics(cfg.Name)

	// Under AsyncSettle a fresh cluster reports CREATING until the window
	// elapses, matching real EKS (CreateCluster -> CREATING -> ACTIVE). With
	// the default (AsyncSettle off) SettleDuration is 0 -> inactive window ->
	// ACTIVE immediately, so nothing changes for existing callers. The stored
	// row keeps the terminal status; only the read-path copy is overlaid.
	m.clusterSettle.Begin(cfg.Name, eksdriver.ClusterStatusCreating, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultClusterSettle))

	out := cluster
	m.withK8sEndpoint(&out)
	m.overlayClusterStatus(&out)

	return &out, nil
}

// withK8sEndpoint mutates c.Endpoint in place to point at the per-cluster
// Kubernetes data-plane URL when a shared APIServer is wired and the cluster
// has an associated UID. Falls back to the Wave 1 sentinel otherwise so
// existing test fixtures keep working.
//
// Caller must hold at least an RLock on m.mu. c is passed by pointer because
// eksdriver.Cluster is a heavy struct (slice + map fields); the caller
// usually wraps this with `out := <local copy>; m.withK8sEndpoint(&out)`.
func (m *Mock) withK8sEndpoint(c *eksdriver.Cluster) {
	if m.k8sAPI == nil {
		return
	}

	uid, ok := m.k8sUIDs[c.Name]
	if !ok {
		return
	}

	base := m.k8sAPI.BaseURL()
	if base == "" {
		return
	}

	c.Endpoint = base + "/k8s/" + uid

	// The advertised CA certifies this endpoint when the data plane is served
	// with ServingTLSConfig, so a caller can validate what it dials.
}

// clusterStatusLocked returns c's current status, overlaid with any active
// clusterSettle window (CREATING/UPDATING until it elapses, then the stored
// terminal status). Caller must hold at least an RLock on m.mu.
func (m *Mock) clusterStatusLocked(c *eksdriver.Cluster) string {
	return m.clusterSettle.State(c.Name, m.opts.Clock.Now(), c.Status)
}

// overlayClusterStatus mutates c.Status in place to the settle-overlaid value.
// Caller must hold at least an RLock on m.mu.
func (m *Mock) overlayClusterStatus(c *eksdriver.Cluster) {
	c.Status = m.clusterStatusLocked(c)
}

// nodegroupStatusLocked is the nodegroup analog of clusterStatusLocked.
func (m *Mock) nodegroupStatusLocked(ng *eksdriver.Nodegroup) string {
	return m.nodegroupSettle.State(nodegroupKey(ng.ClusterName, ng.NodegroupName), m.opts.Clock.Now(), ng.Status)
}

// overlayNodegroupStatus is the nodegroup analog of overlayClusterStatus.
func (m *Mock) overlayNodegroupStatus(ng *eksdriver.Nodegroup) {
	ng.Status = m.nodegroupStatusLocked(ng)
}

// DescribeCluster looks up a cluster by name.
func (m *Mock) DescribeCluster(_ context.Context, name string) (*eksdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.clusters.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", name)
	}

	out := c
	m.withK8sEndpoint(&out)
	m.overlayClusterStatus(&out)

	return &out, nil
}

// ListClusters returns the names of all clusters.
func (m *Mock) ListClusters(_ context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.clusters.Keys(), nil
}

// UpdateClusterConfig records a logical update for VPC config / logging /
// tags. Wave 1 applies the changes synchronously and returns a Successful
// update so SDK pollers terminate immediately.
//
//nolint:gocritic // cfg matches the driver interface signature; one copy on entry is fine.
func (m *Mock) UpdateClusterConfig(
	_ context.Context, name string, cfg eksdriver.VPCConfig, tags map[string]string,
) (*eksdriver.ClusterUpdate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.clusters.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", name)
	}

	if status := m.clusterStatusLocked(&c); status != eksdriver.ClusterStatusActive {
		return nil, resourceInUseErrf(
			"cluster %q already has a pending update (status %s); only one update is allowed at a time", name, status)
	}

	if len(cfg.SubnetIDs) > 0 {
		c.VPCConfig.SubnetIDs = copyStrings(cfg.SubnetIDs)
	}

	if len(cfg.SecurityGroupIDs) > 0 {
		c.VPCConfig.SecurityGroupIDs = copyStrings(cfg.SecurityGroupIDs)
	}

	if len(cfg.PublicAccessCidrs) > 0 {
		c.VPCConfig.PublicAccessCidrs = copyStrings(cfg.PublicAccessCidrs)
	}

	c.VPCConfig.EndpointPublicAccess = cfg.EndpointPublicAccess
	c.VPCConfig.EndpointPrivateAccess = cfg.EndpointPrivateAccess

	if tags != nil {
		c.Tags = copyTags(tags)
	}

	m.clusters.Set(name, c)

	m.clusterSettle.Begin(name, eksdriver.ClusterStatusUpdating, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultClusterSettle))

	return m.recordUpdate(&eksdriver.ClusterUpdate{
		ID:          newUpdateID(),
		Type:        "EndpointAccessUpdate",
		Status:      "Successful",
		CreatedAt:   m.opts.Clock.Now().UTC(),
		ClusterName: name,
	}), nil
}

// UpdateClusterVersion bumps the Kubernetes version of an existing cluster.
func (m *Mock) UpdateClusterVersion(_ context.Context, name, version string) (*eksdriver.ClusterUpdate, error) {
	if version == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "version is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.clusters.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", name)
	}

	if status := m.clusterStatusLocked(&c); status != eksdriver.ClusterStatusActive {
		return nil, resourceInUseErrf(
			"cluster %q already has a pending update (status %s); only one update is allowed at a time", name, status)
	}

	c.Version = version
	m.clusters.Set(name, c)

	m.clusterSettle.Begin(name, eksdriver.ClusterStatusUpdating, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultClusterSettle))

	return m.recordUpdate(&eksdriver.ClusterUpdate{
		ID:          newUpdateID(),
		Type:        "VersionUpdate",
		Status:      "Successful",
		CreatedAt:   m.opts.Clock.Now().UTC(),
		ClusterName: name,
	}), nil
}

// DeleteCluster removes a cluster (only if no nodegroups, Fargate profiles,
// or add-ons remain attached, matching real EKS behavior).
//

func (m *Mock) DeleteCluster(_ context.Context, name string) (*eksdriver.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.clusters.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", name)
	}

	//nolint:gocritic // Store.All copies values out anyway; the per-iter copy here is no extra cost.
	for _, ng := range m.nodegroups.All() {
		if ng.ClusterName == name {
			return nil, resourceInUseErrf("cluster %q still has nodegroup %q attached", name, ng.NodegroupName)
		}
	}

	//nolint:gocritic // Store.All copies values out anyway; the per-iter copy here is no extra cost.
	for _, fp := range m.fargateProfiles.All() {
		if fp.ClusterName == name {
			return nil, resourceInUseErrf("cluster %q still has Fargate profile %q attached", name, fp.FargateProfileName)
		}
	}

	//nolint:gocritic // Store.All copies values out anyway; the per-iter copy here is no extra cost.
	for _, ad := range m.addons.All() {
		if ad.ClusterName == name {
			return nil, resourceInUseErrf("cluster %q still has add-on %q installed", name, ad.AddonName)
		}
	}

	c.Status = eksdriver.ClusterStatusDeleting

	m.clusters.Delete(name)
	m.clusterSettle.Clear(name)

	// Resolve the endpoint before deregistering: the response describes the
	// cluster as it was, and reading afterwards yields the not-implemented
	// sentinel instead of the endpoint the caller had been using.
	out := c
	m.withK8sEndpoint(&out)

	// Wave 2: tear down the cluster's Kubernetes data-plane state too. The
	// UID map entry is dropped after deregister so subsequent describes find
	// nothing — matching the real cluster going away.
	if uid, ok := m.k8sUIDs[name]; ok && m.k8sAPI != nil {
		m.k8sAPI.DeregisterCluster(uid)
		delete(m.k8sUIDs, name)
	}

	return &out, nil
}

// DescribeUpdate returns a recorded update by ID. clusterName scopes the
// lookup so a stale ID from another cluster reports NotFound, matching EKS.
func (m *Mock) DescribeUpdate(_ context.Context, clusterName, updateID string) (*eksdriver.ClusterUpdate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	u, ok := m.updates.Get(updateID)
	if !ok || (clusterName != "" && u.ClusterName != clusterName) {
		return nil, cerrors.Newf(cerrors.NotFound, "update %q not found", updateID)
	}

	out := u

	return &out, nil
}

// ListUpdates returns the IDs of updates recorded for a cluster, optionally
// filtered to a specific nodegroup or add-on.
func (m *Mock) ListUpdates(_ context.Context, clusterName, nodegroupName, addonName string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.clusters.Get(clusterName); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", clusterName)
	}

	out := make([]string, 0)

	for _, u := range m.updates.All() {
		if u.ClusterName != clusterName {
			continue
		}

		if nodegroupName != "" && u.NodegroupName != nodegroupName {
			continue
		}

		if addonName != "" && u.AddonName != addonName {
			continue
		}

		out = append(out, u.ID)
	}

	return out, nil
}

// CreateNodegroup creates a new managed node group.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateNodegroup(_ context.Context, cfg eksdriver.NodegroupConfig) (*eksdriver.Nodegroup, error) {
	if cfg.ClusterName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "clusterName is required")
	}

	if cfg.NodegroupName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "nodegroupName is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	parent, ok := m.clusters.Get(cfg.ClusterName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", cfg.ClusterName)
	}

	if status := m.clusterStatusLocked(&parent); status != eksdriver.ClusterStatusActive {
		return nil, resourceInUseErrf(
			"cluster %q is not ACTIVE (currently %s); cannot create a nodegroup", cfg.ClusterName, status)
	}

	key := nodegroupKey(cfg.ClusterName, cfg.NodegroupName)
	if _, ok := m.nodegroups.Get(key); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"nodegroup %q already exists in cluster %q", cfg.NodegroupName, cfg.ClusterName)
	}

	if err := validateScaling(cfg.ScalingConfig); err != nil {
		return nil, err
	}

	now := m.opts.Clock.Now().UTC()

	instanceTypes := copyStrings(cfg.InstanceTypes)
	if len(instanceTypes) == 0 {
		instanceTypes = []string{defaultNodegroupInstanceType}
	}

	amiType := cfg.AmiType
	if amiType == "" {
		amiType = defaultNodegroupAmiType
	}

	diskSize := cfg.DiskSize
	if diskSize == 0 {
		diskSize = defaultNodegroupDiskSize
	}

	ng := eksdriver.Nodegroup{
		ClusterName:    cfg.ClusterName,
		NodegroupName:  cfg.NodegroupName,
		ARN:            m.nodegroupARN(arnRegion(parent.ARN, m.opts.Region), cfg.ClusterName, cfg.NodegroupName),
		NodeRole:       cfg.NodeRole,
		Subnets:        copyStrings(cfg.Subnets),
		InstanceTypes:  instanceTypes,
		AmiType:        amiType,
		CapacityType:   cfg.CapacityType,
		DiskSize:       diskSize,
		Version:        cfg.Version,
		ReleaseVersion: cfg.ReleaseVersion,
		ScalingConfig:  cfg.ScalingConfig,
		Status:         eksdriver.NodegroupStatusActive,
		Labels:         copyTags(cfg.Labels),
		Taints:         copyTaints(cfg.Taints),
		Tags:           copyTags(cfg.Tags),
		CreatedAt:      now,
		ModifiedAt:     now,
	}

	m.nodegroups.Set(key, ng)

	// Under AsyncSettle a fresh nodegroup reports CREATING until the window
	// elapses, matching real EKS. With the default (AsyncSettle off)
	// SettleDuration is 0 -> inactive window -> ACTIVE immediately, so nothing
	// changes for existing callers.
	m.nodegroupSettle.Begin(key, eksdriver.NodegroupStatusCreating, now,
		m.opts.SettleDuration(settle.DefaultClusterSettle))

	out := ng
	m.overlayNodegroupStatus(&out)

	return &out, nil
}

// DescribeNodegroup looks up a nodegroup by cluster + name.
func (m *Mock) DescribeNodegroup(_ context.Context, clusterName, nodegroupName string) (*eksdriver.Nodegroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ng, ok := m.nodegroups.Get(nodegroupKey(clusterName, nodegroupName))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound,
			"nodegroup %q not found in cluster %q", nodegroupName, clusterName)
	}

	out := ng
	m.overlayNodegroupStatus(&out)

	return &out, nil
}

// ListNodegroups returns the names of all nodegroups in a cluster.
func (m *Mock) ListNodegroups(_ context.Context, clusterName string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.clusters.Get(clusterName); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", clusterName)
	}

	out := make([]string, 0)

	//nolint:gocritic // Store.All copies values out anyway; the per-iter copy here is no extra cost.
	for _, ng := range m.nodegroups.All() {
		if ng.ClusterName == clusterName {
			out = append(out, ng.NodegroupName)
		}
	}

	return out, nil
}

// UpdateNodegroupConfig applies scaling, label, and taint changes to a
// nodegroup. Labels and taints are merged as add/update + remove deltas so a
// change touches only the keys named, matching real EKS.
//
//nolint:gocritic // upd matches the driver interface signature; copied once on entry.
func (m *Mock) UpdateNodegroupConfig(
	_ context.Context, clusterName, nodegroupName string,
	upd eksdriver.NodegroupConfigUpdate,
) (*eksdriver.ClusterUpdate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := nodegroupKey(clusterName, nodegroupName)

	ng, ok := m.nodegroups.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound,
			"nodegroup %q not found in cluster %q", nodegroupName, clusterName)
	}

	if status := m.nodegroupStatusLocked(&ng); status != eksdriver.NodegroupStatusActive {
		return nil, resourceInUseErrf(
			"nodegroup %q already has a pending update (status %s); only one update is allowed at a time",
			nodegroupName, status)
	}

	if upd.Scaling != nil {
		if err := validateScaling(*upd.Scaling); err != nil {
			return nil, err
		}

		ng.ScalingConfig = *upd.Scaling
	}

	ng.Labels = mergeLabels(ng.Labels, upd.AddOrUpdateLabels, upd.RemoveLabels)
	ng.Taints = applyTaints(ng.Taints, upd.AddOrUpdateTaints, upd.RemoveTaints)
	ng.ModifiedAt = m.opts.Clock.Now().UTC()

	m.nodegroups.Set(key, ng)

	m.nodegroupSettle.Begin(key, eksdriver.NodegroupStatusUpdating, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultClusterSettle))

	return m.recordUpdate(&eksdriver.ClusterUpdate{
		ID:            newUpdateID(),
		Type:          "ConfigUpdate",
		Status:        "Successful",
		CreatedAt:     m.opts.Clock.Now().UTC(),
		ClusterName:   clusterName,
		NodegroupName: nodegroupName,
	}), nil
}

// UpdateNodegroupVersion bumps the Kubernetes version of a nodegroup.
func (m *Mock) UpdateNodegroupVersion(
	_ context.Context, clusterName, nodegroupName, version, releaseVersion string,
) (*eksdriver.ClusterUpdate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := nodegroupKey(clusterName, nodegroupName)

	ng, ok := m.nodegroups.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound,
			"nodegroup %q not found in cluster %q", nodegroupName, clusterName)
	}

	if status := m.nodegroupStatusLocked(&ng); status != eksdriver.NodegroupStatusActive {
		return nil, resourceInUseErrf(
			"nodegroup %q already has a pending update (status %s); only one update is allowed at a time",
			nodegroupName, status)
	}

	if version != "" {
		ng.Version = version
	}

	if releaseVersion != "" {
		ng.ReleaseVersion = releaseVersion
	}

	ng.ModifiedAt = m.opts.Clock.Now().UTC()

	m.nodegroups.Set(key, ng)

	m.nodegroupSettle.Begin(key, eksdriver.NodegroupStatusUpdating, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultClusterSettle))

	return m.recordUpdate(&eksdriver.ClusterUpdate{
		ID:            newUpdateID(),
		Type:          "VersionUpdate",
		Status:        "Successful",
		CreatedAt:     m.opts.Clock.Now().UTC(),
		ClusterName:   clusterName,
		NodegroupName: nodegroupName,
	}), nil
}

// DeleteNodegroup removes a nodegroup.
func (m *Mock) DeleteNodegroup(_ context.Context, clusterName, nodegroupName string) (*eksdriver.Nodegroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := nodegroupKey(clusterName, nodegroupName)

	ng, ok := m.nodegroups.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound,
			"nodegroup %q not found in cluster %q", nodegroupName, clusterName)
	}

	ng.Status = eksdriver.NodegroupStatusDeleting

	m.nodegroups.Delete(key)
	m.nodegroupSettle.Clear(key)

	out := ng

	return &out, nil
}

// CreateFargateProfile creates a new Fargate profile in the named cluster.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateFargateProfile(
	_ context.Context, cfg eksdriver.FargateProfileConfig,
) (*eksdriver.FargateProfile, error) {
	if cfg.ClusterName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "clusterName is required")
	}

	if cfg.FargateProfileName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "fargateProfileName is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	parent, ok := m.clusters.Get(cfg.ClusterName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", cfg.ClusterName)
	}

	key := fargateKey(cfg.ClusterName, cfg.FargateProfileName)
	if _, ok := m.fargateProfiles.Get(key); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"Fargate profile %q already exists in cluster %q", cfg.FargateProfileName, cfg.ClusterName)
	}

	fp := eksdriver.FargateProfile{
		ClusterName:        cfg.ClusterName,
		FargateProfileName: cfg.FargateProfileName,
		ARN:                m.fargateARN(arnRegion(parent.ARN, m.opts.Region), cfg.ClusterName, cfg.FargateProfileName),
		PodExecutionRole:   cfg.PodExecutionRole,
		Subnets:            copyStrings(cfg.Subnets),
		Selectors:          append([]eksdriver.FargateProfileSelector(nil), cfg.Selectors...),
		Status:             eksdriver.FargateProfileStatusActive,
		Tags:               copyTags(cfg.Tags),
		CreatedAt:          m.opts.Clock.Now().UTC(),
	}

	m.fargateProfiles.Set(key, fp)

	out := fp

	return &out, nil
}

// DescribeFargateProfile looks up a profile by cluster + name.
func (m *Mock) DescribeFargateProfile(
	_ context.Context, clusterName, profileName string,
) (*eksdriver.FargateProfile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	fp, ok := m.fargateProfiles.Get(fargateKey(clusterName, profileName))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound,
			"Fargate profile %q not found in cluster %q", profileName, clusterName)
	}

	out := fp

	return &out, nil
}

// ListFargateProfiles returns the names of all profiles in a cluster.
func (m *Mock) ListFargateProfiles(_ context.Context, clusterName string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.clusters.Get(clusterName); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", clusterName)
	}

	out := make([]string, 0)

	//nolint:gocritic // Store.All copies values out anyway; the per-iter copy here is no extra cost.
	for _, fp := range m.fargateProfiles.All() {
		if fp.ClusterName == clusterName {
			out = append(out, fp.FargateProfileName)
		}
	}

	return out, nil
}

// DeleteFargateProfile removes a Fargate profile.
func (m *Mock) DeleteFargateProfile(
	_ context.Context, clusterName, profileName string,
) (*eksdriver.FargateProfile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fargateKey(clusterName, profileName)

	fp, ok := m.fargateProfiles.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound,
			"Fargate profile %q not found in cluster %q", profileName, clusterName)
	}

	fp.Status = eksdriver.FargateProfileStatusDeleting

	m.fargateProfiles.Delete(key)

	out := fp

	return &out, nil
}

// CreateAddon installs a new add-on on a cluster.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateAddon(_ context.Context, cfg eksdriver.AddonConfig) (*eksdriver.Addon, error) {
	if cfg.ClusterName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "clusterName is required")
	}

	if cfg.AddonName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "addonName is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	parent, ok := m.clusters.Get(cfg.ClusterName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", cfg.ClusterName)
	}

	key := addonKey(cfg.ClusterName, cfg.AddonName)
	if _, ok := m.addons.Get(key); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"add-on %q already installed on cluster %q", cfg.AddonName, cfg.ClusterName)
	}

	now := m.opts.Clock.Now().UTC()

	ad := eksdriver.Addon{
		ClusterName:           cfg.ClusterName,
		AddonName:             cfg.AddonName,
		AddonVersion:          cfg.AddonVersion,
		ARN:                   m.addonARN(arnRegion(parent.ARN, m.opts.Region), cfg.ClusterName, cfg.AddonName),
		ServiceAccountRoleArn: cfg.ServiceAccountRoleArn,
		ConfigurationValues:   cfg.ConfigurationValues,
		Status:                eksdriver.AddonStatusActive,
		Tags:                  copyTags(cfg.Tags),
		CreatedAt:             now,
		ModifiedAt:            now,
	}

	m.addons.Set(key, ad)

	out := ad

	return &out, nil
}

// DescribeAddon looks up an add-on by cluster + name.
func (m *Mock) DescribeAddon(_ context.Context, clusterName, addonName string) (*eksdriver.Addon, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ad, ok := m.addons.Get(addonKey(clusterName, addonName))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound,
			"add-on %q not found on cluster %q", addonName, clusterName)
	}

	out := ad

	return &out, nil
}

// ListAddons returns the names of all add-ons installed on a cluster.
func (m *Mock) ListAddons(_ context.Context, clusterName string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.clusters.Get(clusterName); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", clusterName)
	}

	out := make([]string, 0)

	//nolint:gocritic // Store.All copies values out anyway; the per-iter copy here is no extra cost.
	for _, ad := range m.addons.All() {
		if ad.ClusterName == clusterName {
			out = append(out, ad.AddonName)
		}
	}

	return out, nil
}

// UpdateAddon updates an installed add-on (version, configuration, etc.).
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) UpdateAddon(_ context.Context, cfg eksdriver.AddonConfig) (*eksdriver.ClusterUpdate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := addonKey(cfg.ClusterName, cfg.AddonName)

	ad, ok := m.addons.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound,
			"add-on %q not found on cluster %q", cfg.AddonName, cfg.ClusterName)
	}

	if cfg.AddonVersion != "" {
		ad.AddonVersion = cfg.AddonVersion
	}

	if cfg.ServiceAccountRoleArn != "" {
		ad.ServiceAccountRoleArn = cfg.ServiceAccountRoleArn
	}

	if cfg.ConfigurationValues != "" {
		ad.ConfigurationValues = cfg.ConfigurationValues
	}

	if cfg.Tags != nil {
		ad.Tags = copyTags(cfg.Tags)
	}

	ad.ModifiedAt = m.opts.Clock.Now().UTC()
	m.addons.Set(key, ad)

	return m.recordUpdate(&eksdriver.ClusterUpdate{
		ID:          newUpdateID(),
		Type:        "AddonUpdate",
		Status:      "Successful",
		CreatedAt:   m.opts.Clock.Now().UTC(),
		ClusterName: cfg.ClusterName,
		AddonName:   cfg.AddonName,
	}), nil
}

// DeleteAddon removes an add-on from a cluster.
func (m *Mock) DeleteAddon(_ context.Context, clusterName, addonName string) (*eksdriver.Addon, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := addonKey(clusterName, addonName)

	ad, ok := m.addons.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound,
			"add-on %q not found on cluster %q", addonName, clusterName)
	}

	ad.Status = eksdriver.AddonStatusDeleting

	m.addons.Delete(key)

	out := ad

	return &out, nil
}
