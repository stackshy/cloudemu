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
	// IamInstanceProfileARN / IamInstanceProfileName reference the IAM instance
	// profile to attach at launch (AWS RunInstances IamInstanceProfile.Arn /
	// .Name). Callers supply one or the other; the AWS provider resolves the
	// reference to the profile's ARN and ID. Ignored by Azure/GCP.
	IamInstanceProfileARN  string
	IamInstanceProfileName string
	// Identity is the ARM managed-identity block to attach at launch (Azure
	// VM identity.type / identity.userAssignedIdentities), when set. Only
	// Type and UserAssigned (the caller-supplied identity resource IDs) are
	// meaningful on input — PrincipalID/TenantID/ClientID are provider-
	// synthesized output, ignored here. Ignored by AWS/GCP.
	Identity *ManagedIdentity
	// NetworkInterfaces are the NICs referenced by the VM's
	// networkProfile.networkInterfaces (Azure), resolved from each entry's ARM
	// resource id down to the (resourceGroup, name) pair the networking mock
	// is keyed by. The provider attaches each one to the launched instance,
	// setting the NIC's properties.virtualMachine back-reference. Ignored by
	// AWS/GCP.
	NetworkInterfaces []AzureNICRef
}

// AzureNICRef identifies a Network Interface (Microsoft.Network/networkInterfaces)
// by its (resourceGroup, name) pair, as resolved from the ARM resource id
// referenced in networkProfile.networkInterfaces. Azure-only.
type AzureNICRef struct {
	ResourceGroup string
	Name          string
}

// IamInstanceProfile is the IAM instance profile association reported on an EC2
// instance (arn + id), matching the EC2 IamInstanceProfile response element.
type IamInstanceProfile struct {
	ARN string
	ID  string
}

// ManagedIdentity models an Azure VM managed-identity block: system-assigned
// and/or user-assigned identity configuration. Ignored by AWS/GCP.
type ManagedIdentity struct {
	// Type is one of "None", "SystemAssigned", "UserAssigned",
	// "SystemAssigned,UserAssigned".
	Type string
	// PrincipalID/TenantID are synthesized for a system-assigned identity
	// (empty otherwise or on input).
	PrincipalID string
	TenantID    string
	// UserAssigned holds the attached user-assigned identities, keyed by
	// their full ARM resource ID. On input, only the key (the identity to
	// attach) matters; the provider fills in each entry's synthesized
	// PrincipalID/ClientID for output.
	UserAssigned map[string]UserAssignedIdentity
}

// UserAssignedIdentity is one entry of ManagedIdentity.UserAssigned: the
// synthesized principal/client id pair Azure reports for an attached
// user-assigned identity.
type UserAssignedIdentity struct {
	PrincipalID string
	ClientID    string
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
	// PowerState is the Azure power state ("running"/"stopped"/"deallocated"/
	// "starting"/"stopping"/"deallocating"), when known. It distinguishes a
	// PowerOff'd VM (stopped, still allocated) from a Deallocated one, a
	// distinction the lifecycle State field cannot carry. Empty for AWS/GCP.
	PowerState string
	// Generalized is true once the VM has been generalized (Azure Generalize
	// action): its OS-specific state has been removed so it can be captured
	// into a reusable image. Empty/false for AWS/GCP and for un-generalized
	// Azure VMs.
	Generalized bool
	// Operator carries service-provider managed-resource metadata. It is nil
	// for ordinary (unmanaged) instances.
	Operator *OperatorInfo
	// MetadataOptions is the instance's IMDS configuration (AWS). The zero value
	// means "not set"; the wire layer fills in EC2 defaults when rendering.
	MetadataOptions MetadataOptions
	// IamInstanceProfile is the IAM instance profile attached to the instance
	// (AWS), nil when none is attached.
	IamInstanceProfile *IamInstanceProfile
	// Identity is the resolved managed-identity block (Azure), nil when no
	// identity is attached (identity.type "None" or unset).
	Identity *ManagedIdentity
}

// MetadataOptions is an instance's IMDS (instance metadata service)
// configuration, set at launch and changed by ModifyInstanceMetadataOptions.
type MetadataOptions struct {
	State                   string // "applied", "pending"
	HTTPTokens              string // "optional" (IMDSv1+v2), "required" (IMDSv2-only)
	HTTPPutResponseHopLimit int
	HTTPEndpoint            string // "enabled", "disabled"
	HTTPProtocolIPv6        string // "enabled", "disabled"
	InstanceMetadataTags    string // "enabled", "disabled"
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
	// LaunchSource records how the group launches instances (AWS). Exactly one of
	// LaunchConfigurationName or the LaunchTemplate trio is set; both empty means
	// the group was created from an existing instance or a mixed-instances policy.
	LaunchConfigurationName string
	LaunchTemplateName      string
	LaunchTemplateID        string
	LaunchTemplateVersion   string
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
	// LaunchSource echoes the group's launch source set at create time (AWS).
	LaunchConfigurationName string
	LaunchTemplateName      string
	LaunchTemplateID        string
	LaunchTemplateVersion   string
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
	// DefaultVersion is the version number RunInstances uses when no version is
	// named (AWS defaultVersionNumber). LatestVersion is the highest version
	// number created so far (AWS latestVersionNumber). CreatedBy is the ARN of
	// the principal that created the template. Tags are the template-level
	// resource tags. These are populated by AWS EC2; providers with no launch
	// template versioning leave them zero.
	DefaultVersion int
	LatestVersion  int
	CreatedBy      string
	Tags           map[string]string
}

// LaunchTemplateConfig configures a launch template.
type LaunchTemplateConfig struct {
	Name           string
	InstanceConfig InstanceConfig
	// Tags are template-level resource tags (AWS TagSpecification on the
	// launch-template resource). VersionDescription annotates the initial (v1)
	// version. Both are AWS-specific and ignored by providers without launch
	// template versioning.
	Tags               map[string]string
	VersionDescription string
}

// LaunchTemplateVersion is one immutable version of a launch template (AWS EC2).
// Versions are numbered sequentially per template starting at 1.
type LaunchTemplateVersion struct {
	LaunchTemplateID   string
	LaunchTemplateName string
	VersionNumber      int
	DefaultVersion     bool
	CreatedBy          string
	CreateTime         string
	VersionDescription string
	InstanceConfig     InstanceConfig
}

// CreateLaunchTemplateVersionInput carries the parameters for
// CreateLaunchTemplateVersion. Exactly one of Name/ID identifies the template.
// When SourceVersion is set the new version inherits that version's parameters,
// with the non-zero fields of InstanceConfig overlaid on top.
type CreateLaunchTemplateVersionInput struct {
	Name               string
	ID                 string
	SourceVersion      string
	VersionDescription string
	InstanceConfig     InstanceConfig
}

// DescribeLaunchTemplateVersionsInput carries the filters for
// DescribeLaunchTemplateVersions. Exactly one of Name/ID identifies the
// template. Versions is an explicit version-number list (also accepting the
// "$Latest"/"$Default" tokens); MinVersion/MaxVersion bound the range. Paging
// (MaxResults/NextToken) is applied by the wire layer.
type DescribeLaunchTemplateVersionsInput struct {
	Name       string
	ID         string
	Versions   []string
	MinVersion string
	MaxVersion string
}

// LaunchTemplateVersioner is an AWS-only optional capability implementing launch
// template versioning. Only the EC2 provider implements it; the wire handler
// type-asserts for it, so providers without versioning (Azure, GCP) are
// unaffected.
type LaunchTemplateVersioner interface {
	// CreateLaunchTemplateVersion appends a new immutable version to a template.
	CreateLaunchTemplateVersion(ctx context.Context, input CreateLaunchTemplateVersionInput) (*LaunchTemplateVersion, error)
	// DescribeLaunchTemplateVersions returns a template's versions (filtered,
	// sorted ascending by version number).
	DescribeLaunchTemplateVersions(ctx context.Context, input DescribeLaunchTemplateVersionsInput) ([]LaunchTemplateVersion, error)
	// GetLaunchTemplateData synthesizes launch-template data from a running
	// instance's configuration.
	GetLaunchTemplateData(ctx context.Context, instanceID string) (*InstanceConfig, error)
}

// ModifyLaunchTemplateInput carries the parameters for ModifyLaunchTemplate.
// Exactly one of Name/ID identifies the template; DefaultVersion is the version
// number (or "$Latest"/"$Default") to promote to the template's default.
type ModifyLaunchTemplateInput struct {
	Name           string
	ID             string
	DefaultVersion string
}

// LaunchTemplateModifier is an AWS-only optional capability implementing
// ModifyLaunchTemplate (promoting a template version to the default). Only the
// EC2 provider implements it; the wire handler type-asserts for it, so
// providers without launch templates (Azure, GCP) are unaffected.
type LaunchTemplateModifier interface {
	ModifyLaunchTemplate(ctx context.Context, input ModifyLaunchTemplateInput) (*LaunchTemplate, error)
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
	// CreateVolumePermissions holds the createVolumePermission attribute set by
	// ModifySnapshotAttribute (snapshot sharing). Empty means private.
	CreateVolumePermissions []SnapshotCreateVolumePermission
}

// SnapshotCreateVolumePermission is one createVolumePermission grant: either a
// Group ("all" for public) or a specific UserID (account id).
type SnapshotCreateVolumePermission struct {
	Group  string
	UserID string
}

// ModifySnapshotAttributeInput describes an AWS EC2 ModifySnapshotAttribute
// request for the createVolumePermission attribute. OperationType is "add" or
// "remove"; Groups and UserIDs are the grants added or removed.
type ModifySnapshotAttributeInput struct {
	SnapshotID    string
	OperationType string
	Groups        []string
	UserIDs       []string
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
	// LaunchPermissions holds the launchPermission attribute set by
	// ModifyImageAttribute (AMI sharing). Empty means private.
	LaunchPermissions []ImageLaunchPermission
}

// ImageLaunchPermission is one launchPermission grant on an AMI: either a Group
// ("all" for public) or a specific UserID (account id).
type ImageLaunchPermission struct {
	Group  string
	UserID string
}

// CopyImageInput describes an AWS EC2 CopyImage request. SourceRegion is
// required by the API but ignored by the single-region emulator.
type CopyImageInput struct {
	SourceRegion  string
	SourceImageID string
	Name          string
	Description   string
	Tags          map[string]string
}

// ModifyImageAttributeInput describes an AWS EC2 ModifyImageAttribute request
// for the launchPermission attribute. OperationType is "add" or "remove".
type ModifyImageAttributeInput struct {
	ImageID       string
	OperationType string
	Groups        []string
	UserIDs       []string
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

// InstanceMetadataModifier is an optional AWS-only capability for
// ec2:ModifyInstanceMetadataOptions (IMDS settings, e.g. enforcing IMDSv2 via
// HttpTokens=required). A zero-value field in the update means "leave
// unchanged". Discovered by type assertion.
type InstanceMetadataModifier interface {
	ModifyInstanceMetadataOptions(ctx context.Context, instanceID string, update MetadataOptions) (*MetadataOptions, error)
}

// AzureVMController is an optional Azure-only capability supporting the ARM
// virtualMachines operations that have no AWS/GCP equivalent: the PowerOff vs
// Deallocate distinction (PowerOff stops the guest but keeps the VM allocated;
// Deallocate releases the compute) and the idempotent CreateOrUpdate PUT
// (updating an existing VM's mutable config in place rather than provisioning a
// duplicate). Only the Azure VM mock implements it; the wire handler type-
// asserts for it, so AWS/GCP are unaffected.
type AzureVMController interface {
	// PowerOff stops the guest OS while keeping the VM allocated
	// (PowerState/stopped).
	PowerOff(ctx context.Context, instanceID string) error
	// Deallocate stops the guest and releases the allocated compute
	// (PowerState/deallocated).
	Deallocate(ctx context.Context, instanceID string) error
	// UpdateInstance overwrites the mutable configuration of an existing
	// instance in place (preserving its ID and launch time), for idempotent
	// ARM CreateOrUpdate.
	UpdateInstance(ctx context.Context, instanceID string, cfg InstanceConfig) error
	// GeneralizeInstance marks an instance as generalized (Azure Generalize
	// action), a precondition for capturing it into a reusable image. It is
	// idempotent: generalizing an already-generalized VM succeeds.
	GeneralizeInstance(ctx context.Context, instanceID string) error
}

// AzureDiskAccessor is an optional Azure-only capability for the managed-disk
// beginGetAccess / endGetAccess actions (DisksClient.BeginGrantAccess /
// BeginRevokeAccess): a time-bounded SAS URI is issued for exporting or
// importing the disk's contents, and later revoked. Only the Azure VM mock
// implements it; the wire handler type-asserts for it, so AWS/GCP are
// unaffected.
type AzureDiskAccessor interface {
	// GrantDiskAccess issues a time-bounded SAS URI granting the requested
	// access level ("Read"/"Write") to the disk for durationSeconds.
	GrantDiskAccess(ctx context.Context, volumeID, access string, durationSeconds int) (string, error)
	// RevokeDiskAccess revokes any SAS access previously granted to the disk.
	RevokeDiskAccess(ctx context.Context, volumeID string) error
}

// AzureSSHKeyUpdater is an optional Azure-only capability for the sshPublicKeys
// PATCH Update operation, which updates a key resource's public key and/or tags
// in place. Only the Azure VM mock implements it; the wire handler type-asserts
// for it, so AWS/GCP are unaffected.
type AzureSSHKeyUpdater interface {
	// UpdateKeyPair updates the public key and/or tags of an existing key pair.
	// A nil publicKey / tags leaves that field unchanged; a non-nil tags map
	// replaces the resource's tags.
	UpdateKeyPair(ctx context.Context, name string, publicKey *string, tags map[string]string) (*KeyPairInfo, error)
}

// KeyPairGenerator is an optional Azure-only capability for the ARM
// sshPublicKeys generateKeyPair action, which generates a fresh RSA key pair
// server-side, stores the public key on the resource, and returns both the
// public and (one-time) private key. Only the Azure mock implements it.
type KeyPairGenerator interface {
	GenerateKeyPair(ctx context.Context, name string) (*KeyPairInfo, error)
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

// SnapshotAttributeModifier is an optional AWS-only capability for the EC2
// snapshot-sharing round-trip: ModifySnapshotAttribute persists the
// createVolumePermission attribute and DescribeSnapshotAttribute reads it back.
// Discovered by type assertion.
type SnapshotAttributeModifier interface {
	ModifySnapshotAttribute(ctx context.Context, input ModifySnapshotAttributeInput) error
	DescribeSnapshotVolumePermissions(ctx context.Context, snapshotID string) ([]SnapshotCreateVolumePermission, error)
}

// ImageCopier is an optional AWS-only capability for EC2 CopyImage (aws_ami_copy).
// Discovered by type assertion.
type ImageCopier interface {
	CopyImage(ctx context.Context, input CopyImageInput) (*ImageInfo, error)
}

// PlacementGroupConfig describes an EC2 placement group to create.
type PlacementGroupConfig struct {
	Name           string
	Strategy       string // "cluster", "spread", or "partition"
	PartitionCount int
	SpreadLevel    string
	Tags           map[string]string
}

// PlacementGroup describes an EC2 placement group.
type PlacementGroup struct {
	ID             string
	Name           string
	Strategy       string
	State          string // "available", "pending", "deleting", "deleted"
	PartitionCount int
	SpreadLevel    string
	Tags           map[string]string
}

// PlacementGroups is an optional AWS-only capability for EC2 placement groups
// (aws_placement_group). Discovered by type assertion.
type PlacementGroups interface {
	CreatePlacementGroup(ctx context.Context, config PlacementGroupConfig) (*PlacementGroup, error)
	DeletePlacementGroup(ctx context.Context, name string) error
	DescribePlacementGroups(ctx context.Context, names, ids []string) ([]PlacementGroup, error)
}

// ImageAttributeModifier is an optional AWS-only capability for the EC2 AMI
// launchPermission round-trip: ModifyImageAttribute persists launchPermission
// grants (aws_ami_launch_permission) and DescribeImageAttribute reads them back.
// Discovered by type assertion.
type ImageAttributeModifier interface {
	ModifyImageAttribute(ctx context.Context, input ModifyImageAttributeInput) error
	DescribeImageLaunchPermissions(ctx context.Context, imageID string) ([]ImageLaunchPermission, error)
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
