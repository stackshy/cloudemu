package streamanalytics

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/azure/streamanalytics"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// serveChild routes the nested transformation/input/output/function surface,
// including the per-child test and RetrieveDefaultDefinition POST actions. The
// kind is normalized to its canonical lower-case segment so the stored resource
// type is stable regardless of the URL's casing.
func (h *Handler) serveChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	kind := strings.ToLower(rp.SubResource)

	if rp.SubResourceAction != "" {
		h.serveChildAction(w, r, rp, kind)
		return
	}

	if rp.SubResourceName == "" {
		h.listChildren(w, r, rp, kind)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createChild(w, r, rp, kind)
	case http.MethodPatch:
		h.updateChild(w, r, rp, kind)
	case http.MethodGet:
		h.getChild(w, r, rp, kind)
	case http.MethodDelete:
		h.deleteChild(w, r, rp, kind)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// serveChildAction routes the POST child actions: test (all kinds) and
// RetrieveDefaultDefinition (functions only).
func (h *Handler) serveChildAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, kind string) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	switch strings.ToLower(rp.SubResourceAction) {
	case actionTest:
		h.testChild(w, r, rp, kind)
	case actionRetrieveDefaults:
		h.retrieveDefaultDefinition(w, r, rp, kind)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType", "unknown action "+rp.SubResourceAction)
	}
}

func (h *Handler) createChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, kind string) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req childBody
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	c, created, err := h.store.CreateOrUpdateChild(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, kind, rp.SubResourceName, req.Properties)
	if err != nil {
		writeChildErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toChildResponse(&c))
}

// updateChild applies an ARM PATCH to a child, preserving everything the body
// did not name. A PATCH on a missing child is a 404.
func (h *Handler) updateChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, kind string) {
	if _, err := h.store.GetChild(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, kind, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req childBody
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	c, _, err := h.store.CreateOrUpdateChild(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, kind, rp.SubResourceName, req.Properties)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toChildResponse(&c))
}

func (h *Handler) getChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, kind string) {
	c, err := h.store.GetChild(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, kind, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toChildResponse(&c))
}

func (h *Handler) deleteChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, kind string) {
	existed, err := h.store.DeleteChild(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, kind, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeDeleteStatus(w, existed)
}

func (h *Handler) listChildren(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, kind string) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	items, err := h.store.ListChildren(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, kind)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := childListResponse{Value: make([]childResponse, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, toChildResponse(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) testChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, kind string) {
	status, err := h.store.TestChild(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, kind, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, testStatusResponse{Status: status})
}

// retrieveDefaultDefinition returns the stored function's current definition.
// Real Azure infers a default binding/signature from the request; the emulator
// echoes the persisted function, which is a NotFound when the function is
// absent. Only the functions collection supports this action.
func (h *Handler) retrieveDefaultDefinition(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, kind string,
) {
	if kind != streamanalytics.KindFunctions {
		azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType",
			"retrieveDefaultDefinition is only valid on functions")
		return
	}

	c, err := h.store.GetChild(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, kind, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toChildResponse(&c))
}
