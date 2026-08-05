package virtualmachines

// ARM JSON request/response shapes for Microsoft.Compute/virtualMachineScaleSets.
// Only the cost-relevant surface (SKU + per-VM profile) is modeled.

// vmssRequest is the inbound shape for PUT virtualMachineScaleSets/{name}.
type vmssRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	SKU        *vmssSKU          `json:"sku,omitempty"`
	Properties vmssRequestProps  `json:"properties"`
}

// vmssSKU is the scale-set SKU: VM size, tier, and instance count.
type vmssSKU struct {
	Name     string `json:"name,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Capacity int    `json:"capacity,omitempty"`
}

type vmssRequestProps struct {
	VirtualMachineProfile *vmssVMProfile `json:"virtualMachineProfile,omitempty"`
}

// vmssVMProfile is the per-VM template a scale set stamps out. We decode the
// cost inputs only (Spot priority, hybrid-benefit license, OS type).
type vmssVMProfile struct {
	Priority       string          `json:"priority,omitempty"`
	LicenseType    string          `json:"licenseType,omitempty"`
	StorageProfile *storageProfile `json:"storageProfile,omitempty"`
}

// vmssResponse is the outbound shape for a single scale set.
type vmssResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	SKU        *vmssSKU          `json:"sku,omitempty"`
	Properties vmssResponseProps `json:"properties"`
}

type vmssResponseProps struct {
	ProvisioningState     string         `json:"provisioningState"`
	VirtualMachineProfile *vmssVMProfile `json:"virtualMachineProfile,omitempty"`
}

// vmssListResponse is the outbound shape for a scale-set list.
type vmssListResponse struct {
	Value []vmssResponse `json:"value"`
}
