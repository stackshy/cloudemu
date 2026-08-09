package sesv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// serveTenants routes /tenants and its POST-based sub-paths.
func (h *Handler) serveTenants(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createTenant(w, r)
		case http.MethodGet:
			h.listTenants(w, r)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	h.serveTenantAction(w, r, rest)
}

func (h *Handler) serveTenantAction(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == twoSegments && rest[0] == rootResources {
		h.serveTenantResourceAction(w, r, rest[1])

		return
	}

	if len(rest) != 1 {
		notFound(w, r.URL.Path)

		return
	}

	switch rest[0] {
	case segList:
		h.listTenants(w, r)
	case "get":
		h.getTenant(w, r)
	case segDelete:
		h.deleteTenant(w, r)
	case rootResources:
		h.createTenantAssociation(w, r)
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) serveTenantResourceAction(w http.ResponseWriter, r *http.Request, action string) {
	switch action {
	case segDelete:
		h.deleteTenantAssociation(w, r)
	case segList:
		h.listTenantResources(w, r)
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) createTenant(w http.ResponseWriter, r *http.Request) {
	var req createTenantRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	t, err := h.ses.CreateTenant(r.Context(), req.TenantName, tagsToMap(req.Tags))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, createTenantResponse{TenantName: t.Name, TenantID: t.ID, TenantArn: t.ARN})
}

func (h *Handler) getTenant(w http.ResponseWriter, r *http.Request) {
	var req tenantNameRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	t, err := h.ses.GetTenant(r.Context(), req.TenantName)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, getTenantResponse{Tenant: tenantToJSON(t)})
}

func (h *Handler) deleteTenant(w http.ResponseWriter, r *http.Request) {
	var req tenantNameRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.DeleteTenant(r.Context(), req.TenantName))
}

func (h *Handler) listTenants(w http.ResponseWriter, r *http.Request) {
	tenants, err := h.ses.ListTenants(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]tenantInfoJSON, 0, len(tenants))
	for i := range tenants {
		out = append(out, tenantInfoJSON{TenantName: tenants[i].Name, TenantID: tenants[i].ID, TenantArn: tenants[i].ARN})
	}

	writeJSON(w, listTenantsResponse{Tenants: out})
}

func (h *Handler) createTenantAssociation(w http.ResponseWriter, r *http.Request) {
	var req tenantResourceRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.CreateTenantResourceAssociation(r.Context(), req.TenantName, req.ResourceArn))
}

func (h *Handler) deleteTenantAssociation(w http.ResponseWriter, r *http.Request) {
	var req tenantResourceRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.DeleteTenantResourceAssociation(r.Context(), req.TenantName, req.ResourceArn))
}

func (h *Handler) listTenantResources(w http.ResponseWriter, r *http.Request) {
	var req tenantNameRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	resources, err := h.ses.ListTenantResources(r.Context(), req.TenantName)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]tenantResourceJSON, 0, len(resources))
	for i := range resources {
		out = append(out, tenantResourceJSON{ResourceArn: resources[i].ResourceARN})
	}

	writeJSON(w, listTenantResourcesResponse{TenantResources: out})
}

// serveTenant routes /tenant/suppression (PutTenantSuppressionAttributes).
func (h *Handler) serveTenant(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 1 || rest[0] != rootSuppression || r.Method != http.MethodPost {
		notFound(w, r.URL.Path)

		return
	}

	var req tenantSuppressionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.PutTenantSuppressionAttributes(r.Context(), req.TenantName, req.SuppressedReasons))
}

// serveResources routes /resources/tenants/list (ListResourceTenants).
func (h *Handler) serveResources(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != twoSegments || rest[0] != rootTenants || rest[1] != segList || r.Method != http.MethodPost {
		notFound(w, r.URL.Path)

		return
	}

	var req resourceTenantsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	tenants, err := h.ses.ListResourceTenants(r.Context(), req.ResourceArn)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]tenantInfoJSON, 0, len(tenants))
	for i := range tenants {
		out = append(out, tenantInfoJSON{TenantName: tenants[i].Name, TenantID: tenants[i].ID})
	}

	writeJSON(w, listResourceTenantsResponse{ResourceTenants: out})
}

func tenantToJSON(t *driver.Tenant) tenantInfoJSON {
	return tenantInfoJSON{TenantName: t.Name, TenantID: t.ID, TenantArn: t.ARN}
}
