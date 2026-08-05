package virtualmachines

// ARM JSON request/response shapes for Microsoft.Compute/virtualMachines.
//
// We model the minimum surface needed for SDK clients to decode responses
// and for tests to assert wire shapes. This is not a full ARM contract: many
// optional fields (extensions, plan, identity, etc.) are intentionally omitted.

// vmRequest is the inbound shape for a PUT virtualMachines/{name} request.
type vmRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Zones      []string          `json:"zones,omitempty"`
	Properties vmRequestProps    `json:"properties"`
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
}

// osDisk carries the OS-disk shape; we only model osType, a cost input.
type osDisk struct {
	OSType string `json:"osType,omitempty"`
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
}

// vmResponse is the outbound shape for a single VM. Mirrors the real ARM
// response closely enough that azure-sdk-for-go's armcompute.VirtualMachine
// JSON decoder is happy.
type vmResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Zones      []string          `json:"zones,omitempty"`
	Properties vmResponseProps   `json:"properties"`
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

// vmListResponse is the outbound shape for a list operation.
type vmListResponse struct {
	Value []vmResponse `json:"value"`
}
