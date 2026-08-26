package databricks

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	dbxdriver "github.com/stackshy/cloudemu/v2/services/databricks/driver"
)

func (h *Handler) createOrUpdateWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armWorkspace
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := dbxdriver.WorkspaceConfig{
		Name:          rp.ResourceName,
		Subscription:  rp.Subscription,
		ResourceGroup: rp.ResourceGroup,
		Location:      body.Location,
		Tags:          body.Tags,
	}

	if body.SKU != nil {
		cfg.SKUName = body.SKU.Name
		cfg.SKUTier = body.SKU.Tier
	}

	if body.Properties != nil {
		cfg.ManagedResourceGroupID = body.Properties.ManagedResourceGroupID
		cfg.WorkspaceExtendedProperties = dbxdriver.WorkspaceExtendedProperties{
			Parameters:             body.Properties.Parameters,
			PublicNetworkAccess:    body.Properties.PublicNetworkAccess,
			RequiredNsgRules:       body.Properties.RequiredNsgRules,
			Authorizations:         body.Properties.Authorizations,
			UIDefinitionURI:        body.Properties.UIDefinitionURI,
			ManagedDiskIdentity:    body.Properties.ManagedDiskIdentity,
			StorageAccountIdentity: body.Properties.StorageAccountIdentity,
			DiskEncryptionSetID:    body.Properties.DiskEncryptionSetID,
		}
	}

	ws, err := h.dbx.CreateWorkspace(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMWorkspace(ws))
}

func (h *Handler) getWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	ws, err := h.dbx.GetWorkspace(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMWorkspace(ws))
}

func (h *Handler) updateWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body workspaceUpdate
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	ws, err := h.dbx.UpdateWorkspaceTags(r.Context(), rp.ResourceGroup, rp.ResourceName, body.Tags)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMWorkspace(ws))
}

func (h *Handler) deleteWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	// ARM DELETE is idempotent: a missing resource is the caller's desired end
	// state, so a NotFound from the driver still returns 204 (teardown retries
	// and delete-then-delete must not fail on the second pass).
	err := h.dbx.DeleteWorkspace(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil && !cerrors.IsNotFound(err) {
		azurearm.WriteCErr(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listWorkspaces(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	workspaces, err := h.listFor(r, rp)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]armWorkspace, 0, len(workspaces))
	for i := range workspaces {
		out = append(out, toARMWorkspace(&workspaces[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList{Value: out})
}

// listFor selects the resource-group-scoped or subscription-scoped listing
// based on whether the URL carried a resource group.
func (h *Handler) listFor(r *http.Request, rp *azurearm.ResourcePath) ([]dbxdriver.Workspace, error) {
	if rp.ResourceGroup != "" {
		return h.dbx.ListWorkspacesByResourceGroup(r.Context(), rp.ResourceGroup)
	}

	return h.dbx.ListWorkspaces(r.Context())
}
