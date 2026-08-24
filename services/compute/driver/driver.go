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
	// ClientToken provides idempotency for RunInstances (AWS): a retry with the
	// same case-sensitive token returns the already-launched instances instead
	// of provisioning new ones. Empty disables dedup. Ignored by Azure/GCP.
	ClientToken string
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
	// ReservationID groups instances launched by one RunInstances call under a
	// shared AWS reservation (r-xxxx). Empty for providers with no reservation
	// concept; the wire layer then falls back to a per-instance reservation.
	ReservationID string
	// KeyName is the key pair the instance was launched with (AWS), when set.
	KeyName string
	// Monitoring is the CloudWatch detailed-monitoring state
	// ("disabled"/"enabled"), when known (AWS). Empty renders as "disabled".
	Monitoring string
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
	// Encrypted requests an encrypted volume (AWS EBS). Echoed back so
	// Terraform's aws_ebs_volume does not see perpetual drift.
	Encrypted bool
	// SnapshotID is the source snapshot the volume is restored from, when set.
	SnapshotID string
	// KmsKeyID is the KMS key protecting the volume's encryption key, when set.
	KmsKeyID string
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
	// Encrypted indicates the volume is encrypted (AWS EBS).
	Encrypted bool
	// SnapshotID is the source snapshot the volume was restored from, when set.
	SnapshotID string
	// KmsKeyID is the KMS key protecting the volume's encryption key, when set.
	KmsKeyID string
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
	// OwnerID is the account that owns the snapshot (AWS ownerId).
	OwnerID string
	// Progress is the completion percentage (e.g. "100%"), for waiters.
	Progress string
	// Encrypted indicates the snapshot is encrypted.
	Encrypted bool
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
	// OwnerID is the account that owns the image (AWS imageOwnerId).
	OwnerID string
	// Architecture is the CPU architecture (e.g. "x86_64", "arm64").
	Architecture string
	// RootDeviceType is "ebs" or "instance-store".
	RootDeviceType string
	// RootDeviceName is the root device (e.g. "/dev/sda1").
	RootDeviceName string
	// VirtualizationType is "hvm" or "paravirtual".
	VirtualizationType string
	// Hypervisor is "xen" or "ovm".
	Hypervisor string
	// ImageType is "machine", "kernel", or "ramdisk".
	ImageType string
	// PlatformDetails is the billing platform detail (e.g. "Linux/UNIX").
	PlatformDetails string
	// BlockDeviceMappings are the image's block device mappings.
	BlockDeviceMappings []ImageBlockDeviceMapping
}

// ImageBlockDeviceMapping is one entry in an image's block device mapping set.
type ImageBlockDeviceMapping struct {
	DeviceName          string
	SnapshotID          string
	VolumeSize          int
	VolumeType          string
	DeleteOnTermination bool
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

// ConsoleReader is an optional capability a Compute implementation may provide
// to return an instance's console output. It is served by the real
// config.ComputeEngine backing an instance; a provider with no engine wired
// returns empty output. The server type-asserts for it so implementations that
// do not support console output (Azure, GCP) are unaffected.
type ConsoleReader interface {
	GetConsoleOutput(ctx context.Context, instanceID string) ([]byte, error)
}

// CopySnapshotInput describes an AWS EC2 CopySnapshot request. SourceRegion is
// required by the API but ignored by the single-region emulator.
type CopySnapshotInput struct {
	SourceRegion     string
	SourceSnapshotID string
	Description      string
	Encrypted        bool
	KmsKeyID         string
	Tags             map[string]string
}

// RegisterImageInput describes an AWS EC2 RegisterImage request.
type RegisterImageInput struct {
	Name                string
	Description         string
	Architecture        string
	RootDeviceName      string
	VirtualizationType  string
	BlockDeviceMappings []ImageBlockDeviceMapping
	Tags                map[string]string
}

// ImportKeyPairInput describes an AWS EC2 ImportKeyPair request. PublicKeyMaterial
// is the decoded public key (OpenSSH or PEM), not the base64 wire form.
type ImportKeyPairInput struct {
	Name              string
	PublicKeyMaterial []byte
	Tags              map[string]string
}

// ModifyVolumeInput describes an AWS EC2 ModifyVolume request. A zero numeric
// field or empty VolumeType means "leave unchanged".
type ModifyVolumeInput struct {
	VolumeID   string
	Size       int
	IOPS       int
	Throughput int
	VolumeType string
}

// VolumeModification describes the state of an in-progress AWS EC2 ModifyVolume,
// mirroring the API's VolumeModification structure.
type VolumeModification struct {
	VolumeID           string
	ModificationState  string // "modifying", "optimizing", "completed", "failed"
	StartTime          string
	Progress           int
	OriginalSize       int
	OriginalIOPS       int
	OriginalThroughput int
	OriginalVolumeType string
	TargetSize         int
	TargetIOPS         int
	TargetThroughput   int
	TargetVolumeType   string
}

// SnapshotCopier is an optional AWS-only capability for EC2 CopySnapshot. It is
// discovered by type assertion; clouds that do not model EBS snapshot copies
// (Azure, GCP, OCI) simply do not implement it.
type SnapshotCopier interface {
	CopySnapshot(ctx context.Context, input CopySnapshotInput) (*SnapshotInfo, error)
}

// ImageRegistrar is an optional AWS-only capability for EC2 RegisterImage
// (registering an AMI from block device mappings). Discovered by type assertion.
type ImageRegistrar interface {
	RegisterImage(ctx context.Context, input RegisterImageInput) (*ImageInfo, error)
}

// KeyPairImporter is an optional AWS-only capability for EC2 ImportKeyPair
// (importing an externally-generated public key). Discovered by type assertion.
type KeyPairImporter interface {
	ImportKeyPair(ctx context.Context, input ImportKeyPairInput) (*KeyPairInfo, error)
}

// VolumeModifier is an optional AWS-only capability for EC2 ModifyVolume
// (elastic volume resize / IOPS / throughput / type change). Discovered by
// type assertion.
type VolumeModifier interface {
	ModifyVolume(ctx context.Context, input ModifyVolumeInput) (*VolumeModification, error)
}
