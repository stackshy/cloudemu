// Package azuresearch implements the Azure AI Search REST APIs as
// server.Handlers: the Microsoft.Search ARM control plane (service lifecycle,
// admin/query keys, private links) and the {service}.search.windows.net data
// plane (indexes, documents, indexers, data sources, skillsets, synonym maps,
// aliases, service statistics).
//
// Real armsearch clients (and the azsearch data-plane client) configured with a
// custom endpoint hit these handlers the same way they hit management.azure.com
// and {service}.search.windows.net.
package azuresearch

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	srchdriver "github.com/stackshy/cloudemu/v2/services/azuresearch/driver"
)

const searchProvider = "Microsoft.Search"

// Account sub-resource collections under a search service.
const (
	subAdminKeys   = "listAdminKeys"
	subRegenerate  = "regenerateAdminKey"
	subListQuery   = "listQueryKeys"
	subCreateQuery = "createQueryKey"
	subDeleteQuery = "deleteQueryKey"
	collSharedLink = "sharedPrivateLinkResources"
	collPEC        = "privateEndpointConnections"
)

// ControlHandler serves Microsoft.Search/searchServices ARM requests.
type ControlHandler struct {
	svc srchdriver.SearchControl
}

// NewControl returns a control-plane handler backed by svc.
func NewControl(svc srchdriver.SearchControl) *ControlHandler {
	return &ControlHandler{svc: svc}
}

// Matches claims Microsoft.Search/searchServices ARM paths.
func (*ControlHandler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == searchProvider && rp.ResourceType == "searchServices"
}

// ServeHTTP routes by path shape and method.
func (h *ControlHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")

		return
	}

	switch {
	case rp.ResourceName == "":
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)

			return
		}

		h.listServices(w, r, &rp)
	case rp.SubResource == "":
		h.serveService(w, r, &rp)
	case rp.SubResource == collSharedLink:
		h.serveSharedLink(w, r, &rp)
	case rp.SubResource == collPEC:
		h.servePEC(w, r, &rp)
	default:
		h.serveServiceAction(w, r, &rp)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}

func serviceJSON(s *srchdriver.Service) map[string]any {
	return map[string]any{
		"id": s.ID, "name": s.Name, "type": searchProvider + "/searchServices",
		"location": s.Location, "tags": s.Tags,
		"sku": map[string]any{"name": s.SKUName},
		"properties": map[string]any{
			"replicaCount": s.ReplicaCount, "partitionCount": s.PartitionCount,
			"hostingMode": s.HostingMode, "status": s.Status,
			"provisioningState": s.ProvisioningState,
			"endpoint":          s.Endpoint,
		},
	}
}

func (h *ControlHandler) serveService(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createService(w, r, rp)
	case http.MethodGet:
		h.getService(w, r, rp)
	case http.MethodPatch:
		h.patchService(w, r, rp)
	case http.MethodDelete:
		h.deleteService(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *ControlHandler) createService(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body struct {
		Location string `json:"location"`
		SKU      *struct {
			Name string `json:"name"`
		} `json:"sku"`
		Tags       map[string]string `json:"tags"`
		Properties *struct {
			ReplicaCount   int    `json:"replicaCount"`
			PartitionCount int    `json:"partitionCount"`
			HostingMode    string `json:"hostingMode"`
		} `json:"properties"`
	}

	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := srchdriver.ServiceConfig{Name: rp.ResourceName, ResourceGroup: rp.ResourceGroup, Location: body.Location, Tags: body.Tags}
	if body.SKU != nil {
		cfg.SKUName = body.SKU.Name
	}

	if body.Properties != nil {
		cfg.ReplicaCount = body.Properties.ReplicaCount
		cfg.PartitionCount = body.Properties.PartitionCount
		cfg.HostingMode = body.Properties.HostingMode
	}

	s, err := h.svc.CreateService(r.Context(), cfg)
	writeRes(w, serviceJSON, s, err)
}

func (h *ControlHandler) getService(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	s, err := h.svc.GetService(r.Context(), rp.ResourceGroup, rp.ResourceName)
	writeRes(w, serviceJSON, s, err)
}

func (h *ControlHandler) patchService(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body struct {
		Tags       map[string]string `json:"tags"`
		Properties *struct {
			ReplicaCount   int `json:"replicaCount"`
			PartitionCount int `json:"partitionCount"`
		} `json:"properties"`
	}

	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	var replicas, partitions int
	if body.Properties != nil {
		replicas = body.Properties.ReplicaCount
		partitions = body.Properties.PartitionCount
	}

	s, err := h.svc.UpdateService(r.Context(), rp.ResourceGroup, rp.ResourceName, replicas, partitions, body.Tags)
	writeRes(w, serviceJSON, s, err)
}

func (h *ControlHandler) deleteService(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.svc.DeleteService(r.Context(), rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (h *ControlHandler) listServices(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var (
		svcs []srchdriver.Service
		err  error
	)

	if rp.ResourceGroup == "" {
		svcs, err = h.svc.ListServices(r.Context())
	} else {
		svcs, err = h.svc.ListServicesByResourceGroup(r.Context(), rp.ResourceGroup)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(svcs))
	for i := range svcs {
		out = append(out, serviceJSON(&svcs[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})
}

// writeRes writes a created/fetched resource or maps the error.
func writeRes[T any](w http.ResponseWriter, toJSON func(*T) map[string]any, res *T, err error) {
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toJSON(res))
}
