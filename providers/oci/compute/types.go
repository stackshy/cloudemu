package compute

// Shape is an OCI compute shape. Flexible shapes carry an OCPU and memory
// range the caller picks a point in; fixed shapes report the same value for
// both ends.
type Shape struct {
	Name                      string
	ProcessorDescription      string
	OCPUs                     float32
	MemoryInGBs               float32
	NetworkingBandwidthInGbps float32
	MaxVNICAttachments        int
	IsFlexible                bool
	MinOCPUs                  float32
	MaxOCPUs                  float32
	MinMemoryInGBs            float32
	MaxMemoryInGBs            float32
}

// ShapeConfig is the OCPU and memory a flexible shape was launched with.
type ShapeConfig struct {
	OCPUs                     float32
	MemoryInGBs               float32
	NetworkingBandwidthInGbps float32
}

// SourceDetails names what an instance or boot volume was created from.
type SourceDetails struct {
	// SourceType is "image", "bootVolume", "volume", "volumeBackup" or
	// "bootVolumeBackup".
	SourceType string
	ID         string
	// BootVolumeSizeInGBs overrides the image's default boot volume size.
	BootVolumeSizeInGBs int
}

// AgentConfig is the Oracle Cloud Agent configuration on an instance.
type AgentConfig struct {
	IsMonitoringDisabled  bool
	IsManagementDisabled  bool
	AreAllPluginsDisabled bool
}

// InstanceDetails is OCI's view of an instance: everything the portable
// Instance projection has no field for.
type InstanceDetails struct {
	DisplayName        string
	AvailabilityDomain string
	FaultDomain        string
	// Metadata is OCI's instance metadata. An SSH public key lives here under
	// ssh_authorized_keys; OCI models no key pair resource.
	Metadata         map[string]string
	ExtendedMetadata map[string]any
	ShapeConfig      *ShapeConfig
	SourceDetails    SourceDetails
	AgentConfig      *AgentConfig
	// IsPreemptible marks a preemptible instance, OCI's spot equivalent.
	IsPreemptible bool
	// PreserveBootVolume is the terminate-time choice recorded on the
	// instance, so the boot volume survives a TerminateInstance that asked it.
	PreserveBootVolume bool
	DedicatedVMHostID  string
	// BootVolumeID is the boot volume created with the instance.
	BootVolumeID string
	// VNICID is the primary VNIC placed in the instance's subnet.
	VNICID string
	// HostnameLabel is the DNS label of the primary VNIC.
	HostnameLabel string
	// InstancePoolID is set when the instance is a member of an instance pool.
	InstancePoolID string
	// InstanceConfigurationID is set when the instance was launched from one.
	InstanceConfigurationID string
}

// VolumeAttachment is OCI's block volume attachment, a resource with its own
// OCID. The portable driver models an attachment as two fields on the volume.
type VolumeAttachment struct {
	ID                             string
	InstanceID                     string
	VolumeID                       string
	AvailabilityDomain             string
	DisplayName                    string
	Device                         string
	AttachmentType                 string
	IsReadOnly                     bool
	IsShareable                    bool
	IsPVEncryptionInTransitEnabled bool
	LifecycleState                 string
	TimeCreated                    string
}

// BootVolume is the volume an instance boots from. OCI creates one with every
// instance and lets it outlive the instance.
type BootVolume struct {
	ID                 string
	AvailabilityDomain string
	DisplayName        string
	SizeInGBs          int
	VpusPerGB          int
	ImageID            string
	SourceDetails      SourceDetails
	VolumeGroupID      string
	LifecycleState     string
	IsHydrated         bool
	TimeCreated        string
	Tags               map[string]string
}

// BootVolumeAttachment ties a boot volume to the instance booting from it.
type BootVolumeAttachment struct {
	ID                 string
	InstanceID         string
	BootVolumeID       string
	AvailabilityDomain string
	DisplayName        string
	LifecycleState     string
	TimeCreated        string
}

// VolumeGroup is a consistency group of block and boot volumes.
type VolumeGroup struct {
	ID                 string
	AvailabilityDomain string
	DisplayName        string
	VolumeIDs          []string
	SizeInGBs          int
	SourceType         string
	SourceID           string
	LifecycleState     string
	TimeCreated        string
	Tags               map[string]string
}

// VNICAttachment ties an instance to the VNIC the VCN service created for it.
type VNICAttachment struct {
	ID                 string
	InstanceID         string
	VNICID             string
	SubnetID           string
	AvailabilityDomain string
	DisplayName        string
	NICIndex           int
	LifecycleState     string
	TimeCreated        string
}

// InstancePool is OCI's managed group of identical instances, the equivalent
// of an auto-scaling group.
type InstancePool struct {
	ID                      string
	DisplayName             string
	InstanceConfigurationID string
	Size                    int
	LifecycleState          string
	Placements              []PoolPlacement
	LoadBalancers           []PoolLoadBalancer
	InstanceIDs             []string
	TimeCreated             string
	Tags                    map[string]string
}

// PoolPlacement is where an instance pool places its instances.
type PoolPlacement struct {
	AvailabilityDomain string
	PrimarySubnetID    string
	FaultDomains       []string
}

// PoolLoadBalancer attaches an instance pool's instances to a backend set.
type PoolLoadBalancer struct {
	ID             string
	LoadBalancerID string
	BackendSetName string
	Port           int
	VNICSelection  string
	LifecycleState string
}

// InstanceConfiguration is OCI's saved launch specification, the equivalent of
// a launch template.
type InstanceConfiguration struct {
	ID          string
	DisplayName string
	// InstanceType is the configuration's source, always "compute".
	InstanceType string
	Launch       LaunchSpec
	TimeCreated  string
	Tags         map[string]string
}

// LaunchSpec is the instance an instance configuration launches.
type LaunchSpec struct {
	AvailabilityDomain string
	FaultDomain        string
	DisplayName        string
	Shape              string
	ShapeConfig        *ShapeConfig
	ImageID            string
	SubnetID           string
	NSGIDs             []string
	Metadata           map[string]string
	IsPreemptible      bool
	Tags               map[string]string
}

// PoolInstance is a member of an instance pool as ListInstancePoolInstances
// reports it.
type PoolInstance struct {
	ID                   string
	InstanceID           string
	AvailabilityDomain   string
	DisplayName          string
	Shape                string
	State                string
	LoadBalancerBackends []string
	TimeCreated          string
}

// Update is a partial update: a nil pointer leaves the field alone.
type Update struct {
	DisplayName *string
	Tags        map[string]string
}
