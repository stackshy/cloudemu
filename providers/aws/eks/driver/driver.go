// Package driver defines the interface for AWS EKS control-plane mocks.
//
// Wave 1 covers the cloud-side EKS surface only: clusters, managed node
// groups, Fargate profiles, and add-ons. The Kubernetes data plane
// (Deployments, Pods, Services, …) is explicitly out of scope and will
// be Wave 2 — when it lands, the cluster Endpoint and CertificateAuthority
// fields will point at a real in-process apiserver instead of the
// placeholder values returned today.
//
// The shape is intentionally service-local: EKS has no cross-cloud
// equivalent that warrants a portable abstraction yet, so this lives next
// to the provider implementation rather than in a top-level package.
package driver

import (
	"context"
	"time"
)

// Cluster lifecycle states. Mirrors the AWS EKS ClusterStatus enum.
const (
	ClusterStatusCreating = "CREATING"
	ClusterStatusActive   = "ACTIVE"
	ClusterStatusDeleting = "DELETING"
	ClusterStatusUpdating = "UPDATING"
)

// Nodegroup lifecycle states.
const (
	NodegroupStatusCreating = "CREATING"
	NodegroupStatusActive   = "ACTIVE"
	NodegroupStatusUpdating = "UPDATING"
	NodegroupStatusDeleting = "DELETING"
)

// FargateProfile lifecycle states.
const (
	FargateProfileStatusCreating = "CREATING"
	FargateProfileStatusActive   = "ACTIVE"
	FargateProfileStatusDeleting = "DELETING"
)

// Addon lifecycle states.
const (
	AddonStatusCreating = "CREATING"
	AddonStatusActive   = "ACTIVE"
	AddonStatusUpdating = "UPDATING"
	AddonStatusDeleting = "DELETING"
)

// VPCConfig captures the subset of EKS VPC configuration the mock retains.
type VPCConfig struct {
	SubnetIDs             []string
	SecurityGroupIDs      []string
	EndpointPublicAccess  bool
	EndpointPrivateAccess bool
	PublicAccessCidrs     []string
	// ClusterSecurityGroupID and VpcID are populated by the provider on cluster
	// create (they are not caller inputs): real EKS auto-creates a cluster
	// security group and reports it here, and derives vpcId from the subnets.
	ClusterSecurityGroupID string
	VpcID                  string
}

// ClusterLogging is one EKS control-plane log-type group and whether it is
// enabled. Real EKS normalizes cluster logging into (up to) two entries: the
// enabled log types and the disabled ones. The recognized types are api,
// audit, authenticator, controllerManager, and scheduler.
type ClusterLogging struct {
	Types   []string
	Enabled bool
}

// NetworkConfig captures the Kubernetes networking configuration surfaced under
// DescribeCluster kubernetesNetworkConfig. ServiceIPv4CIDR and IPFamily are
// caller inputs; ServiceIPv6CIDR is provider-assigned for IPv6 clusters (the
// caller cannot choose it), mirroring how VpcID is derived on VPCConfig.
type NetworkConfig struct {
	ServiceIPv4CIDR string
	ServiceIPv6CIDR string
	IPFamily        string
}

// AccessConfigRequest is the caller-supplied access configuration on
// CreateCluster. BootstrapClusterCreatorAdminPermissions is a pointer so the
// provider can distinguish "omitted" (apply the AWS default of true) from an
// explicit false.
type AccessConfigRequest struct {
	AuthenticationMode                      string
	BootstrapClusterCreatorAdminPermissions *bool
}

// AccessConfig is the resolved cluster access-management configuration surfaced
// under DescribeCluster accessConfig.
type AccessConfig struct {
	AuthenticationMode                      string
	BootstrapClusterCreatorAdminPermissions bool
}

// ClusterConfig configures a new EKS cluster.
type ClusterConfig struct {
	Name          string
	Version       string
	RoleArn       string
	VPCConfig     VPCConfig
	Logging       []ClusterLogging
	NetworkConfig NetworkConfig
	AccessConfig  AccessConfigRequest
	Tags          map[string]string
}

// Cluster is the mock-side representation of an EKS cluster.
type Cluster struct {
	Name                 string
	ARN                  string
	Version              string
	PlatformVersion      string
	RoleArn              string
	Endpoint             string
	CertificateAuthority string
	Status               string
	VPCConfig            VPCConfig
	Logging              []ClusterLogging
	NetworkConfig        NetworkConfig
	AccessConfig         AccessConfig
	Tags                 map[string]string
	CreatedAt            time.Time
	// OIDCIssuer is the cluster's OpenID Connect issuer URL, surfaced under
	// DescribeCluster identity.oidc.issuer. Required for IRSA and
	// aws_iam_openid_connect_provider wiring.
	OIDCIssuer string
}

// ClusterUpdate is returned by mutating cluster ops; SDKs poll this via
// DescribeUpdate but Wave 1 returns done=true immediately.
type ClusterUpdate struct {
	ID        string
	Type      string // VersionUpdate, EndpointAccessUpdate, …
	Status    string // InProgress, Failed, Canceled, Successful
	CreatedAt time.Time
	// ClusterName is the cluster the update belongs to; used by ListUpdates.
	ClusterName string
	// NodegroupName / AddonName scope the update to a child resource when set,
	// so ListUpdates can filter by nodegroupName/addonName like real EKS.
	NodegroupName string
	AddonName     string
}

// NodegroupScalingConfig captures Auto Scaling sizing for a nodegroup.
type NodegroupScalingConfig struct {
	MinSize     int
	MaxSize     int
	DesiredSize int
}

// Taint is a Kubernetes taint applied to a managed node group's nodes. Effect
// is one of NO_SCHEDULE, PREFER_NO_SCHEDULE, or NO_EXECUTE. A taint is
// identified by its Key+Effect pair.
type Taint struct {
	Key    string
	Value  string
	Effect string
}

// NodegroupConfig configures a new managed node group.
type NodegroupConfig struct {
	ClusterName    string
	NodegroupName  string
	NodeRole       string
	Subnets        []string
	InstanceTypes  []string
	AmiType        string
	CapacityType   string
	DiskSize       int
	Version        string
	ReleaseVersion string
	ScalingConfig  NodegroupScalingConfig
	Labels         map[string]string
	Taints         []Taint
	Tags           map[string]string
}

// Nodegroup is the mock-side representation of a managed node group.
type Nodegroup struct {
	ClusterName    string
	NodegroupName  string
	ARN            string
	NodeRole       string
	Subnets        []string
	InstanceTypes  []string
	AmiType        string
	CapacityType   string
	DiskSize       int
	Version        string
	ReleaseVersion string
	ScalingConfig  NodegroupScalingConfig
	Status         string
	Labels         map[string]string
	Taints         []Taint
	Tags           map[string]string
	CreatedAt      time.Time
	// ModifiedAt advances on every mutating op (config/version update); on a
	// freshly created nodegroup it equals CreatedAt.
	ModifiedAt time.Time
}

// NodegroupConfigUpdate carries the mutable fields UpdateNodegroupConfig
// applies. Scaling, when non-nil, is the already-merged target sizing (the
// caller overlays partial requests). Label and taint changes are expressed as
// add/update and remove deltas, matching the real EKS request shape.
type NodegroupConfigUpdate struct {
	Scaling           *NodegroupScalingConfig
	AddOrUpdateLabels map[string]string
	RemoveLabels      []string
	AddOrUpdateTaints []Taint
	RemoveTaints      []Taint
}

// FargateProfileSelector matches Pods to a Fargate profile.
type FargateProfileSelector struct {
	Namespace string
	Labels    map[string]string
}

// FargateProfileConfig configures a new Fargate profile.
type FargateProfileConfig struct {
	ClusterName        string
	FargateProfileName string
	PodExecutionRole   string
	Subnets            []string
	Selectors          []FargateProfileSelector
	Tags               map[string]string
}

// FargateProfile is the mock-side representation of a Fargate profile.
type FargateProfile struct {
	ClusterName        string
	FargateProfileName string
	ARN                string
	PodExecutionRole   string
	Subnets            []string
	Selectors          []FargateProfileSelector
	Status             string
	Tags               map[string]string
	CreatedAt          time.Time
}

// AddonConfig configures a new cluster add-on.
type AddonConfig struct {
	ClusterName           string
	AddonName             string
	AddonVersion          string
	ServiceAccountRoleArn string
	ConfigurationValues   string
	Tags                  map[string]string
}

// Addon is the mock-side representation of a cluster add-on.
type Addon struct {
	ClusterName           string
	AddonName             string
	AddonVersion          string
	ARN                   string
	ServiceAccountRoleArn string
	ConfigurationValues   string
	Status                string
	Tags                  map[string]string
	CreatedAt             time.Time
	ModifiedAt            time.Time
}

// EKS is the interface implemented by the EKS provider mock. It mirrors the
// AWS EKS API operations the SDK-compat handler needs to serve real clients.
type EKS interface {
	// Clusters
	CreateCluster(ctx context.Context, cfg ClusterConfig) (*Cluster, error)
	DescribeCluster(ctx context.Context, name string) (*Cluster, error)
	ListClusters(ctx context.Context) ([]string, error)
	UpdateClusterConfig(ctx context.Context, name string, cfg VPCConfig, tags map[string]string) (*ClusterUpdate, error)
	UpdateClusterVersion(ctx context.Context, name, version string) (*ClusterUpdate, error)
	DeleteCluster(ctx context.Context, name string) (*Cluster, error)

	// Updates
	DescribeUpdate(ctx context.Context, clusterName, updateID string) (*ClusterUpdate, error)
	ListUpdates(ctx context.Context, clusterName, nodegroupName, addonName string) ([]string, error)

	// Node groups
	CreateNodegroup(ctx context.Context, cfg NodegroupConfig) (*Nodegroup, error)
	DescribeNodegroup(ctx context.Context, clusterName, nodegroupName string) (*Nodegroup, error)
	ListNodegroups(ctx context.Context, clusterName string) ([]string, error)
	UpdateNodegroupConfig(
		ctx context.Context, clusterName, nodegroupName string,
		upd NodegroupConfigUpdate,
	) (*ClusterUpdate, error)
	UpdateNodegroupVersion(
		ctx context.Context, clusterName, nodegroupName, version, releaseVersion string,
	) (*ClusterUpdate, error)
	DeleteNodegroup(ctx context.Context, clusterName, nodegroupName string) (*Nodegroup, error)

	// Fargate profiles
	CreateFargateProfile(ctx context.Context, cfg FargateProfileConfig) (*FargateProfile, error)
	DescribeFargateProfile(ctx context.Context, clusterName, profileName string) (*FargateProfile, error)
	ListFargateProfiles(ctx context.Context, clusterName string) ([]string, error)
	DeleteFargateProfile(ctx context.Context, clusterName, profileName string) (*FargateProfile, error)

	// Add-ons
	CreateAddon(ctx context.Context, cfg AddonConfig) (*Addon, error)
	DescribeAddon(ctx context.Context, clusterName, addonName string) (*Addon, error)
	ListAddons(ctx context.Context, clusterName string) ([]string, error)
	UpdateAddon(ctx context.Context, cfg AddonConfig) (*ClusterUpdate, error)
	DeleteAddon(ctx context.Context, clusterName, addonName string) (*Addon, error)
}
