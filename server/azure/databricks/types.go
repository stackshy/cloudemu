package databricks

import dbxdriver "github.com/stackshy/cloudemu/v2/services/databricks/driver"

// JSON wire shapes for the Microsoft.Databricks ARM REST API. Field names match
// what the real armdatabricks client emits and expects.

// armWorkspace is the ARM resource envelope for a Databricks workspace.
type armWorkspace struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Location   string            `json:"location,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	SKU        *armSKU           `json:"sku,omitempty"`
	Properties *workspaceProps   `json:"properties,omitempty"`
}

type armSKU struct {
	Name string `json:"name,omitempty"`
	Tier string `json:"tier,omitempty"`
}

type workspaceProps struct {
	ManagedResourceGroupID string `json:"managedResourceGroupId,omitempty"`
	ProvisioningState      string `json:"provisioningState,omitempty"`
	WorkspaceURL           string `json:"workspaceUrl,omitempty"`
	WorkspaceID            string `json:"workspaceId,omitempty"`
	CreatedDateTime        string `json:"createdDateTime,omitempty"`
}

// workspaceUpdate is the PATCH body shape (tags-only update).
type workspaceUpdate struct {
	Tags map[string]string `json:"tags"`
}

// armList is the ARM list-response envelope.
type armList struct {
	Value    []armWorkspace `json:"value"`
	NextLink string         `json:"nextLink,omitempty"`
}

// toARMWorkspace converts a portable Workspace to its ARM JSON shape.
func toARMWorkspace(ws *dbxdriver.Workspace) armWorkspace {
	out := armWorkspace{
		ID:       ws.ID,
		Name:     ws.Name,
		Type:     providerName + "/" + resourceType,
		Location: ws.Location,
		Tags:     ws.Tags,
		Properties: &workspaceProps{
			ManagedResourceGroupID: ws.ManagedResourceGroupID,
			ProvisioningState:      ws.ProvisioningState,
			WorkspaceURL:           ws.WorkspaceURL,
			WorkspaceID:            ws.WorkspaceID,
			CreatedDateTime:        ws.CreatedAt,
		},
	}
	if ws.SKUName != "" {
		out.SKU = &armSKU{Name: ws.SKUName, Tier: ws.SKUTier}
	}

	return out
}
