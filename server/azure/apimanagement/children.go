package apimanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/azure/apimanagement"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// Child collection path segments under .../service/{name}/.
const (
	segAPIs        = "apis"
	segProducts    = "products"
	segPolicies    = "policies"
	segPortal      = "portalsettings"
	segTenant      = "tenant"
	policyName     = "policy"
	actListSecrets = "listsecrets"
)

// childStore is the child-resource half of Store.
type childStore interface {
	ListAPIs(ctx context.Context, sub, rg, svc string) ([]apimanagement.ChildResource, error)
	GetAPI(ctx context.Context, sub, rg, svc, apiID string) (apimanagement.ChildResource, error)
	DeleteAPI(ctx context.Context, sub, rg, svc, apiID, ifMatch string) (bool, error)
	ListProducts(ctx context.Context, sub, rg, svc string) ([]apimanagement.ChildResource, error)
	GetProduct(ctx context.Context, sub, rg, svc, productID string) (apimanagement.ChildResource, error)
	DeleteProduct(ctx context.Context, sub, rg, svc, productID, ifMatch string) (bool, error)

	GetPolicy(ctx context.Context, sub, rg, svc string) (apimanagement.ChildResource, error)
	PutPolicy(ctx context.Context, sub, rg, svc, value, format, ifMatch string) (apimanagement.ChildResource, bool, error)
	DeletePolicy(ctx context.Context, sub, rg, svc, ifMatch string) (bool, error)

	GetPortalSetting(ctx context.Context, sub, rg, svc, name string) (apimanagement.ChildResource, error)
	PutPortalSetting(
		ctx context.Context, sub, rg, svc, name string, props json.RawMessage, ifMatch string,
	) (apimanagement.ChildResource, error)
	DelegationValidationKey(ctx context.Context, sub, rg, svc string) (string, error)

	GetTenantAccess(ctx context.Context, sub, rg, svc, name string) (apimanagement.TenantAccess, error)
	UpdateTenantAccess(
		ctx context.Context, sub, rg, svc, name string, enabled *bool, ifMatch string,
	) (apimanagement.TenantAccess, error)
}

// childSegments returns the path segments after .../service/{name}/. ParsePath
// keeps only the first few trailing segments, so the child router reads the
// whole tail itself to tell .../apis/{id} from .../apis/{id}/operations.
func childSegments(urlPath string) []string {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")

	for i := 0; i+3 < len(parts); i++ {
		if strings.EqualFold(parts[i], "providers") && strings.EqualFold(parts[i+1], providerName) &&
			strings.EqualFold(parts[i+2], serviceType) {
			return parts[i+4:]
		}
	}

	return nil
}

// childScope is one child request: its service coordinates and its path tail.
type childScope struct {
	sub, rg, svc string
	serviceID    string
	segs         []string
}

// serveChild routes the service's child resources.
func (h *Handler) serveChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, segs []string) {
	if len(segs) == 0 {
		unsupportedChild(w, rp.SubResource)
		return
	}

	c := childScope{
		sub: rp.Subscription, rg: rp.ResourceGroup, svc: rp.ResourceName,
		serviceID: azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, serviceType, rp.ResourceName),
		segs:      segs,
	}

	switch strings.ToLower(segs[0]) {
	case segAPIs, segProducts:
		h.serveCollection(w, r, &c)
	case segPolicies:
		h.servePolicy(w, r, &c)
	case segPortal:
		h.servePortal(w, r, &c)
	case segTenant:
		h.serveTenant(w, r, &c)
	default:
		unsupportedChild(w, segs[0])
	}
}

// unsupportedChild answers a child path the emulator does not model.
func unsupportedChild(w http.ResponseWriter, what string) {
	azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType",
		"unsupported API Management sub-resource "+what)
}

// writeChild writes a child resource with its ETag header.
func writeChild(w http.ResponseWriter, r *http.Request, status int, etag string, body any) {
	w.Header().Set("ETag", `"`+etag+`"`)

	if r.Method == http.MethodHead {
		w.WriteHeader(status)
		return
	}

	azurearm.WriteJSON(w, status, body)
}

// Path-tail lengths: .../{collection}, .../{collection}/{id} and
// .../{collection}/{id}/{action}.
const (
	collectionSegs = 1
	itemSegs       = 2
	actionSegs     = 3
)

// isRead reports a GET or HEAD.
func isRead(r *http.Request) bool {
	return r.Method == http.MethodGet || r.Method == http.MethodHead
}

// collectionOps are the list/get/delete operations of one child collection.
type collectionOps struct {
	list func(ctx context.Context, sub, rg, svc string) ([]apimanagement.ChildResource, error)
	get  func(ctx context.Context, sub, rg, svc, id string) (apimanagement.ChildResource, error)
	del  func(ctx context.Context, sub, rg, svc, id, ifMatch string) (bool, error)
}

// serveCollection serves the apis and products collections: list, get and
// delete. Creating an API or a product is out of scope.
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, c *childScope) {
	seg := strings.ToLower(c.segs[0])

	ops := collectionOps{list: h.store.ListProducts, get: h.store.GetProduct, del: h.store.DeleteProduct}
	if seg == segAPIs {
		ops = collectionOps{list: h.store.ListAPIs, get: h.store.GetAPI, del: h.store.DeleteAPI}
	}

	switch len(c.segs) {
	case collectionSegs:
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}

		h.listCollection(w, r, c, seg, &ops)
	case itemSegs:
		h.serveCollectionItem(w, r, c, seg, &ops)
	default:
		unsupportedChild(w, strings.Join(c.segs, "/"))
	}
}

func (*Handler) listCollection(w http.ResponseWriter, r *http.Request, c *childScope, seg string, ops *collectionOps) {
	items, err := ops.list(r.Context(), c.sub, c.rg, c.svc)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]childResponse, 0, len(items))
	for i := range items {
		out = append(out, toChildResponse(c.serviceID, seg, &items[i]))
	}

	writeList(w, r, out, true)
}

func (*Handler) serveCollectionItem(w http.ResponseWriter, r *http.Request, c *childScope, seg string, ops *collectionOps) {
	switch {
	case isRead(r):
		item, err := ops.get(r.Context(), c.sub, c.rg, c.svc, c.segs[1])
		if err != nil {
			writeErr(w, err)
			return
		}

		writeChild(w, r, http.StatusOK, item.Etag, toChildResponse(c.serviceID, seg, &item))
	case r.Method == http.MethodDelete:
		existed, err := ops.del(r.Context(), c.sub, c.rg, c.svc, c.segs[1], r.Header.Get("If-Match"))
		if err != nil {
			writeErr(w, err)
			return
		}

		// An API delete answers 204 either way: the 2022-08-01 armapimanagement
		// client accepts only 202/204 and go-azure-sdk (azurerm) only 200/204.
		if seg == segAPIs {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		writeDeleted(w, existed)
	default:
		methodNotAllowed(w)
	}
}

// servePolicy serves the service-level policy (policies/policy) and the
// policies list.
func (h *Handler) servePolicy(w http.ResponseWriter, r *http.Request, c *childScope) {
	switch {
	case len(c.segs) == collectionSegs && r.Method == http.MethodGet:
		h.listPolicies(w, r, c)
	case len(c.segs) != itemSegs || !strings.EqualFold(c.segs[1], policyName):
		unsupportedChild(w, strings.Join(c.segs, "/"))
	case isRead(r):
		p, err := h.store.GetPolicy(r.Context(), c.sub, c.rg, c.svc)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeChild(w, r, http.StatusOK, p.Etag, toChildResponse(c.serviceID, segPolicies, &p))
	case r.Method == http.MethodPut:
		h.putPolicy(w, r, c)
	case r.Method == http.MethodDelete:
		existed, err := h.store.DeletePolicy(r.Context(), c.sub, c.rg, c.svc, r.Header.Get("If-Match"))
		if err != nil {
			writeErr(w, err)
			return
		}

		writeDeleted(w, existed)
	default:
		methodNotAllowed(w)
	}
}

// listPolicies lists the service's policies: the service policy when one is
// set.
func (h *Handler) listPolicies(w http.ResponseWriter, r *http.Request, c *childScope) {
	out := []childResponse{}

	p, err := h.store.GetPolicy(r.Context(), c.sub, c.rg, c.svc)
	if err == nil {
		out = append(out, toChildResponse(c.serviceID, segPolicies, &p))
	} else if _, gerr := h.store.GetService(r.Context(), c.sub, c.rg, c.svc); gerr != nil {
		writeErr(w, gerr)
		return
	}

	writeList(w, r, out, true)
}

func (h *Handler) putPolicy(w http.ResponseWriter, r *http.Request, c *childScope) {
	var req policyRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	p, created, err := h.store.PutPolicy(r.Context(), c.sub, c.rg, c.svc,
		req.Properties.Value, req.Properties.Format, r.Header.Get("If-Match"))
	if err != nil {
		writeErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	writeChild(w, r, status, p.Etag, toChildResponse(c.serviceID, segPolicies, &p))
}

// servePortal serves portalsettings/{signin|signup|delegation} (get, put) and
// portalsettings/delegation/listSecrets.
func (h *Handler) servePortal(w http.ResponseWriter, r *http.Request, c *childScope) {
	switch {
	case len(c.segs) == actionSegs && strings.EqualFold(c.segs[1], apimanagement.PortalDelegation) &&
		strings.EqualFold(c.segs[2], actListSecrets) && r.Method == http.MethodPost:
		h.delegationSecrets(w, r, c)
	case len(c.segs) != itemSegs:
		unsupportedChild(w, strings.Join(c.segs, "/"))
	case isRead(r):
		s, err := h.store.GetPortalSetting(r.Context(), c.sub, c.rg, c.svc, strings.ToLower(c.segs[1]))
		if err != nil {
			writeErr(w, err)
			return
		}

		writeChild(w, r, http.StatusOK, s.Etag, toChildResponse(c.serviceID, segPortal, &s))
	case r.Method == http.MethodPut:
		h.putPortal(w, r, c)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) delegationSecrets(w http.ResponseWriter, r *http.Request, c *childScope) {
	key, err := h.store.DelegationValidationKey(r.Context(), c.sub, c.rg, c.svc)
	if err != nil {
		writeErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]string{"validationKey": key})
}

func (h *Handler) putPortal(w http.ResponseWriter, r *http.Request, c *childScope) {
	var req childRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	s, err := h.store.PutPortalSetting(r.Context(), c.sub, c.rg, c.svc,
		strings.ToLower(c.segs[1]), req.Properties, r.Header.Get("If-Match"))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeChild(w, r, http.StatusOK, s.Etag, toChildResponse(c.serviceID, segPortal, &s))
}

// serveTenant serves tenant/{access|gitAccess} (get, patch) and its
// listSecrets action.
func (h *Handler) serveTenant(w http.ResponseWriter, r *http.Request, c *childScope) {
	switch {
	case len(c.segs) == actionSegs && strings.EqualFold(c.segs[2], actListSecrets) && r.Method == http.MethodPost:
		t, err := h.store.GetTenantAccess(r.Context(), c.sub, c.rg, c.svc, c.segs[1])
		if err != nil {
			writeErr(w, err)
			return
		}

		writeChild(w, r, http.StatusOK, t.Etag, tenantAccessSecrets{
			ID: t.Name, PrincipalID: t.PrincipalID, PrimaryKey: t.PrimaryKey, SecondaryKey: t.SecondaryKey, Enabled: t.Enabled,
		})
	case len(c.segs) != itemSegs:
		unsupportedChild(w, strings.Join(c.segs, "/"))
	case isRead(r):
		t, err := h.store.GetTenantAccess(r.Context(), c.sub, c.rg, c.svc, c.segs[1])
		if err != nil {
			writeErr(w, err)
			return
		}

		writeChild(w, r, http.StatusOK, t.Etag, toTenantAccessResponse(c.serviceID, &t))
	case r.Method == http.MethodPatch:
		h.patchTenant(w, r, c)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) patchTenant(w http.ResponseWriter, r *http.Request, c *childScope) {
	var req tenantAccessRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	t, err := h.store.UpdateTenantAccess(r.Context(), c.sub, c.rg, c.svc, c.segs[1],
		req.Properties.Enabled, r.Header.Get("If-Match"))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeChild(w, r, http.StatusOK, t.Etag, toTenantAccessResponse(c.serviceID, &t))
}
