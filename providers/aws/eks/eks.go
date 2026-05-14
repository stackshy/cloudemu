// Package eks provides an in-memory mock of AWS EKS — Wave 1 covers the
// control plane only (clusters, managed node groups, Fargate profiles, and
// add-ons). The Kubernetes data plane is out of scope and will be Wave 2;
// for now the cluster Endpoint and CertificateAuthority fields return
// placeholder values so kubeconfig generation works syntactically without
// a real apiserver.
//
// The mock implements eks/driver.EKS so the same backend serves both the
// SDK-compat HTTP handler in server/aws/eks and any direct programmatic
// access from Go test code.
package eks

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	eksdriver "github.com/stackshy/cloudemu/providers/aws/eks/driver"
)

// Wave 1 placeholder for the cluster API server endpoint. Wave 2 will swap
// in a real per-cluster apiserver address.
const (
	wavePlaceholderEndpoint = "https://EKS-DATAPLANE-NOT-IMPLEMENTED.cloudemu.local"
	defaultPlatformVersion  = "eks.1"
	namespaceEKS            = "AWS/EKS"
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

	opts       *config.Options
	monitoring mondriver.Monitoring
}

// New creates a new AWS EKS mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:        memstore.New[eksdriver.Cluster](),
		nodegroups:      memstore.New[eksdriver.Nodegroup](),
		fargateProfiles: memstore.New[eksdriver.FargateProfile](),
		addons:          memstore.New[eksdriver.Addon](),
		opts:            opts,
	}
}

// SetMonitoring wires a CloudWatch-style backend for auto-metric emission.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
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

// stubCertificate returns a placeholder base64 CA blob. Real EKS returns a
// PEM-encoded x509 cert; SDK clients only base64-decode it for the
// kubeconfig, so a deterministic stub is enough for Wave 1.
func stubCertificate() string {
	const placeholder = "-----BEGIN CERTIFICATE-----\nMIICloudemuStubCertificate\n-----END CERTIFICATE-----\n"

	return base64.StdEncoding.EncodeToString([]byte(placeholder))
}

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

func (m *Mock) clusterARN(name string) string {
	return idgen.AWSARN("eks", m.opts.Region, m.opts.AccountID, "cluster/"+name)
}

func (m *Mock) nodegroupARN(clusterName, nodegroupName string) string {
	return idgen.AWSARN("eks", m.opts.Region, m.opts.AccountID,
		"nodegroup/"+clusterName+"/"+nodegroupName)
}

func (m *Mock) fargateARN(clusterName, profileName string) string {
	return idgen.AWSARN("eks", m.opts.Region, m.opts.AccountID,
		"fargateprofile/"+clusterName+"/"+profileName)
}

func (m *Mock) addonARN(clusterName, addonName string) string {
	return idgen.AWSARN("eks", m.opts.Region, m.opts.AccountID,
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
func (m *Mock) CreateCluster(_ context.Context, cfg eksdriver.ClusterConfig) (*eksdriver.Cluster, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "cluster name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.clusters.Get(cfg.Name); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "cluster %q already exists", cfg.Name)
	}

	cluster := eksdriver.Cluster{
		Name:                 cfg.Name,
		ARN:                  m.clusterARN(cfg.Name),
		Version:              cfg.Version,
		PlatformVersion:      defaultPlatformVersion,
		RoleArn:              cfg.RoleArn,
		Endpoint:             wavePlaceholderEndpoint,
		CertificateAuthority: stubCertificate(),
		Status:               eksdriver.ClusterStatusActive,
		VPCConfig: eksdriver.VPCConfig{
			SubnetIDs:             copyStrings(cfg.VPCConfig.SubnetIDs),
			SecurityGroupIDs:      copyStrings(cfg.VPCConfig.SecurityGroupIDs),
			EndpointPublicAccess:  cfg.VPCConfig.EndpointPublicAccess,
			EndpointPrivateAccess: cfg.VPCConfig.EndpointPrivateAccess,
			PublicAccessCidrs:     copyStrings(cfg.VPCConfig.PublicAccessCidrs),
		},
		Tags:      copyTags(cfg.Tags),
		CreatedAt: m.opts.Clock.Now().UTC(),
	}

	m.clusters.Set(cfg.Name, cluster)

	m.emitClusterMetrics(cfg.Name)

	out := cluster

	return &out, nil
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

	return &eksdriver.ClusterUpdate{
		ID:        newUpdateID(),
		Type:      "EndpointAccessUpdate",
		Status:    "Successful",
		CreatedAt: m.opts.Clock.Now().UTC(),
	}, nil
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

	c.Version = version
	m.clusters.Set(name, c)

	return &eksdriver.ClusterUpdate{
		ID:        newUpdateID(),
		Type:      "VersionUpdate",
		Status:    "Successful",
		CreatedAt: m.opts.Clock.Now().UTC(),
	}, nil
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
			return nil, cerrors.Newf(cerrors.FailedPrecondition,
				"cluster %q still has nodegroup %q attached", name, ng.NodegroupName)
		}
	}

	//nolint:gocritic // Store.All copies values out anyway; the per-iter copy here is no extra cost.
	for _, fp := range m.fargateProfiles.All() {
		if fp.ClusterName == name {
			return nil, cerrors.Newf(cerrors.FailedPrecondition,
				"cluster %q still has Fargate profile %q attached", name, fp.FargateProfileName)
		}
	}

	//nolint:gocritic // Store.All copies values out anyway; the per-iter copy here is no extra cost.
	for _, ad := range m.addons.All() {
		if ad.ClusterName == name {
			return nil, cerrors.Newf(cerrors.FailedPrecondition,
				"cluster %q still has add-on %q installed", name, ad.AddonName)
		}
	}

	c.Status = eksdriver.ClusterStatusDeleting

	m.clusters.Delete(name)

	out := c

	return &out, nil
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

	if _, ok := m.clusters.Get(cfg.ClusterName); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", cfg.ClusterName)
	}

	key := nodegroupKey(cfg.ClusterName, cfg.NodegroupName)
	if _, ok := m.nodegroups.Get(key); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"nodegroup %q already exists in cluster %q", cfg.NodegroupName, cfg.ClusterName)
	}

	ng := eksdriver.Nodegroup{
		ClusterName:    cfg.ClusterName,
		NodegroupName:  cfg.NodegroupName,
		ARN:            m.nodegroupARN(cfg.ClusterName, cfg.NodegroupName),
		NodeRole:       cfg.NodeRole,
		Subnets:        copyStrings(cfg.Subnets),
		InstanceTypes:  copyStrings(cfg.InstanceTypes),
		AmiType:        cfg.AmiType,
		CapacityType:   cfg.CapacityType,
		DiskSize:       cfg.DiskSize,
		Version:        cfg.Version,
		ReleaseVersion: cfg.ReleaseVersion,
		ScalingConfig:  cfg.ScalingConfig,
		Status:         eksdriver.NodegroupStatusActive,
		Labels:         copyTags(cfg.Labels),
		Tags:           copyTags(cfg.Tags),
		CreatedAt:      m.opts.Clock.Now().UTC(),
	}

	m.nodegroups.Set(key, ng)

	out := ng

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

// UpdateNodegroupConfig applies scaling and label changes to a nodegroup.
func (m *Mock) UpdateNodegroupConfig(
	_ context.Context, clusterName, nodegroupName string,
	scaling *eksdriver.NodegroupScalingConfig, labels map[string]string,
) (*eksdriver.ClusterUpdate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := nodegroupKey(clusterName, nodegroupName)

	ng, ok := m.nodegroups.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound,
			"nodegroup %q not found in cluster %q", nodegroupName, clusterName)
	}

	if scaling != nil {
		ng.ScalingConfig = *scaling
	}

	if labels != nil {
		ng.Labels = copyTags(labels)
	}

	m.nodegroups.Set(key, ng)

	return &eksdriver.ClusterUpdate{
		ID:        newUpdateID(),
		Type:      "ConfigUpdate",
		Status:    "Successful",
		CreatedAt: m.opts.Clock.Now().UTC(),
	}, nil
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

	if version != "" {
		ng.Version = version
	}

	if releaseVersion != "" {
		ng.ReleaseVersion = releaseVersion
	}

	m.nodegroups.Set(key, ng)

	return &eksdriver.ClusterUpdate{
		ID:        newUpdateID(),
		Type:      "VersionUpdate",
		Status:    "Successful",
		CreatedAt: m.opts.Clock.Now().UTC(),
	}, nil
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

	if _, ok := m.clusters.Get(cfg.ClusterName); !ok {
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
		ARN:                m.fargateARN(cfg.ClusterName, cfg.FargateProfileName),
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

	if _, ok := m.clusters.Get(cfg.ClusterName); !ok {
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
		ARN:                   m.addonARN(cfg.ClusterName, cfg.AddonName),
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

	return &eksdriver.ClusterUpdate{
		ID:        newUpdateID(),
		Type:      "AddonUpdate",
		Status:    "Successful",
		CreatedAt: m.opts.Clock.Now().UTC(),
	}, nil
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
