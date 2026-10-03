package virtualmachines

import (
	"maps"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

const (
	subExtensions     = "extensions"
	extensionTypeName = providerName + "/" + resourceType + "/" + subExtensions
	// extensionMaxDepth is the deepest extension route: {vm}/extensions/{name}.
	extensionMaxDepth = 3
)

type extensionRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags"`
	Properties map[string]any    `json:"properties"`
}

type extensionListResponse struct {
	Value []extensionResponse `json:"value"`
}

type extensionResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties map[string]any    `json:"properties"`
}

// serveExtensions handles .../virtualMachines/{vm}/extensions[/{name}]
// (VirtualMachineExtensions CreateOrUpdate/Update/Get/List/Delete).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveExtensions(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if azurearm.TooDeep(w, r, &rp, extensionMaxDepth) {
		return
	}

	store, ok := h.compute.(computedriver.AzureVMExtensions)
	if !ok {
		azurearm.WriteChildNotImplemented(w, &rp)
		return
	}

	inst, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if rp.SubResourceName == "" {
		listExtensions(w, r, rp, store, inst.ID)
		return
	}

	switch r.Method {
	case http.MethodPut, http.MethodPatch:
		putExtension(w, r, rp, store, inst.ID, inst.Region)
	case http.MethodGet:
		ext, gerr := store.GetVMExtension(r.Context(), inst.ID, rp.SubResourceName)
		if gerr != nil {
			azurearm.WriteCErr(w, gerr)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, toExtensionResponse(rp, ext))
	case http.MethodDelete:
		// Deleting an absent extension is idempotent in ARM: 204.
		status := http.StatusOK
		if store.DeleteVMExtension(r.Context(), inst.ID, rp.SubResourceName) != nil {
			status = http.StatusNoContent
		}

		w.WriteHeader(status)
	default:
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
	}
}

//nolint:gocritic // rp is a request-scoped value
func listExtensions(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	store computedriver.AzureVMExtensions, instanceID string,
) {
	if r.Method != http.MethodGet {
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
		return
	}

	exts, err := store.ListVMExtensions(r.Context(), instanceID)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]extensionResponse, 0, len(exts))
	for i := range exts {
		out = append(out, toExtensionResponse(rp, &exts[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, extensionListResponse{Value: out})
}

// putExtension serves PUT (full replace) and PATCH (merge onto the stored
// extension) for one extension.
//
//nolint:gocritic // rp is a request-scoped value
func putExtension(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	store computedriver.AzureVMExtensions, instanceID, vmLocation string,
) {
	var req extensionRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	ext := computedriver.AzureVMExtension{
		Name:       rp.SubResourceName,
		Location:   defaultIfEmpty(req.Location, defaultIfEmpty(vmLocation, defaultVMSSLocation)),
		Tags:       req.Tags,
		Properties: req.Properties,
	}

	if r.Method == http.MethodPatch {
		cur, err := store.GetVMExtension(r.Context(), instanceID, rp.SubResourceName)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		ext.Location = cur.Location
		if req.Tags == nil {
			ext.Tags = cur.Tags
		}

		merged := cur.Properties
		if merged == nil {
			merged = map[string]any{}
		}

		maps.Copy(merged, req.Properties)
		ext.Properties = merged
	}

	stored, created, err := store.PutVMExtension(r.Context(), instanceID, ext)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toExtensionResponse(rp, stored))
}

//nolint:gocritic // rp is a request-scoped value
func toExtensionResponse(rp azurearm.ResourcePath, ext *computedriver.AzureVMExtension) extensionResponse {
	props := maps.Clone(ext.Properties)
	if props == nil {
		props = map[string]any{}
	}

	props["provisioningState"] = provisioningSucceeded

	vmID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, rp.ResourceName)

	return extensionResponse{
		ID:         vmID + "/" + subExtensions + "/" + ext.Name,
		Name:       ext.Name,
		Type:       extensionTypeName,
		Location:   ext.Location,
		Tags:       ext.Tags,
		Properties: props,
	}
}
