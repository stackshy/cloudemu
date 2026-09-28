// Package apimanagement serves the Azure API Management ARM API
// (Microsoft.ApiManagement). Real armapimanagement clients, the azurerm
// Terraform provider's go-azure-sdk clients and the az CLI hit this handler the
// same way they hit management.azure.com.
//
// Real Azure runs service create, update and delete as long-running operations
// (a Developer-tier create takes 30-45 minutes). The emulator completes them
// synchronously: PUT answers 201/200 and PATCH 200 with a body whose
// properties.provisioningState is already "Succeeded" and no
// Azure-AsyncOperation / Location header, which azcore's Body poller treats as
// terminal on the initial response; DELETE answers 200/204 with no polling
// header, which azcore treats as a completed no-op poll. So PollUntilDone
// returns on the first call.
//
// Served: the service CRUD surface, the soft-deleted services
// (locations/{l}/deletedservices/{name} get and purge, deletedservices list),
// checkNameAvailability, and the child resources around a service's create,
// refresh and destroy (apis and products list/get/delete, policies/policy,
// portalsettings signin/signup/delegation, tenant access). The behavior lives
// in the provider; this package only maps the wire.
package apimanagement

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/apimanagement"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName   = "Microsoft.ApiManagement"
	serviceType    = "service"
	serviceArmType = providerName + "/" + serviceType

	typeLocations       = "locations"
	typeDeletedServices = "deletedservices"
	typeCheckName       = "checkNameAvailability"
)

// Store is the API Management backend the handler needs. *apimanagement.Mock
// satisfies it.
type Store interface {
	CreateOrUpdateService(
		ctx context.Context, sub, rg, name, location string, in *apimanagement.ServiceInput,
	) (apimanagement.Service, bool, error)
	UpdateService(ctx context.Context, sub, rg, name string, in *apimanagement.ServiceInput) (apimanagement.Service, error)
	GetService(ctx context.Context, sub, rg, name string) (apimanagement.Service, error)
	DeleteServiceIfMatch(ctx context.Context, sub, rg, name, ifMatch string) (bool, error)
	ListServicesByResourceGroup(ctx context.Context, sub, rg string) ([]apimanagement.Service, error)
	ListServicesBySubscription(ctx context.Context, sub string) ([]apimanagement.Service, error)
	PurgeResourceGroup(ctx context.Context, sub, rg string) error
	CheckNameAvailability(ctx context.Context, name string) apimanagement.NameAvailability

	GetDeletedService(ctx context.Context, sub, location, name string) (apimanagement.DeletedService, error)
	ListDeletedServices(ctx context.Context, sub string) ([]apimanagement.DeletedService, error)
	PurgeDeletedService(ctx context.Context, sub, location, name string) (apimanagement.DeletedService, error)

	childStore
}

// Handler serves Microsoft.ApiManagement ARM requests.
type Handler struct {
	store Store
}

// New returns an API Management handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets an API Management ARM URL: a service (and
// its children), a soft-deleted service, or checkNameAvailability. The
// provider and type are matched case-insensitively.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok || !strings.EqualFold(rp.Provider, providerName) {
		return false
	}

	switch strings.ToLower(rp.ResourceType) {
	case strings.ToLower(serviceType), strings.ToLower(typeDeletedServices), strings.ToLower(typeCheckName):
		return true
	case typeLocations:
		return strings.EqualFold(rp.SubResource, typeDeletedServices)
	default:
		return false
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	switch strings.ToLower(rp.ResourceType) {
	case strings.ToLower(typeCheckName):
		h.checkNameAvailability(w, r)
	case strings.ToLower(typeDeletedServices):
		h.listDeletedServices(w, r, &rp)
	case typeLocations:
		h.serveDeletedService(w, r, &rp)
	default:
		h.serveServiceTree(w, r, &rp)
	}
}

// serveServiceTree routes .../service, .../service/{name} and the service's
// child resources.
func (h *Handler) serveServiceTree(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch {
	case rp.ResourceName == "":
		h.listServices(w, r, rp)
	case rp.SubResource == "":
		h.serveService(w, r, rp)
	default:
		h.serveChild(w, r, rp, childSegments(r.URL.Path))
	}
}

// PurgeResourceGroup soft-deletes every service under sub/rg so a
// resource-group delete cascades into them.
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

// writeErr maps a provider error onto APIM's ARM error codes: validation
// failures are 400 ValidationError, a stale If-Match is 412 PreconditionFailed,
// and the three service conflicts carry their specific 409 codes.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	switch {
	case errors.Is(err, apimanagement.ErrLocationMismatch):
		azurearm.WriteError(w, http.StatusConflict, "InvalidResourceLocation", msg)
	case errors.Is(err, apimanagement.ErrNameNotAvailable):
		azurearm.WriteError(w, http.StatusConflict, "ServiceAlreadyExists", msg)
	case errors.Is(err, apimanagement.ErrSoftDeleted):
		azurearm.WriteError(w, http.StatusConflict, "ServiceAlreadyExistsInSoftDeletedState", msg)
	case errors.Is(err, apimanagement.ErrTierNotSupported):
		azurearm.WriteError(w, http.StatusBadRequest, "MethodNotAllowedInPricingTier", msg)
	case cerrors.IsInvalidArgument(err):
		azurearm.WriteError(w, http.StatusBadRequest, "ValidationError", msg)
	case cerrors.IsFailedPrecondition(err):
		azurearm.WriteError(w, http.StatusPreconditionFailed, "PreconditionFailed", msg)
	default:
		azurearm.WriteCErr(w, err)
	}
}

// methodNotAllowed writes the ARM 405.
func methodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}

// serveService routes the top-level service CRUD surface.
func (h *Handler) serveService(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createService(w, r, rp)
	case http.MethodPatch:
		h.updateService(w, r, rp)
	case http.MethodGet:
		h.getService(w, r, rp)
	case http.MethodDelete:
		h.deleteService(w, r, rp)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createService(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req serviceRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := serviceInputFromRequest(&req, r.Header.Get("If-Match"))

	s, created, err := h.store.CreateOrUpdateService(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, req.Location, &in)
	if err != nil {
		writeErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toServiceResponse(&s))
}

// updateService applies an ARM PATCH: supplied tags replace the set, sku and
// identity are re-resolved when named, and the properties block is merged. A
// PATCH on a missing service is a 404.
func (h *Handler) updateService(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var req serviceRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := serviceInputFromRequest(&req, r.Header.Get("If-Match"))

	s, err := h.store.UpdateService(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, &in)
	if err != nil {
		writeErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toServiceResponse(&s))
}

func (h *Handler) getService(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	s, err := h.store.GetService(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		writeErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toServiceResponse(&s))
}

// deleteService is the idempotent ARM DELETE (a soft delete): 200 when the
// service existed, 204 when it did not.
func (h *Handler) deleteService(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.DeleteServiceIfMatch(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, r.Header.Get("If-Match"))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeDeleted(w, existed)
}

// writeDeleted answers a DELETE: 200 when the resource existed, 204 when not.
func writeDeleted(w http.ResponseWriter, existed bool) {
	if existed {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listServices(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	var (
		items []apimanagement.Service
		err   error
	)

	if rp.ResourceGroup != "" {
		items, err = h.store.ListServicesByResourceGroup(r.Context(), rp.Subscription, rp.ResourceGroup)
	} else {
		items, err = h.store.ListServicesBySubscription(r.Context(), rp.Subscription)
	}

	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]serviceResponse, 0, len(items))
	for i := range items {
		out = append(out, toServiceResponse(&items[i]))
	}

	writeList(w, r, out, false)
}

// writeList pages a list with $skip/$top and a nextLink. withCount adds the
// APIM collection's total count.
func writeList[T any](w http.ResponseWriter, r *http.Request, items []T, withCount bool) {
	page, next := azurearm.Paginate(r, items, azurearm.DefaultPageSize)

	out := listResponse[T]{Value: page, NextLink: next}

	if withCount {
		n := len(items)
		out.Count = &n
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// checkNameAvailability answers POST .../providers/Microsoft.ApiManagement/checkNameAvailability.
func (h *Handler) checkNameAvailability(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var req nameAvailabilityRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	v := h.store.CheckNameAvailability(r.Context(), req.Name)
	azurearm.WriteJSON(w, http.StatusOK, nameAvailabilityResponse{
		NameAvailable: v.Available, Reason: v.Reason, Message: v.Message,
	})
}

// serveDeletedService routes .../locations/{l}/deletedservices/{name}: GET
// reads the soft-deleted service, DELETE purges it; both 404 when nothing by
// that name is soft-deleted there.
func (h *Handler) serveDeletedService(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" || rp.SubResourceAction != "" {
		azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType",
			"unsupported API Management deleted-services path")

		return
	}

	var (
		d   apimanagement.DeletedService
		err error
	)

	switch r.Method {
	case http.MethodGet:
		d, err = h.store.GetDeletedService(r.Context(), rp.Subscription, rp.ResourceName, rp.SubResourceName)
	case http.MethodDelete:
		d, err = h.store.PurgeDeletedService(r.Context(), rp.Subscription, rp.ResourceName, rp.SubResourceName)
	default:
		methodNotAllowed(w)
		return
	}

	if err != nil {
		writeErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toDeletedServiceResponse(&d))
}

// listDeletedServices answers GET .../providers/Microsoft.ApiManagement/deletedservices.
func (h *Handler) listDeletedServices(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet || rp.ResourceName != "" {
		methodNotAllowed(w)
		return
	}

	items, err := h.store.ListDeletedServices(r.Context(), rp.Subscription)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]deletedServiceResponse, 0, len(items))
	for i := range items {
		out = append(out, toDeletedServiceResponse(&items[i]))
	}

	writeList(w, r, out, false)
}
