package virtualmachines

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	providervm "github.com/stackshy/cloudemu/v2/providers/azure/virtualmachines"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// scaleSetStore is the subset of the Azure virtualmachines mock the handler
// type-asserts to when serving virtualMachineScaleSets. Drivers that don't
// implement it (e.g. AWS/GCP compute) fall through to 501.
type scaleSetStore interface {
	CreateScaleSet(ctx context.Context, s providervm.ScaleSet) (*providervm.ScaleSet, error)
	ListScaleSets(ctx context.Context) ([]providervm.ScaleSet, error)
	DeleteScaleSet(ctx context.Context, name string) error
	ListScaleSetVMs(ctx context.Context, vmssName string) ([]providervm.ScaleSetVM, error)
	GetScaleSetVM(ctx context.Context, vmssName, instanceID string) (*providervm.ScaleSetVM, error)
	DeleteScaleSetVM(ctx context.Context, vmssName, instanceID string) error
	PowerScaleSetVM(ctx context.Context, vmssName, instanceID, action string) error
}

// serveScaleSet dispatches PUT/GET on Microsoft.Compute/virtualMachineScaleSets.
//
//nolint:gocritic // rp is a request-scoped value passed once per request
func (h *Handler) serveScaleSet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	store, ok := h.compute.(scaleSetStore)
	if !ok {
		writeNotImplemented(w, "virtualMachineScaleSets")
		return
	}

	if rp.SubResource != "" {
		serveScaleSetSubResource(w, r, rp, store)
		return
	}

	if rp.ResourceName == "" {
		if r.Method == http.MethodGet {
			listScaleSets(w, r, rp, store)
			return
		}

		writeNotImplemented(w, r.Method+" "+r.URL.Path)

		return
	}

	switch r.Method {
	case http.MethodPut:
		createScaleSet(w, r, rp, store)
	case http.MethodGet:
		getScaleSet(w, r, rp, store)
	case http.MethodDelete:
		deleteScaleSet(w, r, rp, store)
	default:
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
	}
}

// deleteScaleSet handles DELETE virtualMachineScaleSets/{name}. Real Azure
// returns 202 Accepted with the async-operation polling headers; the SDK's
// poller then observes the operation Succeeded and a follow-up GET reports
// NotFound.
//
//nolint:gocritic // rp is a request-scoped value
func deleteScaleSet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store scaleSetStore) {
	if err := store.DeleteScaleSet(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "vmss-delete-"+rp.ResourceName)
}

// createScaleSet handles PUT virtualMachineScaleSets/{name}.
//
//nolint:gocritic // rp is a request-scoped value
func createScaleSet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store scaleSetStore) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req vmssRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	set := providervm.ScaleSet{
		Name:          rp.ResourceName,
		ID:            azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceTypeScaleSets, rp.ResourceName),
		Location:      req.Location,
		Tags:          req.Tags,
		ResourceGroup: rp.ResourceGroup,
	}

	if req.SKU != nil {
		set.SKUName = req.SKU.Name
		set.SKUTier = req.SKU.Tier

		if req.SKU.Capacity != nil {
			set.Capacity = int(*req.SKU.Capacity)
			set.CapacityZero = *req.SKU.Capacity == 0
		}
	}

	if p := req.Properties.VirtualMachineProfile; p != nil {
		set.Priority = p.Priority
		set.LicenseType = p.LicenseType
		set.OSType = osTypeFromStorage(p.StorageProfile)
	}

	stored, err := store.CreateScaleSet(r.Context(), set)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toVMSSResponse(stored, rp))
}

// getScaleSet handles GET virtualMachineScaleSets/{name}.
//
//nolint:gocritic // rp is a request-scoped value
func getScaleSet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store scaleSetStore) {
	sets, err := store.ListScaleSets(r.Context())
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	for i := range sets {
		// ARM resource names are case-insensitive, so a GET with a
		// differently-cased scale-set name must still resolve it.
		if strings.EqualFold(sets[i].Name, rp.ResourceName) {
			azurearm.WriteJSON(w, http.StatusOK, toVMSSResponse(&sets[i], rp))
			return
		}
	}

	azurearm.WriteCErr(w, cerrors.Newf(cerrors.NotFound, "virtualMachineScaleSet %s not found", rp.ResourceName))
}

// listScaleSets handles GET virtualMachineScaleSets (collection).
//
//nolint:gocritic // rp is a request-scoped value
func listScaleSets(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store scaleSetStore) {
	sets, err := store.ListScaleSets(r.Context())
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]vmssResponse, 0, len(sets))

	for i := range sets {
		scope := rp
		scope.ResourceName = sets[i].Name
		out = append(out, toVMSSResponse(&sets[i], scope))
	}

	azurearm.WriteJSON(w, http.StatusOK, vmssListResponse{Value: out})
}

// toVMSSResponse maps a stored ScaleSet onto the ARM wire shape.
//
//nolint:gocritic // rp is a value type passed once per response build
func toVMSSResponse(s *providervm.ScaleSet, rp azurearm.ResourcePath) vmssResponse {
	return vmssResponse{
		ID:       azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceTypeScaleSets, s.Name),
		Name:     s.Name,
		Type:     providerName + "/" + resourceTypeScaleSets,
		Location: defaultIfEmpty(s.Location, "eastus"),
		Tags:     s.Tags,
		SKU:      &vmssSKU{Name: s.SKUName, Tier: s.SKUTier, Capacity: capacityPtr(s.Capacity)},
		Properties: vmssResponseProps{
			ProvisioningState: "Succeeded",
			VirtualMachineProfile: &vmssVMProfile{
				Priority:       s.Priority,
				LicenseType:    s.LicenseType,
				StorageProfile: osDiskProfile(s.OSType),
			},
		},
	}
}

// capacityPtr converts the stored capacity (always a concrete value after
// CreateScaleSet defaulting) to the pointer form the ARM wire shape uses.
func capacityPtr(capacity int) *int64 {
	c := int64(capacity)
	return &c
}
