package virtualmachines

// ARM JSON request/response shapes for Microsoft.Compute/virtualMachines.
//
// We model the minimum surface needed for SDK clients to decode responses
// and for tests to assert wire shapes. This is not a full ARM contract: many
// optional fields (extensions, plan, etc.) are intentionally omitted.

// vmRequest is the inbound shape for a PUT virtualMachines/{name} request.
type vmRequest struct {
	Location string            `json:"location"`
	Tags     map[string]string `json:"tags,omitempty"`
	Zones    []string          `json:"zones,omitempty"`
	// Identity is the managed-identity block, a sibling of properties at the
	// resource root (armcompute.VirtualMachine.Identity). Omitted (nil) means
	// the request did not touch identity; real Azure then preserves whatever
	// identity is already attached rather than clearing it.
	Identity   *identity      `json:"identity,omitempty"`
	Properties vmRequestProps `json:"properties"`
}

// identity is the ARM managed-service-identity envelope
// (armcompute.VirtualMachineIdentity): system-assigned and/or user-assigned
// identity configuration.
type identity struct {
	// Type is one of "None", "SystemAssigned", "UserAssigned",
	// "SystemAssigned,UserAssigned".
	Type string `json:"type,omitempty"`
	// PrincipalID/TenantID are read-only: Azure populates them once a
	// system-assigned identity is attached. A request sends neither; we
	// still decode them (and ignore the values) so a client that round-trips
	// a GET response back into a PUT body doesn't fail to decode.
	PrincipalID string `json:"principalId,omitempty"`
	TenantID    string `json:"tenantId,omitempty"`
	// UserAssignedIdentities is keyed by the full ARM resource ID of a
	// Microsoft.ManagedIdentity/userAssignedIdentities resource to attach. A
	// request sends an empty object per key; principalId/clientId are
	// read-only, populated on the response.
	UserAssignedIdentities map[string]*userAssignedIdentity `json:"userAssignedIdentities,omitempty"`
}

// userAssignedIdentity is one entry of identity.userAssignedIdentities: the
// read-only principal/client id pair Azure reports for an attached
// user-assigned identity.
type userAssignedIdentity struct {
	PrincipalID string `json:"principalId,omitempty"`
	ClientID    string `json:"clientId,omitempty"`
}

type vmRequestProps struct {
	HardwareProfile *hardwareProfile `json:"hardwareProfile,omitempty"`
	StorageProfile  *storageProfile  `json:"storageProfile,omitempty"`
	NetworkProfile  *networkProfile  `json:"networkProfile,omitempty"`
	OSProfile       *osProfile       `json:"osProfile,omitempty"`
	// Priority ("Spot"/"Regular") and LicenseType (hybrid-benefit marker) are
	// cost inputs the SDK sends under properties; we carry them to the driver.
	Priority    string `json:"priority,omitempty"`
	LicenseType string `json:"licenseType,omitempty"`
}

type hardwareProfile struct {
	VMSize string `json:"vmSize,omitempty"`
}

type storageProfile struct {
	ImageReference *imageReference `json:"imageReference,omitempty"`
	OSDisk         *osDisk         `json:"osDisk,omitempty"`
	DataDisks      []dataDisk      `json:"dataDisks,omitempty"`
}

// osDisk carries the OS-disk shape; we only model osType, a cost input.
type osDisk struct {
	OSType string `json:"osType,omitempty"`
}

// dataDisk mirrors armcompute.DataDisk: one entry of storageProfile.dataDisks,
// either declaring a managed disk to attach (createOption "Attach" +
// managedDisk.id) or, on the PATCH Update path, marking an already-attached
// disk for detachment (toBeDetached).
type dataDisk struct {
	Lun          int                    `json:"lun"`
	Name         string                 `json:"name,omitempty"`
	CreateOption string                 `json:"createOption,omitempty"`
	DiskSizeGB   int                    `json:"diskSizeGB,omitempty"`
	Caching      string                 `json:"caching,omitempty"`
	ManagedDisk  *managedDiskParameters `json:"managedDisk,omitempty"`
	ToBeDetached bool                   `json:"toBeDetached,omitempty"`
	DetachOption string                 `json:"detachOption,omitempty"`
}

// managedDiskParameters references an existing Microsoft.Compute/disks
// resource a dataDisk entry attaches.
type managedDiskParameters struct {
	ID                 string `json:"id,omitempty"`
	StorageAccountType string `json:"storageAccountType,omitempty"`
}

type imageReference struct {
	ID        string `json:"id,omitempty"`
	Publisher string `json:"publisher,omitempty"`
	Offer     string `json:"offer,omitempty"`
	SKU       string `json:"sku,omitempty"`
	Version   string `json:"version,omitempty"`
}

type networkProfile struct {
	NetworkInterfaces []networkInterfaceRef `json:"networkInterfaces,omitempty"`
}

type networkInterfaceRef struct {
	ID string `json:"id,omitempty"`
}

type osProfile struct {
	ComputerName  string `json:"computerName,omitempty"`
	AdminUsername string `json:"adminUsername,omitempty"`
	// CustomData is the base64-encoded cloud-init/boot script Azure runs on
	// first boot — the customData field of the ARM osProfile. It maps to the
	// driver's InstanceConfig.UserData (base64-decoded) so a real compute engine
	// runs it as the boot script.
	CustomData string `json:"customData,omitempty"`
}

// bootDiagnosticsDataResult is the ARM response for
// POST virtualMachines/{name}/retrieveBootDiagnosticsData. It mirrors
// armcompute.RetrieveBootDiagnosticsDataResult: URIs the client downloads the
// captured console screenshot and serial log from. We point the serial-log URI
// back at this server so the captured boot output is retrievable.
type bootDiagnosticsDataResult struct {
	ConsoleScreenshotBlobURI string `json:"consoleScreenshotBlobUri,omitempty"`
	SerialConsoleLogBlobURI  string `json:"serialConsoleLogBlobUri,omitempty"`
}

// vmResponse is the outbound shape for a single VM. Mirrors the real ARM
// response closely enough that azure-sdk-for-go's armcompute.VirtualMachine
// JSON decoder is happy.
type vmResponse struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Location string            `json:"location"`
	Tags     map[string]string `json:"tags,omitempty"`
	Zones    []string          `json:"zones,omitempty"`
	// Identity echoes the managed-identity block attached to the VM, nil when
	// none is attached.
	Identity   *identity       `json:"identity,omitempty"`
	Properties vmResponseProps `json:"properties"`
}

type vmResponseProps struct {
	VMID              string           `json:"vmId"`
	ProvisioningState string           `json:"provisioningState"`
	HardwareProfile   *hardwareProfile `json:"hardwareProfile,omitempty"`
	StorageProfile    *storageProfile  `json:"storageProfile,omitempty"`
	NetworkProfile    *networkProfile  `json:"networkProfile,omitempty"`
	OSProfile         *osProfile       `json:"osProfile,omitempty"`
	Priority          string           `json:"priority,omitempty"`
	LicenseType       string           `json:"licenseType,omitempty"`
	InstanceView      *instanceView    `json:"instanceView,omitempty"`
}

type instanceView struct {
	Statuses []instanceViewStatus `json:"statuses"`
}

type instanceViewStatus struct {
	Code          string `json:"code"`
	Level         string `json:"level"`
	DisplayStatus string `json:"displayStatus"`
}

// instanceViewResponse is the ARM VirtualMachineInstanceView returned by
// GET virtualMachines/{name}/instanceView.
type instanceViewResponse struct {
	ComputerName     string               `json:"computerName,omitempty"`
	OSName           string               `json:"osName,omitempty"`
	HyperVGeneration string               `json:"hyperVGeneration,omitempty"`
	VMAgent          *vmAgentInstanceView `json:"vmAgent,omitempty"`
	Disks            []diskInstanceView   `json:"disks,omitempty"`
	Statuses         []instanceViewStatus `json:"statuses"`
}

type vmAgentInstanceView struct {
	VMAgentVersion string               `json:"vmAgentVersion,omitempty"`
	Statuses       []instanceViewStatus `json:"statuses,omitempty"`
}

type diskInstanceView struct {
	Name     string               `json:"name,omitempty"`
	Statuses []instanceViewStatus `json:"statuses,omitempty"`
}

// vmListResponse is the outbound shape for a list operation.
type vmListResponse struct {
	Value []vmResponse `json:"value"`
}

// captureRequest is the body for POST virtualMachines/{name}/capture
// (armcompute.VirtualMachineCaptureParameters).
type captureRequest struct {
	VhdPrefix                string `json:"vhdPrefix"`
	DestinationContainerName string `json:"destinationContainerName"`
	OverwriteVhds            bool   `json:"overwriteVhds"`
}

// captureResponse is the VirtualMachineCaptureResult: an ARM deployment-template
// envelope whose resources recreate similar VMs from the captured disks.
type captureResponse struct {
	Schema         string            `json:"$schema"`
	ContentVersion string            `json:"contentVersion"`
	Parameters     map[string]any    `json:"parameters"`
	Resources      []captureResource `json:"resources"`
	ID             string            `json:"id"`
}

type captureResource struct {
	Type       string               `json:"type"`
	APIVersion string               `json:"apiVersion"`
	Properties captureResourceProps `json:"properties"`
}

type captureResourceProps struct {
	StorageProfile captureStorageProfile `json:"storageProfile"`
}

type captureStorageProfile struct {
	OSDisk captureOSDisk `json:"osDisk"`
}

type captureOSDisk struct {
	OSType string      `json:"osType"`
	Name   string      `json:"name"`
	Image  *captureVHD `json:"image,omitempty"`
}

type captureVHD struct {
	URI string `json:"uri"`
}
