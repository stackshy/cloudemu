package loganalytics

import (
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// provisioningSucceeded is the terminal provisioning state. The azcore body
// poller treats it as done, so CreateOrUpdate/Delete complete on the first
// response without a follow-up poll.
const provisioningSucceeded = "Succeeded"

// workspaceProperties is the subset of Log Analytics workspace properties we
// model. RetentionInDays round-trips through the driver; provisioningState is
// synthesized so the SDK's LRO poller sees a terminal state.
type workspaceProperties struct {
	ProvisioningState string `json:"provisioningState,omitempty"`
	RetentionInDays   *int32 `json:"retentionInDays,omitempty"`
	CustomerID        string `json:"customerId,omitempty"`
	CreatedDate       string `json:"createdDate,omitempty"`
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
		RetentionInDays *int32 `json:"retentionInDays"`
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

// toWorkspaceJSON renders a driver log group as an ARM workspace. location is
// echoed from the request on create; on read paths it is empty (the driver does
// not persist location), which the SDK tolerates.
func toWorkspaceJSON(rp *azurearm.ResourcePath, info *logdriver.LogGroupInfo, location string) workspaceJSON {
	retention := int32(info.RetentionDays) //nolint:gosec // retention days is a small positive value

	return workspaceJSON{
		ID:       info.ResourceID,
		Name:     info.Name,
		Type:     providerName + "/" + typeWorkspaces,
		Location: location,
		Tags:     info.Tags,
		Properties: workspaceProperties{
			ProvisioningState: provisioningSucceeded,
			RetentionInDays:   &retention,
			CustomerID:        info.ResourceID,
			CreatedDate:       info.CreatedAt,
		},
	}
}
