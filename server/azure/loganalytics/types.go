package loganalytics

import (
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// provisioningSucceeded is the terminal provisioning state. The azcore body
// poller treats it as done, so CreateOrUpdate/Delete complete on the first
// response without a follow-up poll.
const provisioningSucceeded = "Succeeded"

// defaultSKUName is the SKU real Log Analytics defaults a workspace to when the
// caller omits one (the current commitment-tier-less pricing SKU).
const defaultSKUName = "PerGB2018"

// workspaceSKU is the ARM Workspace SKU sub-object (properties.sku).
type workspaceSKU struct {
	Name string `json:"name"`
}

// workspaceProperties is the subset of Log Analytics workspace properties we
// model. RetentionInDays round-trips through the driver; provisioningState,
// customerId and sku are Azure-only ARM fields tracked in the wire handler's
// per-workspace metadata.
type workspaceProperties struct {
	ProvisioningState string        `json:"provisioningState,omitempty"`
	RetentionInDays   *int32        `json:"retentionInDays,omitempty"`
	CustomerID        string        `json:"customerId,omitempty"`
	SKU               *workspaceSKU `json:"sku,omitempty"`
	CreatedDate       string        `json:"createdDate,omitempty"`
}

// workspaceJSON is the ARM Workspace resource envelope.
type workspaceJSON struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	Type       string              `json:"type"`
	Location   string              `json:"location,omitempty"`
	Tags       map[string]string   `json:"tags,omitempty"`
	Properties workspaceProperties `json:"properties"`
}

// workspaceListResult is the paged list envelope (SDK reads `.value`).
type workspaceListResult struct {
	Value []workspaceJSON `json:"value"`
}

// workspaceRequest is the inbound CreateOrUpdate body.
type workspaceRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags"`
	Properties *struct {
		RetentionInDays *int32        `json:"retentionInDays"`
		SKU             *workspaceSKU `json:"sku"`
	} `json:"properties"`
}

// retentionDays returns the requested retention, or 0 when unset (the driver
// then applies its default).
func (req *workspaceRequest) retentionDays() int {
	if req.Properties == nil || req.Properties.RetentionInDays == nil {
		return 0
	}

	return int(*req.Properties.RetentionInDays)
}

// skuName returns the requested SKU name, or "" when unset.
func (req *workspaceRequest) skuName() string {
	if req.Properties == nil || req.Properties.SKU == nil {
		return ""
	}

	return req.Properties.SKU.Name
}

// toWorkspaceJSON renders a driver log group as an ARM workspace, folding in the
// Azure-only ARM fields (location, customerId GUID, sku) held in meta.
func toWorkspaceJSON(info *logdriver.LogGroupInfo, meta *workspaceMeta) workspaceJSON {
	retention := int32(info.RetentionDays) //nolint:gosec // retention days is a small positive value

	return workspaceJSON{
		ID:       info.ResourceID,
		Name:     info.Name,
		Type:     providerName + "/" + typeWorkspaces,
		Location: meta.Location,
		Tags:     info.Tags,
		Properties: workspaceProperties{
			ProvisioningState: provisioningSucceeded,
			RetentionInDays:   &retention,
			CustomerID:        meta.CustomerID,
			SKU:               &workspaceSKU{Name: meta.SKU},
			CreatedDate:       info.CreatedAt,
		},
	}
}
