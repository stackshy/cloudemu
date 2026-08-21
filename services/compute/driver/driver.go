// Package driver defines the interface for compute service implementations.
package driver

import "context"

// InstanceConfig describes a virtual machine instance to create.
type InstanceConfig struct {
	ImageID        string
	InstanceType   string
	Tags           map[string]string
	SubnetID       string
	SecurityGroups []string
	KeyName        string
	UserData       string
	// Managed marks the instance as a service-provider-managed resource at
	// launch time. Principal names the managing service provider.
	Managed   bool
	Principal string
	// OSType ("Linux"/"Windows"), Priority ("Spot"/"Regular"), and LicenseType
	// (hybrid-benefit marker) are cost inputs a discoverer prices on; carried
	// through to the Instance so Resource Graph / discovery can echo them. Zones
	// are the availability zones the instance is placed in.
	OSType      string
	Priority    string
	LicenseType string
	Zones       []string
	// Region is the location the instance is launched in (Azure location, e.g.
	// "eastus"); empty falls back to the emulator's default region. ResourceGroup
	// is the Azure resource group the instance belongs to; empty for providers
	// with no such concept. Both are carried through so cross-service discovery
	// (Resource Graph) reports the instance's real location and resource group
	// rather than the emulator defaults.
	Region        string
	ResourceGroup string
}

// Instance describes a running virtual machine.
type Instance struct {
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
	// OSType is the guest OS family ("Linux"/"Windows"), when known.
	OSType string
	// Priority is the provisioning priority ("Spot"/"Regular"), when known —
	// used by cost consumers to price interruptible instances.
	Priority string
	// LicenseType is a bring-your-own-license / hybrid-benefit marker
	// ("Windows_Server"/"RHEL_BYOS"), when set.
	LicenseType string
	// Zones are the availability zones the instance occupies, when known.
	Zones []string
	// Region / ResourceGroup echo where the instance lives (Azure location and
	// resource group), when known; empty falls back to the emulator defaults.
	Region        string
	ResourceGroup string
	// Operator carries service-provider managed-resource metadata. It is nil
	// for ordinary (unmanaged) instances.
	Operator *OperatorInfo
}

// OperatorInfo describes the service-provider ownership of a managed resource.
type OperatorInfo struct {
	// Managed is true when the resource is managed by a service provider.
	Managed bool
	// Principal is the service provider managing the resource (set when Managed).
	Principal string
	// HiddenByDefault is true when the account's managed-resource-visibility
	// setting hides this resource from describe calls by default.
	HiddenByDefault bool
}

// DescribeInstancesOptions carries optional flags for DescribeInstances.
type DescribeInstancesOptions struct {
	// IncludeManagedResources reveals service-provider-managed instances that
	// would otherwise be hidden by the account's managed-resource-visibility.
	IncludeManagedResources bool
}

// ModifyInstanceInput holds modifiable instance attributes.
type ModifyInstanceInput struct {
	InstanceType string
	Tags         map[string]string
}

// DescribeFilter is a filter for describing instances.
type DescribeFilter struct {
	Name   string
	Values []string
}

// AutoScalingGroupConfig configures an auto-scaling group.
type AutoScalingGroupConfig struct {
	Name              string
	MinSize           int
	MaxSize           int
	DesiredCapacity   int
	InstanceConfig    InstanceConfig
	HealthCheckType   string // "EC2", "ELB"
	HealthCheckGrace  int    // seconds
	Tags              map[string]string
	AvailabilityZones []string
}

// AutoScalingGroup describes an auto-scaling group.
type AutoScalingGroup struct {
	Name              string
	MinSize           int
	MaxSize           int
	DesiredCapacity   int
	CurrentSize       int
	InstanceIDs       []string
	Status            string
	HealthCheckType   string
	CreatedAt         string
	Tags              map[string]string
	AvailabilityZones []string
}

// ScalingPolicy defines when to scale.
type ScalingPolicy struct {
	Name              string
	AutoScalingGroup  string
	PolicyType        string // "SimpleScaling", "TargetTracking", "StepScaling"
	AdjustmentType    string // "ChangeInCapacity", "ExactCapacity", "PercentChangeInCapacity"
	ScalingAdjustment int
	Cooldown          int     // seconds
	TargetValue       float64 // for TargetTracking
	MetricName        string  // for TargetTracking
}

// SpotInstanceRequest describes a spot/preemptible instance request.
type SpotInstanceRequest struct {
	ID             string
	InstanceConfig InstanceConfig
	MaxPrice       float64
	Status         string // "open", "active", "closed", "canceled"
	InstanceID     string
	CreatedAt      string
	Type           string // "one-time", "persistent"
}

// SpotRequestConfig configures a spot instance request.
type SpotRequestConfig struct {
	InstanceConfig InstanceConfig
	MaxPrice       float64
	Count          int
	Type           string // "one-time", "persistent"
}

// LaunchTemplate describes a launch template.
type LaunchTemplate struct {
	ID             string
	Name           string
	Version        int
	InstanceConfig InstanceConfig
	CreatedAt      string
}

// LaunchTemplateConfig configures a launch template.
type LaunchTemplateConfig struct {
	Name           string
	InstanceConfig InstanceConfig
}

// VolumeConfig describes a volume to create.
type VolumeConfig struct {
	Size             int
	VolumeType       string
	AvailabilityZone string
	Tags             map[string]string
	// IOPS / Throughput are the provisioned performance for io2/gp3 and Azure
	// Premium SSD v2 / Ultra disks — cost inputs a discoverer prices on. Zero
	// means unset (the volume then reports 0, omitted downstream).
	IOPS       int
	Throughput int
	// Tier is the performance tier (Azure P10/P4, or a storage tier name),
	// echoed as properties.tier / sku.tier for cost tiering.
	Tier string
}

// VolumeInfo describes a block storage volume.
type VolumeInfo struct {
	ID               string
	Size             int
	VolumeType       string
	State            string // "available", "in-use"
	AvailabilityZone string
	AttachedTo       string
	Device           string
	CreatedAt        string
	Tags             map[string]string
	// IOPS is the provisioned IOPS (io2/gp3, Premium/Ultra disks), when set.
	IOPS int
	// Throughput is the provisioned throughput in MB/s, when set.
	Throughput int
	// Tier is the performance tier (e.g. Azure P10/P4), when set.
	Tier string
}

// SnapshotConfig describes a snapshot to create.
type SnapshotConfig struct {
	VolumeID    string
	Description string
	Tags        map[string]string
}

// SnapshotInfo describes a volume snapshot.
type SnapshotInfo struct {
	ID          string
	VolumeID    string
	State       string // "completed", "pending"
	Description string
	Size        int
	CreatedAt   string
	Tags        map[string]string
}

// ImageConfig describes a machine image to create.
type ImageConfig struct {
	InstanceID  string
	Name        string
	Description string
	Tags        map[string]string
}

// ImageInfo describes a machine image.
type ImageInfo struct {
	ID          string
	Name        string
	State       string // "available", "deregistered"
	Description string
	CreatedAt   string
	Tags        map[string]string
}

// KeyPairConfig describes a key pair to create.
type KeyPairConfig struct {
	Name    string
	KeyType string // "rsa" or "ed25519"
	Tags    map[string]string
}

// KeyPairInfo describes a key pair.
type KeyPairInfo struct {
	ID          string
	Name        string
	Fingerprint string
	KeyType     string
	PublicKey   string
	PrivateKey  string // only returned on create
	CreatedAt   string
	Tags        map[string]string
}

// Compute is the interface that compute provider implementations must satisfy.
type Compute interface {
	RunInstances(ctx context.Context, config InstanceConfig, count int) ([]Instance, error)
	StartInstances(ctx context.Context, instanceIDs []string) error
	StopInstances(ctx context.Context, instanceIDs []string) error
	RebootInstances(ctx context.Context, instanceIDs []string) error
	TerminateInstances(ctx context.Context, instanceIDs []string) error
	DescribeInstances(
		ctx context.Context, instanceIDs []string, filters []DescribeFilter, opts ...DescribeInstancesOptions,
	) ([]Instance, error)
	ModifyInstance(ctx context.Context, instanceID string, input ModifyInstanceInput) error

	// Auto-Scaling Groups
	CreateAutoScalingGroup(ctx context.Context, config AutoScalingGroupConfig) (*AutoScalingGroup, error)
	DeleteAutoScalingGroup(ctx context.Context, name string, forceDelete bool) error
	GetAutoScalingGroup(ctx context.Context, name string) (*AutoScalingGroup, error)
	ListAutoScalingGroups(ctx context.Context) ([]AutoScalingGroup, error)
	UpdateAutoScalingGroup(ctx context.Context, name string, desired, minSize, maxSize int) error
	SetDesiredCapacity(ctx context.Context, name string, desired int) error

	// Scaling Policies
	PutScalingPolicy(ctx context.Context, policy ScalingPolicy) error
	DeleteScalingPolicy(ctx context.Context, asgName, policyName string) error
	ExecuteScalingPolicy(ctx context.Context, asgName, policyName string) error

	// Spot/Preemptible Instances
	RequestSpotInstances(ctx context.Context, config SpotRequestConfig) ([]SpotInstanceRequest, error)
	CancelSpotRequests(ctx context.Context, requestIDs []string) error
	DescribeSpotRequests(ctx context.Context, requestIDs []string) ([]SpotInstanceRequest, error)

	// Launch Templates
	CreateLaunchTemplate(ctx context.Context, config LaunchTemplateConfig) (*LaunchTemplate, error)
	DeleteLaunchTemplate(ctx context.Context, name string) error
	GetLaunchTemplate(ctx context.Context, name string) (*LaunchTemplate, error)
	ListLaunchTemplates(ctx context.Context) ([]LaunchTemplate, error)

	// Volumes
	CreateVolume(ctx context.Context, config VolumeConfig) (*VolumeInfo, error)
	DeleteVolume(ctx context.Context, id string) error
	DescribeVolumes(ctx context.Context, ids []string) ([]VolumeInfo, error)
	AttachVolume(ctx context.Context, volumeID, instanceID, device string) error
	DetachVolume(ctx context.Context, volumeID string) error

	// Snapshots
	CreateSnapshot(ctx context.Context, config SnapshotConfig) (*SnapshotInfo, error)
	DeleteSnapshot(ctx context.Context, id string) error
	DescribeSnapshots(ctx context.Context, ids []string) ([]SnapshotInfo, error)

	// Images
	CreateImage(ctx context.Context, config ImageConfig) (*ImageInfo, error)
	DeregisterImage(ctx context.Context, id string) error
	DescribeImages(ctx context.Context, ids []string) ([]ImageInfo, error)

	// Key Pairs
	CreateKeyPair(ctx context.Context, config KeyPairConfig) (*KeyPairInfo, error)
	DeleteKeyPair(ctx context.Context, name string) error
	DescribeKeyPairs(ctx context.Context, names []string) ([]KeyPairInfo, error)
}
