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

// Compute is the interface that compute provider implementations must satisfy.
type Compute interface {
	RunInstances(ctx context.Context, config InstanceConfig, count int) ([]Instance, error)
	StartInstances(ctx context.Context, instanceIDs []string) error
	StopInstances(ctx context.Context, instanceIDs []string) error
	RebootInstances(ctx context.Context, instanceIDs []string) error
	TerminateInstances(ctx context.Context, instanceIDs []string) error
	DescribeInstances(ctx context.Context, instanceIDs []string, filters []DescribeFilter) ([]Instance, error)
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
}
