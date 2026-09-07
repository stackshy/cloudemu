package compute

// definedTags is OCI's namespaced tag map. CloudEmu stores freeform tags only,
// so it is always rendered empty.
type definedTags map[string]map[string]string

// changeCompartmentRequest is the body of every changeCompartment action.
type changeCompartmentRequest struct {
	CompartmentID string `json:"compartmentId"`
}

// shapeConfigWire is the OCPU and memory of a flexible shape.
type shapeConfigWire struct {
	Ocpus                     float32 `json:"ocpus,omitempty"`
	MemoryInGBs               float32 `json:"memoryInGBs,omitempty"`
	NetworkingBandwidthInGbps float32 `json:"networkingBandwidthInGbps,omitempty"`
}

// sourceDetailsWire names what an instance or volume was created from.
type sourceDetailsWire struct {
	SourceType          string `json:"sourceType"`
	ImageID             string `json:"imageId,omitempty"`
	ID                  string `json:"id,omitempty"`
	BootVolumeID        string `json:"bootVolumeId,omitempty"`
	BootVolumeSizeInGBs int    `json:"bootVolumeSizeInGBs,omitempty"`
}

// agentConfigWire is the Oracle Cloud Agent configuration.
type agentConfigWire struct {
	IsMonitoringDisabled  bool `json:"isMonitoringDisabled"`
	IsManagementDisabled  bool `json:"isManagementDisabled"`
	AreAllPluginsDisabled bool `json:"areAllPluginsDisabled"`
}

// createVnicDetails is the primary VNIC a launch places in a subnet.
type createVnicDetails struct {
	SubnetID       string            `json:"subnetId"`
	DisplayName    string            `json:"displayName,omitempty"`
	HostnameLabel  string            `json:"hostnameLabel,omitempty"`
	NsgIDs         []string          `json:"nsgIds,omitempty"`
	AssignPublicIP *bool             `json:"assignPublicIp,omitempty"`
	PrivateIP      string            `json:"privateIp,omitempty"`
	FreeformTags   map[string]string `json:"freeformTags,omitempty"`
}

// preemptibleInstanceConfig marks a preemptible (spot) instance.
type preemptibleInstanceConfig struct {
	PreemptionAction preemptionAction `json:"preemptionAction"`
}

type preemptionAction struct {
	Type               string `json:"type"`
	PreserveBootVolume bool   `json:"preserveBootVolume,omitempty"`
}

// launchInstanceRequest is the body of LaunchInstance.
type launchInstanceRequest struct {
	CompartmentID             string                     `json:"compartmentId"`
	AvailabilityDomain        string                     `json:"availabilityDomain"`
	FaultDomain               string                     `json:"faultDomain,omitempty"`
	DisplayName               string                     `json:"displayName,omitempty"`
	Shape                     string                     `json:"shape"`
	ShapeConfig               *shapeConfigWire           `json:"shapeConfig,omitempty"`
	ImageID                   string                     `json:"imageId,omitempty"`
	SourceDetails             *sourceDetailsWire         `json:"sourceDetails,omitempty"`
	CreateVnicDetails         *createVnicDetails         `json:"createVnicDetails,omitempty"`
	SubnetID                  string                     `json:"subnetId,omitempty"`
	Metadata                  map[string]string          `json:"metadata,omitempty"`
	ExtendedMetadata          map[string]any             `json:"extendedMetadata,omitempty"`
	AgentConfig               *agentConfigWire           `json:"agentConfig,omitempty"`
	PreemptibleInstanceConfig *preemptibleInstanceConfig `json:"preemptibleInstanceConfig,omitempty"`
	DedicatedVMHostID         string                     `json:"dedicatedVmHostId,omitempty"`
	FreeformTags              map[string]string          `json:"freeformTags,omitempty"`
	DefinedTags               definedTags                `json:"definedTags,omitempty"`
}

// updateInstanceRequest is the body of UpdateInstance.
type updateInstanceRequest struct {
	DisplayName      string            `json:"displayName,omitempty"`
	Shape            string            `json:"shape,omitempty"`
	ShapeConfig      *shapeConfigWire  `json:"shapeConfig,omitempty"`
	FaultDomain      string            `json:"faultDomain,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	ExtendedMetadata map[string]any    `json:"extendedMetadata,omitempty"`
	AgentConfig      *agentConfigWire  `json:"agentConfig,omitempty"`
	FreeformTags     map[string]string `json:"freeformTags,omitempty"`
	DefinedTags      definedTags       `json:"definedTags,omitempty"`
}

// instanceResponse is OCI's Instance.
type instanceResponse struct {
	ID                        string                     `json:"id"`
	CompartmentID             string                     `json:"compartmentId"`
	AvailabilityDomain        string                     `json:"availabilityDomain"`
	FaultDomain               string                     `json:"faultDomain,omitempty"`
	DisplayName               string                     `json:"displayName"`
	Region                    string                     `json:"region"`
	Shape                     string                     `json:"shape"`
	ShapeConfig               *shapeConfigWire           `json:"shapeConfig,omitempty"`
	ImageID                   string                     `json:"imageId,omitempty"`
	SourceDetails             *sourceDetailsWire         `json:"sourceDetails,omitempty"`
	Metadata                  map[string]string          `json:"metadata"`
	ExtendedMetadata          map[string]any             `json:"extendedMetadata,omitempty"`
	AgentConfig               *agentConfigWire           `json:"agentConfig,omitempty"`
	PreemptibleInstanceConfig *preemptibleInstanceConfig `json:"preemptibleInstanceConfig,omitempty"`
	DedicatedVMHostID         string                     `json:"dedicatedVmHostId,omitempty"`
	LaunchMode                string                     `json:"launchMode"`
	LifecycleState            string                     `json:"lifecycleState"`
	TimeCreated               string                     `json:"timeCreated"`
	FreeformTags              map[string]string          `json:"freeformTags"`
	DefinedTags               definedTags                `json:"definedTags"`
}

// shapeResponse is OCI's Shape.
type shapeResponse struct {
	Shape                     string         `json:"shape"`
	ProcessorDescription      string         `json:"processorDescription,omitempty"`
	Ocpus                     float32        `json:"ocpus"`
	MemoryInGBs               float32        `json:"memoryInGBs"`
	NetworkingBandwidthInGbps float32        `json:"networkingBandwidthInGbps"`
	MaxVnicAttachments        int            `json:"maxVnicAttachments"`
	IsFlexible                bool           `json:"isFlexible"`
	OcpuOptions               *shapeRange    `json:"ocpuOptions,omitempty"`
	MemoryOptions             *shapeMemRange `json:"memoryOptions,omitempty"`
}

type shapeRange struct {
	Min float32 `json:"min"`
	Max float32 `json:"max"`
}

type shapeMemRange struct {
	MinInGBs float32 `json:"minInGBs"`
	MaxInGBs float32 `json:"maxInGBs"`
}

// imageRequest is the body of CreateImage and UpdateImage.
type imageRequest struct {
	CompartmentID      string             `json:"compartmentId,omitempty"`
	DisplayName        string             `json:"displayName,omitempty"`
	InstanceID         string             `json:"instanceId,omitempty"`
	ImageSourceDetails *sourceDetailsWire `json:"imageSourceDetails,omitempty"`
	LaunchMode         string             `json:"launchMode,omitempty"`
	FreeformTags       map[string]string  `json:"freeformTags,omitempty"`
	DefinedTags        definedTags        `json:"definedTags,omitempty"`
}

// imageResponse is OCI's Image.
type imageResponse struct {
	ID                     string            `json:"id"`
	CompartmentID          string            `json:"compartmentId"`
	DisplayName            string            `json:"displayName"`
	OperatingSystem        string            `json:"operatingSystem"`
	OperatingSystemVersion string            `json:"operatingSystemVersion"`
	LaunchMode             string            `json:"launchMode"`
	SizeInMBs              int               `json:"sizeInMBs"`
	BaseImageID            string            `json:"baseImageId,omitempty"`
	CreateImageAllowed     bool              `json:"createImageAllowed"`
	LifecycleState         string            `json:"lifecycleState"`
	TimeCreated            string            `json:"timeCreated"`
	FreeformTags           map[string]string `json:"freeformTags"`
	DefinedTags            definedTags       `json:"definedTags"`
}

// volumeRequest is the body of CreateVolume and UpdateVolume.
type volumeRequest struct {
	CompartmentID      string             `json:"compartmentId,omitempty"`
	AvailabilityDomain string             `json:"availabilityDomain,omitempty"`
	DisplayName        string             `json:"displayName,omitempty"`
	SizeInGBs          int                `json:"sizeInGBs,omitempty"`
	SizeInMBs          int                `json:"sizeInMBs,omitempty"`
	VpusPerGB          int                `json:"vpusPerGB,omitempty"`
	SourceDetails      *sourceDetailsWire `json:"sourceDetails,omitempty"`
	VolumeGroupID      string             `json:"volumeGroupId,omitempty"`
	FreeformTags       map[string]string  `json:"freeformTags,omitempty"`
	DefinedTags        definedTags        `json:"definedTags,omitempty"`
}

// volumeResponse is OCI's Volume.
type volumeResponse struct {
	ID                 string             `json:"id"`
	CompartmentID      string             `json:"compartmentId"`
	AvailabilityDomain string             `json:"availabilityDomain"`
	DisplayName        string             `json:"displayName"`
	SizeInGBs          int                `json:"sizeInGBs"`
	SizeInMBs          int                `json:"sizeInMBs"`
	VpusPerGB          int                `json:"vpusPerGB"`
	SourceDetails      *sourceDetailsWire `json:"sourceDetails,omitempty"`
	VolumeGroupID      string             `json:"volumeGroupId,omitempty"`
	IsHydrated         bool               `json:"isHydrated"`
	LifecycleState     string             `json:"lifecycleState"`
	TimeCreated        string             `json:"timeCreated"`
	FreeformTags       map[string]string  `json:"freeformTags"`
	DefinedTags        definedTags        `json:"definedTags"`
}

// volumeAttachmentRequest is the body of AttachVolume.
type volumeAttachmentRequest struct {
	Type                           string `json:"type,omitempty"`
	InstanceID                     string `json:"instanceId"`
	VolumeID                       string `json:"volumeId"`
	DisplayName                    string `json:"displayName,omitempty"`
	Device                         string `json:"device,omitempty"`
	IsReadOnly                     bool   `json:"isReadOnly,omitempty"`
	IsShareable                    bool   `json:"isShareable,omitempty"`
	IsPvEncryptionInTransitEnabled bool   `json:"isPvEncryptionInTransitEnabled,omitempty"`
}

// volumeAttachmentResponse is OCI's VolumeAttachment.
type volumeAttachmentResponse struct {
	ID                             string `json:"id"`
	CompartmentID                  string `json:"compartmentId"`
	AttachmentType                 string `json:"attachmentType"`
	AvailabilityDomain             string `json:"availabilityDomain"`
	InstanceID                     string `json:"instanceId"`
	VolumeID                       string `json:"volumeId"`
	DisplayName                    string `json:"displayName"`
	Device                         string `json:"device,omitempty"`
	IsReadOnly                     bool   `json:"isReadOnly"`
	IsShareable                    bool   `json:"isShareable"`
	IsPvEncryptionInTransitEnabled bool   `json:"isPvEncryptionInTransitEnabled"`
	LifecycleState                 string `json:"lifecycleState"`
	TimeCreated                    string `json:"timeCreated"`
}

// bootVolumeRequest is the body of CreateBootVolume and UpdateBootVolume.
type bootVolumeRequest struct {
	CompartmentID      string             `json:"compartmentId,omitempty"`
	AvailabilityDomain string             `json:"availabilityDomain,omitempty"`
	DisplayName        string             `json:"displayName,omitempty"`
	SizeInGBs          int                `json:"sizeInGBs,omitempty"`
	VpusPerGB          int                `json:"vpusPerGB,omitempty"`
	SourceDetails      *sourceDetailsWire `json:"sourceDetails,omitempty"`
	FreeformTags       map[string]string  `json:"freeformTags,omitempty"`
	DefinedTags        definedTags        `json:"definedTags,omitempty"`
}

// bootVolumeResponse is OCI's BootVolume.
type bootVolumeResponse struct {
	ID                 string             `json:"id"`
	CompartmentID      string             `json:"compartmentId"`
	AvailabilityDomain string             `json:"availabilityDomain"`
	DisplayName        string             `json:"displayName"`
	SizeInGBs          int                `json:"sizeInGBs"`
	VpusPerGB          int                `json:"vpusPerGB"`
	ImageID            string             `json:"imageId,omitempty"`
	SourceDetails      *sourceDetailsWire `json:"sourceDetails,omitempty"`
	VolumeGroupID      string             `json:"volumeGroupId,omitempty"`
	IsHydrated         bool               `json:"isHydrated"`
	LifecycleState     string             `json:"lifecycleState"`
	TimeCreated        string             `json:"timeCreated"`
	FreeformTags       map[string]string  `json:"freeformTags"`
	DefinedTags        definedTags        `json:"definedTags"`
}

// bootVolumeAttachmentRequest is the body of AttachBootVolume.
type bootVolumeAttachmentRequest struct {
	InstanceID   string `json:"instanceId"`
	BootVolumeID string `json:"bootVolumeId"`
	DisplayName  string `json:"displayName,omitempty"`
}

// bootVolumeAttachmentResponse is OCI's BootVolumeAttachment.
type bootVolumeAttachmentResponse struct {
	ID                 string `json:"id"`
	CompartmentID      string `json:"compartmentId"`
	AvailabilityDomain string `json:"availabilityDomain"`
	InstanceID         string `json:"instanceId"`
	BootVolumeID       string `json:"bootVolumeId"`
	DisplayName        string `json:"displayName"`
	LifecycleState     string `json:"lifecycleState"`
	TimeCreated        string `json:"timeCreated"`
}

// backupRequest is the body of CreateVolumeBackup and CreateBootVolumeBackup.
type backupRequest struct {
	VolumeID     string            `json:"volumeId,omitempty"`
	BootVolumeID string            `json:"bootVolumeId,omitempty"`
	DisplayName  string            `json:"displayName,omitempty"`
	Type         string            `json:"type,omitempty"`
	FreeformTags map[string]string `json:"freeformTags,omitempty"`
	DefinedTags  definedTags       `json:"definedTags,omitempty"`
}

// backupResponse is OCI's VolumeBackup.
type backupResponse struct {
	ID              string            `json:"id"`
	CompartmentID   string            `json:"compartmentId"`
	VolumeID        string            `json:"volumeId,omitempty"`
	BootVolumeID    string            `json:"bootVolumeId,omitempty"`
	DisplayName     string            `json:"displayName"`
	Type            string            `json:"type"`
	SizeInGBs       int               `json:"sizeInGBs"`
	UniqueSizeInGBs int               `json:"uniqueSizeInGBs"`
	SourceType      string            `json:"sourceType"`
	LifecycleState  string            `json:"lifecycleState"`
	TimeCreated     string            `json:"timeCreated"`
	FreeformTags    map[string]string `json:"freeformTags"`
	DefinedTags     definedTags       `json:"definedTags"`
}

// volumeGroupRequest is the body of CreateVolumeGroup and UpdateVolumeGroup.
type volumeGroupRequest struct {
	CompartmentID      string                 `json:"compartmentId,omitempty"`
	AvailabilityDomain string                 `json:"availabilityDomain,omitempty"`
	DisplayName        string                 `json:"displayName,omitempty"`
	SourceDetails      *volumeGroupSourceWire `json:"sourceDetails,omitempty"`
	VolumeIDs          []string               `json:"volumeIds,omitempty"`
	FreeformTags       map[string]string      `json:"freeformTags,omitempty"`
	DefinedTags        definedTags            `json:"definedTags,omitempty"`
}

// volumeGroupSourceWire names the volumes or group a group is built from.
type volumeGroupSourceWire struct {
	Type          string   `json:"type"`
	VolumeIDs     []string `json:"volumeIds,omitempty"`
	VolumeGroupID string   `json:"volumeGroupId,omitempty"`
}

// volumeGroupResponse is OCI's VolumeGroup.
type volumeGroupResponse struct {
	ID                 string            `json:"id"`
	CompartmentID      string            `json:"compartmentId"`
	AvailabilityDomain string            `json:"availabilityDomain"`
	DisplayName        string            `json:"displayName"`
	SizeInGBs          int               `json:"sizeInGBs"`
	SizeInMBs          int               `json:"sizeInMBs"`
	VolumeIDs          []string          `json:"volumeIds"`
	LifecycleState     string            `json:"lifecycleState"`
	TimeCreated        string            `json:"timeCreated"`
	FreeformTags       map[string]string `json:"freeformTags"`
	DefinedTags        definedTags       `json:"definedTags"`
}

// vnicAttachmentRequest is the body of AttachVnic.
type vnicAttachmentRequest struct {
	InstanceID        string             `json:"instanceId"`
	DisplayName       string             `json:"displayName,omitempty"`
	NicIndex          int                `json:"nicIndex,omitempty"`
	CreateVnicDetails *createVnicDetails `json:"createVnicDetails"`
}

// vnicAttachmentResponse is OCI's VnicAttachment.
type vnicAttachmentResponse struct {
	ID                 string `json:"id"`
	CompartmentID      string `json:"compartmentId"`
	AvailabilityDomain string `json:"availabilityDomain"`
	InstanceID         string `json:"instanceId"`
	VnicID             string `json:"vnicId,omitempty"`
	SubnetID           string `json:"subnetId"`
	DisplayName        string `json:"displayName"`
	NicIndex           int    `json:"nicIndex"`
	LifecycleState     string `json:"lifecycleState"`
	TimeCreated        string `json:"timeCreated"`
}

// instanceConfigurationRequest is the body of CreateInstanceConfiguration.
type instanceConfigurationRequest struct {
	CompartmentID   string                        `json:"compartmentId,omitempty"`
	DisplayName     string                        `json:"displayName,omitempty"`
	InstanceType    string                        `json:"instanceType,omitempty"`
	InstanceDetails *instanceConfigurationDetails `json:"instanceDetails,omitempty"`
	FreeformTags    map[string]string             `json:"freeformTags,omitempty"`
	DefinedTags     definedTags                   `json:"definedTags,omitempty"`
}

// instanceConfigurationDetails is the launch an instance configuration saves.
type instanceConfigurationDetails struct {
	InstanceType  string                       `json:"instanceType"`
	LaunchDetails *instanceConfigurationLaunch `json:"launchDetails,omitempty"`
}

// instanceConfigurationLaunch mirrors LaunchInstance's body, minus the fields
// OCI resolves at launch time.
type instanceConfigurationLaunch struct {
	AvailabilityDomain        string                     `json:"availabilityDomain,omitempty"`
	FaultDomain               string                     `json:"faultDomain,omitempty"`
	DisplayName               string                     `json:"displayName,omitempty"`
	Shape                     string                     `json:"shape"`
	ShapeConfig               *shapeConfigWire           `json:"shapeConfig,omitempty"`
	SourceDetails             *sourceDetailsWire         `json:"sourceDetails,omitempty"`
	CreateVnicDetails         *createVnicDetails         `json:"createVnicDetails,omitempty"`
	Metadata                  map[string]string          `json:"metadata,omitempty"`
	PreemptibleInstanceConfig *preemptibleInstanceConfig `json:"preemptibleInstanceConfig,omitempty"`
	FreeformTags              map[string]string          `json:"freeformTags,omitempty"`
}

// instanceConfigurationResponse is OCI's InstanceConfiguration.
type instanceConfigurationResponse struct {
	ID              string                        `json:"id"`
	CompartmentID   string                        `json:"compartmentId"`
	DisplayName     string                        `json:"displayName"`
	InstanceType    string                        `json:"instanceType"`
	InstanceDetails *instanceConfigurationDetails `json:"instanceDetails"`
	TimeCreated     string                        `json:"timeCreated"`
	FreeformTags    map[string]string             `json:"freeformTags"`
	DefinedTags     definedTags                   `json:"definedTags"`
}

// launchConfigurationRequest is the body of the launch action, an optional
// override of the saved launch details.
type launchConfigurationRequest struct {
	InstanceType  string                       `json:"instanceType,omitempty"`
	LaunchDetails *instanceConfigurationLaunch `json:"launchDetails,omitempty"`
}

// instancePoolPlacementWire is where a pool places its instances.
type instancePoolPlacementWire struct {
	AvailabilityDomain string   `json:"availabilityDomain"`
	PrimarySubnetID    string   `json:"primarySubnetId,omitempty"`
	FaultDomains       []string `json:"faultDomains,omitempty"`
}

// instancePoolRequest is the body of CreateInstancePool and UpdateInstancePool.
type instancePoolRequest struct {
	CompartmentID           string                      `json:"compartmentId,omitempty"`
	DisplayName             string                      `json:"displayName,omitempty"`
	InstanceConfigurationID string                      `json:"instanceConfigurationId,omitempty"`
	Size                    *int                        `json:"size,omitempty"`
	Placements              []instancePoolPlacementWire `json:"placementConfigurations,omitempty"`
	FreeformTags            map[string]string           `json:"freeformTags,omitempty"`
	DefinedTags             definedTags                 `json:"definedTags,omitempty"`
}

// instancePoolResponse is OCI's InstancePool.
type instancePoolResponse struct {
	ID                      string                      `json:"id"`
	CompartmentID           string                      `json:"compartmentId"`
	DisplayName             string                      `json:"displayName"`
	InstanceConfigurationID string                      `json:"instanceConfigurationId"`
	Size                    int                         `json:"size"`
	Placements              []instancePoolPlacementWire `json:"placementConfigurations"`
	LifecycleState          string                      `json:"lifecycleState"`
	TimeCreated             string                      `json:"timeCreated"`
	FreeformTags            map[string]string           `json:"freeformTags"`
	DefinedTags             definedTags                 `json:"definedTags"`
}

// instancePoolInstanceResponse is OCI's InstanceSummary for a pool member.
type instancePoolInstanceResponse struct {
	ID                 string `json:"id"`
	InstanceID         string `json:"instanceId"`
	CompartmentID      string `json:"compartmentId"`
	AvailabilityDomain string `json:"availabilityDomain"`
	DisplayName        string `json:"displayName"`
	Shape              string `json:"shape"`
	State              string `json:"state"`
	TimeCreated        string `json:"timeCreated"`
}
