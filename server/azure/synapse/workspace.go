package synapse

import (
	"maps"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

func (h *Handler) serveWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.putWorkspace(w, r, rp)
	case http.MethodPatch:
		h.patchWorkspace(w, r, rp)
	case http.MethodGet:
		h.getWorkspaceResource(w, rp)
	case http.MethodDelete:
		h.deleteWorkspace(w, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// putWorkspace serves the create/replace LRO. The synchronous 201/200 body
// carries provisioningState=Succeeded and no async header, so the armsynapse
// BeginCreateOrUpdate poller finalizes on its first poll.
func (h *Handler) putWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req workspaceRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	h.mu.Lock()

	key := wsKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	ws, existed := h.workspaces.Get(key)
	if !existed {
		ws = newWorkspaceState(rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	}

	ws.Location = req.Location
	ws.Tags = maps.Clone(req.Tags)
	ws.Identity = cloneRaw(req.Identity)
	ws.Props = req.Properties
	h.workspaces.Set(key, ws)

	resource := toWorkspaceResponse(ws)
	h.mu.Unlock()

	azurearm.WriteJSON(w, createStatus(!existed), resource)
}

// patchWorkspace serves the update LRO. Only the mutable fields a patch carries
// are applied; the rest of the workspace is preserved.
func (h *Handler) patchWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var req workspaceRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	h.mu.Lock()

	ws, ok := h.getWorkspace(rp)
	if !ok {
		h.mu.Unlock()
		writeWorkspaceNotFound(w, rp.ResourceName)

		return
	}

	if req.Location != "" {
		ws.Location = req.Location
	}

	if req.Tags != nil {
		ws.Tags = maps.Clone(req.Tags)
	}

	if len(req.Identity) > 0 {
		ws.Identity = cloneRaw(req.Identity)
	}

	mergeWorkspaceProps(&ws.Props, &req.Properties)

	resource := toWorkspaceResponse(ws)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) getWorkspaceResource(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	h.mu.RLock()

	ws, ok := h.getWorkspace(rp)
	if !ok {
		h.mu.RUnlock()
		writeWorkspaceNotFound(w, rp.ResourceName)

		return
	}

	resource := toWorkspaceResponse(ws)
	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

// deleteWorkspace serves the delete LRO. Deleting the workspace state implicitly
// cascades to its SQL pools, Spark pools and integration runtimes.
func (h *Handler) deleteWorkspace(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	h.mu.Lock()

	existed := h.workspaces.Delete(wsKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName))
	h.mu.Unlock()

	deleteStatus(w, existed)
}

// listWorkspaces serves both the by-resource-group and by-subscription pagers,
// distinguished by whether the path carries a resource group.
func (h *Handler) listWorkspaces(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	h.mu.RLock()

	out := listEnvelope{Value: make([]any, 0)}

	for _, ws := range h.workspaces.SortedValues() {
		if !strings.EqualFold(ws.Subscription, rp.Subscription) {
			continue
		}

		if rp.ResourceGroup != "" && !strings.EqualFold(ws.ResourceGroup, rp.ResourceGroup) {
			continue
		}

		out.Value = append(out.Value, toWorkspaceResponse(ws))
	}

	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, out)
}

func writeWorkspaceNotFound(w http.ResponseWriter, name string) {
	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "workspace not found: "+name)
}

// mergeWorkspaceProps applies the non-empty fields of a patch onto stored props.
func mergeWorkspaceProps(dst, src *workspaceReqProps) {
	if src.DefaultDataLakeStorage != nil {
		dst.DefaultDataLakeStorage = src.DefaultDataLakeStorage
	}

	if src.ManagedResourceGroupName != "" {
		dst.ManagedResourceGroupName = src.ManagedResourceGroupName
	}

	if src.ManagedVirtualNetwork != "" {
		dst.ManagedVirtualNetwork = src.ManagedVirtualNetwork
	}

	if src.PublicNetworkAccess != "" {
		dst.PublicNetworkAccess = src.PublicNetworkAccess
	}
}

func toWorkspaceResponse(ws *workspaceState) workspaceResponse {
	return workspaceResponse{
		ID:       azurearm.BuildResourceID(ws.Subscription, ws.ResourceGroup, providerName, typeWorkspaces, ws.Name),
		Name:     ws.Name,
		Type:     armTypeWorkspace,
		Location: ws.Location,
		Tags:     ws.Tags,
		Identity: ws.Identity,
		Properties: workspaceRespProps{
			ProvisioningState:        provisioningSucceeded,
			DefaultDataLakeStorage:   ws.Props.DefaultDataLakeStorage,
			SQLAdministratorLogin:    ws.Props.SQLAdministratorLogin,
			ManagedResourceGroupName: ws.Props.ManagedResourceGroupName,
			ManagedVirtualNetwork:    ws.Props.ManagedVirtualNetwork,
			PublicNetworkAccess:      ws.Props.PublicNetworkAccess,
			ConnectivityEndpoints:    ws.connectivityEndpoints(),
			WorkspaceUID:             ws.WorkspaceUID,
		},
	}
}

// cloneRaw returns an independent copy of a raw JSON message, or nil when empty.
func cloneRaw(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}

	return append([]byte(nil), in...)
}
