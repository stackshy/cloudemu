// Package driver defines the interface for AWS EKS control-plane mocks.
//
// Wave 1 covers the cloud-side EKS surface only: clusters, managed node
// groups, Fargate profiles, and add-ons. The Kubernetes data plane
// (Deployments, Pods, Services, …) is explicitly out of scope and will
// be Wave 2; when it lands, the cluster Endpoint and CertificateAuthority
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

// AccessConfigUpdate is the caller-supplied access-configuration change on
// UpdateClusterConfig. Real EKS only allows changing the authentication mode
// after creation (BootstrapClusterCreatorAdminPermissions is create-only), so
// this intentionally carries just that one field.
type AccessConfigUpdate struct {
	AuthenticationMode string
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
	// CreatorPrincipalArn and CreatorAccessKeyID identify the caller. They
	// pick the principal of the bootstrap cluster admin access entry.
	CreatorPrincipalArn string
	CreatorAccessKeyID  string
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

// NodegroupUpdateConfig captures a managed node group's rolling-update
// concurrency. Real EKS surfaces exactly one of MaxUnavailable (an absolute
// node count) or MaxUnavailablePercentage; the two are mutually exclusive, and
// a zero value on both means "unset". EKS defaults a created nodegroup to
// MaxUnavailable=1 when the caller omits updateConfig entirely, so
// DescribeNodegroup always reports a value and Terraform's update_config block
// stops drifting.
type NodegroupUpdateConfig struct {
	MaxUnavailable           int
	MaxUnavailablePercentage int
}

// LaunchTemplateSpecification identifies the EC2 launch template a managed
// node group is based on. Real EKS requires exactly one of ID or Name on the
// request; Version is optional and defaults to the template's default version
// when omitted.
type LaunchTemplateSpecification struct {
	ID      string
	Name    string
	Version string
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
	UpdateConfig   NodegroupUpdateConfig
	Labels         map[string]string
	Taints         []Taint
	Tags           map[string]string
	// LaunchTemplate is optional; when set, it names the EC2 launch template
	// backing the node group's instances.
	LaunchTemplate *LaunchTemplateSpecification
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
	UpdateConfig   NodegroupUpdateConfig
	Status         string
	Labels         map[string]string
	Taints         []Taint
	Tags           map[string]string
	CreatedAt      time.Time
	// ModifiedAt advances on every mutating op (config/version update); on a
	// freshly created nodegroup it equals CreatedAt.
	ModifiedAt time.Time
	// LaunchTemplate mirrors the caller-supplied launch template, when set.
	LaunchTemplate *LaunchTemplateSpecification
}

// NodegroupConfigUpdate carries the mutable fields UpdateNodegroupConfig
// applies. Scaling, when non-nil, is the already-merged target sizing (the
// caller overlays partial requests). Label and taint changes are expressed as
// add/update and remove deltas, matching the real EKS request shape.
type NodegroupConfigUpdate struct {
	Scaling           *NodegroupScalingConfig
	UpdateConfig      *NodegroupUpdateConfig
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

// Access entry types. Real EKS defaults an omitted type to STANDARD, and the
// type can't change after creation.
const (
	AccessEntryTypeStandard      = "STANDARD"
	AccessEntryTypeEC2Linux      = "EC2_LINUX"
	AccessEntryTypeEC2Windows    = "EC2_WINDOWS"
	AccessEntryTypeFargateLinux  = "FARGATE_LINUX"
	AccessEntryTypeEC2           = "EC2"
	AccessEntryTypeHybridLinux   = "HYBRID_LINUX"
	AccessEntryTypeHyperPodLinux = "HYPERPOD_LINUX"
)

// Access scope types for an associated access policy.
const (
	AccessScopeCluster   = "cluster"
	AccessScopeNamespace = "namespace"
)

// AccessEntryConfig configures a new access entry.
type AccessEntryConfig struct {
	ClusterName        string
	PrincipalArn       string
	Type               string
	Username           string
	KubernetesGroups   []string
	Tags               map[string]string
	ClientRequestToken string
}

// AccessEntryUpdate carries the mutable access entry fields. A nil field
// means the caller left it out, so the stored value stays.
type AccessEntryUpdate struct {
	ClusterName      string
	PrincipalArn     string
	KubernetesGroups *[]string
	Username         *string
}

// AccessScope limits an associated access policy to the whole cluster or to
// a set of namespaces.
type AccessScope struct {
	Type       string
	Namespaces []string
}

// AssociatedAccessPolicy is an access policy linked to an access entry.
type AssociatedAccessPolicy struct {
	PolicyArn    string
	AccessScope  AccessScope
	AssociatedAt time.Time
	ModifiedAt   time.Time
}

// AccessEntry grants one IAM principal access to a cluster. Policies holds
// the associated access policies. Each policy ARN appears at most once.
type AccessEntry struct {
	ClusterName        string
	PrincipalArn       string
	ARN                string
	Type               string
	Username           string
	KubernetesGroups   []string
	Tags               map[string]string
	CreatedAt          time.Time
	ModifiedAt         time.Time
	ClientRequestToken string
	Policies           []AssociatedAccessPolicy
}

// AccessPolicy is one entry of the fixed EKS access policy catalog.
type AccessPolicy struct {
	Name string
	ARN  string
}

// AddonVersionFilter narrows DescribeAddonVersions. Empty fields match all.
type AddonVersionFilter struct {
	AddonName         string
	KubernetesVersion string
	Types             []string
	Publishers        []string
	Owners            []string
}

// AddonCompatibility says which cluster version an add-on version runs on and
// whether it is the default there.
type AddonCompatibility struct {
	ClusterVersion   string
	PlatformVersions []string
	DefaultVersion   bool
}

// AddonVersionInfo is one published version of an add-on.
type AddonVersionInfo struct {
	AddonVersion           string
	Architecture           []string
	ComputeTypes           []string
	Compatibilities        []AddonCompatibility
	RequiresConfiguration  bool
	RequiresIamPermissions bool
}

// AddonInfo is one add-on in the catalog with its versions, newest first.
type AddonInfo struct {
	AddonName        string
	Type             string
	Owner            string
	Publisher        string
	DefaultNamespace string
	AddonVersions    []AddonVersionInfo
}

// AddonPodIdentityConfiguration names the service account an add-on uses and
// the managed policies EKS recommends for its pod identity role.
type AddonPodIdentityConfiguration struct {
	ServiceAccount             string
	RecommendedManagedPolicies []string
}

// AddonConfiguration is the configuration schema of one add-on version.
type AddonConfiguration struct {
	AddonName                string
	AddonVersion             string
	ConfigurationSchema      string
	PodIdentityConfiguration []AddonPodIdentityConfiguration
}

// EKS is the interface implemented by the EKS provider mock. It mirrors the
// AWS EKS API operations the SDK-compat handler needs to serve real clients.
type EKS interface {
	// Clusters
	CreateCluster(ctx context.Context, cfg ClusterConfig) (*Cluster, error)
	DescribeCluster(ctx context.Context, name string) (*Cluster, error)
	ListClusters(ctx context.Context) ([]string, error)
	UpdateClusterConfig(
		ctx context.Context, name string, cfg *VPCConfig,
		logging []ClusterLogging, accessConfig *AccessConfigUpdate, tags map[string]string,
	) (*ClusterUpdate, error)
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

	// Add-on catalog
	DescribeAddonVersions(ctx context.Context, filter AddonVersionFilter) ([]AddonInfo, error)
	DescribeAddonConfiguration(ctx context.Context, addonName, addonVersion string) (*AddonConfiguration, error)

	// Access entries
	CreateAccessEntry(ctx context.Context, cfg AccessEntryConfig) (*AccessEntry, error)
	DescribeAccessEntry(ctx context.Context, clusterName, principalArn string) (*AccessEntry, error)
	ListAccessEntries(ctx context.Context, clusterName, associatedPolicyArn string) ([]string, error)
	UpdateAccessEntry(ctx context.Context, upd AccessEntryUpdate) (*AccessEntry, error)
	DeleteAccessEntry(ctx context.Context, clusterName, principalArn string) error

	// Access policies
	ListAccessPolicies(ctx context.Context) ([]AccessPolicy, error)
	AssociateAccessPolicy(
		ctx context.Context, clusterName, principalArn, policyArn string, scope AccessScope,
	) (*AssociatedAccessPolicy, error)
	DisassociateAccessPolicy(ctx context.Context, clusterName, principalArn, policyArn string) error
	ListAssociatedAccessPolicies(ctx context.Context, clusterName, principalArn string) ([]AssociatedAccessPolicy, error)
}
