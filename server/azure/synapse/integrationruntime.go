package synapse

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

func (h *Handler) serveIntRuntime(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch {
	case rp.SubResourceName == "":
		h.listIntRuntimes(w, r, rp)
	case rp.SubResourceAction != "":
		h.intRuntimeAction(w, r, rp)
	default:
		h.intRuntimeCRUD(w, r, rp)
	}
}

func (h *Handler) intRuntimeCRUD(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut, http.MethodPatch:
		h.putIntRuntime(w, r, rp)
	case http.MethodGet:
		h.getIntRuntime(w, rp)
	case http.MethodDelete:
		h.deleteIntRuntime(w, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) putIntRuntime(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var req integrationRuntimeRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	h.mu.Lock()

	ws, ok := h.getWorkspace(rp)
	if !ok {
		h.mu.Unlock()
		writeParentNotFound(w, rp.ResourceName)

		return
	}

	name := rp.SubResourceName
	k := strings.ToLower(name)

	ir, existed := ws.IntRuntimes[k]
	if !existed {
		ir = &intRuntimeState{Name: name}
	}

	ir.Props = cloneRaw(req.Properties)
	ws.IntRuntimes[k] = ir

	resource := toIntRuntimeResponse(ws, ir)
	h.mu.Unlock()

	// The armsynapse IntegrationRuntimesClient.BeginCreate poller accepts a
	// synchronous 200 (or 202), not 201.
	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) getIntRuntime(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	childGet(h, w, rp, intRuntimesOf, writeIntRuntimeNotFound,
		func(ws *workspaceState, ir *intRuntimeState) any { return toIntRuntimeResponse(ws, ir) })
}

func (h *Handler) deleteIntRuntime(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	childDelete(h, w, rp, intRuntimesOf)
}

func intRuntimesOf(ws *workspaceState) map[string]*intRuntimeState { return ws.IntRuntimes }

// intRuntimeAction serves start/stop, returning the runtime's resulting run
// state. The synchronous 200 body makes the armsynapse BeginStart/BeginStop
// poller finalize on its first poll.
func (h *Handler) intRuntimeAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	state, ok := intRuntimeActionState(rp.SubResourceAction)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "unsupported integration runtime action")
		return
	}

	h.mu.RLock()

	ws, ok := h.getWorkspace(rp)
	if !ok {
		h.mu.RUnlock()
		writeParentNotFound(w, rp.ResourceName)

		return
	}

	ir, ok := ws.IntRuntimes[strings.ToLower(rp.SubResourceName)]
	if !ok {
		h.mu.RUnlock()
		writeIntRuntimeNotFound(w, rp.SubResourceName)

		return
	}

	resp := integrationRuntimeStatusResponse{
		Name: ir.Name,
		Properties: integrationRuntimeStatusProps{
			Type:            irType(ir.Props),
			State:           state,
			DataFactoryName: ws.Name,
		},
	}
	h.mu.RUnlock()

	// Stop returns 200 with no body in the SDK's poller; start returns a status.
	if state == irStateStopped {
		w.WriteHeader(http.StatusOK)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) listIntRuntimes(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	childList(h, w, r, rp, intRuntimesOf,
		func(ws *workspaceState, ir *intRuntimeState) any { return toIntRuntimeResponse(ws, ir) })
}

// intRuntimeActionState maps a start/stop verb to the resulting run state.
func intRuntimeActionState(action string) (string, bool) {
	switch strings.ToLower(action) {
	case actionStart:
		return irStateStarted, true
	case actionStop:
		return irStateStopped, true
	default:
		return "", false
	}
}

func writeIntRuntimeNotFound(w http.ResponseWriter, name string) {
	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "integration runtime not found: "+name)
}

func toIntRuntimeResponse(ws *workspaceState, ir *intRuntimeState) integrationRuntimeResponse {
	id := azurearm.BuildResourceID(ws.Subscription, ws.ResourceGroup, providerName, typeWorkspaces, ws.Name) +
		"/" + childIntRuntime + "/" + ir.Name

	return integrationRuntimeResponse{
		ID:         id,
		Name:       ir.Name,
		Type:       armTypeIntRuntime,
		Etag:       azurearm.ETag(id),
		Properties: ir.Props,
	}
}
