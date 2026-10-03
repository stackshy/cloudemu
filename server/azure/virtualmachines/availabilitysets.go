package virtualmachines

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

const (
	resourceTypeAvailabilitySets = "availabilitySets"
	skuAligned                   = "Aligned"
	defaultFaultDomains          = 2
	defaultUpdateDomains         = 5
)

type availabilitySetSKU struct {
	Name string `json:"name"`
}

type availabilitySetListResponse struct {
	Value []availabilitySetResponse `json:"value"`
}

type availabilitySetRequest struct {
	Location   string              `json:"location"`
	Tags       map[string]string   `json:"tags"`
	SKU        *availabilitySetSKU `json:"sku"`
	Properties struct {
		PlatformFaultDomainCount  *int         `json:"platformFaultDomainCount"`
		PlatformUpdateDomainCount *int         `json:"platformUpdateDomainCount"`
		ProximityPlacementGroup   *subResource `json:"proximityPlacementGroup"`
	} `json:"properties"`
}

type availabilitySetResponse struct {
	ID         string                   `json:"id"`
	Name       string                   `json:"name"`
	Type       string                   `json:"type"`
	Location   string                   `json:"location"`
	Tags       map[string]string        `json:"tags,omitempty"`
	SKU        availabilitySetSKU       `json:"sku"`
	Properties availabilitySetRespProps `json:"properties"`
}

type availabilitySetRespProps struct {
	PlatformFaultDomainCount  int           `json:"platformFaultDomainCount"`
	PlatformUpdateDomainCount int           `json:"platformUpdateDomainCount"`
	ProximityPlacementGroup   *subResource  `json:"proximityPlacementGroup,omitempty"`
	VirtualMachines           []subResource `json:"virtualMachines"`
}

// serveAvailabilitySet handles Microsoft.Compute/availabilitySets
// (CreateOrUpdate/Update/Get/List/Delete).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveAvailabilitySet(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	store, ok := h.compute.(computedriver.AzureAvailabilitySets)
	if !ok {
		writeNotImplemented(w, resourceTypeAvailabilitySets)
		return
	}

	if azurearm.GuardLeaf(w, r, &rp) {
		return
	}

	if rp.ResourceName == "" {
		h.listAvailabilitySets(w, r, rp, store)
		return
	}

	switch r.Method {
	case http.MethodPut, http.MethodPatch:
		h.putAvailabilitySet(w, r, rp, store)
	case http.MethodGet:
		set, err := store.GetAvailabilitySet(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, h.toAvailabilitySetResponse(r.Context(), set))
	case http.MethodDelete:
		// Deleting an absent set is idempotent in ARM: 204.
		status := http.StatusOK
		if store.DeleteAvailabilitySet(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName) != nil {
			status = http.StatusNoContent
		}

		w.WriteHeader(status)
	default:
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listAvailabilitySets(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store computedriver.AzureAvailabilitySets,
) {
	if r.Method != http.MethodGet {
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
		return
	}

	sets, err := store.ListAvailabilitySets(r.Context(), rp.Subscription, rp.ResourceGroup)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]availabilitySetResponse, 0, len(sets))
	for i := range sets {
		out = append(out, h.toAvailabilitySetResponse(r.Context(), &sets[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, availabilitySetListResponse{Value: out})
}

// putAvailabilitySet serves PUT (create or replace) and PATCH (merge onto the
// stored set; the domain counts are immutable in Azure, so PATCH only changes
// tags and the placement group).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) putAvailabilitySet(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store computedriver.AzureAvailabilitySets,
) {
	var req availabilitySetRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	set := computedriver.AzureAvailabilitySet{
		Name: rp.ResourceName, Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup,
		Location: defaultIfEmpty(req.Location, defaultVMSSLocation), Tags: req.Tags, SKUName: "Classic",
		PlatformFaultDomainCount: defaultFaultDomains, PlatformUpdateDomainCount: defaultUpdateDomains,
	}

	if r.Method == http.MethodPatch {
		cur, err := store.GetAvailabilitySet(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		set = *cur
		if req.Tags != nil {
			set.Tags = req.Tags
		}
	}

	req.applyTo(&set)

	stored, err := store.PutAvailabilitySet(r.Context(), set)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// AvailabilitySets CreateOrUpdate is synchronous and answers 200 for both
	// create and update; the SDK rejects a 201.
	azurearm.WriteJSON(w, http.StatusOK, h.toAvailabilitySetResponse(r.Context(), stored))
}

// applyTo copies the fields the request carries onto set.
func (req *availabilitySetRequest) applyTo(set *computedriver.AzureAvailabilitySet) {
	if req.SKU != nil && req.SKU.Name != "" {
		set.SKUName = req.SKU.Name
	}

	if p := req.Properties.PlatformFaultDomainCount; p != nil {
		set.PlatformFaultDomainCount = *p
	}

	if p := req.Properties.PlatformUpdateDomainCount; p != nil {
		set.PlatformUpdateDomainCount = *p
	}

	if ppg := req.Properties.ProximityPlacementGroup; ppg != nil {
		set.ProximityPlacementGroupID = ppg.ID
	}
}

// toAvailabilitySetResponse renders a set with the VMs that reference it.
func (h *Handler) toAvailabilitySetResponse(
	ctx context.Context, set *computedriver.AzureAvailabilitySet,
) availabilitySetResponse {
	id := azurearm.BuildResourceID(set.Subscription, set.ResourceGroup, providerName,
		resourceTypeAvailabilitySets, set.Name)

	props := availabilitySetRespProps{
		PlatformFaultDomainCount:  set.PlatformFaultDomainCount,
		PlatformUpdateDomainCount: set.PlatformUpdateDomainCount,
		VirtualMachines:           h.availabilitySetMembers(ctx, id),
	}

	if set.ProximityPlacementGroupID != "" {
		props.ProximityPlacementGroup = &subResource{ID: set.ProximityPlacementGroupID}
	}

	return availabilitySetResponse{
		ID: id, Name: set.Name, Type: providerName + "/" + resourceTypeAvailabilitySets,
		Location: set.Location, Tags: set.Tags, SKU: availabilitySetSKU{Name: set.SKUName},
		Properties: props,
	}
}

// availabilitySetMembers lists the VMs whose availabilitySet reference is id.
func (h *Handler) availabilitySetMembers(ctx context.Context, id string) []subResource {
	out := []subResource{}

	insts, err := h.compute.DescribeInstances(ctx, nil, nil)
	if err != nil {
		return out
	}

	for i := range insts {
		inst := &insts[i]
		if inst.State != stateTerminated && strings.EqualFold(inst.Tags[availabilitySetTag], id) {
			out = append(out, subResource{ID: azurearm.BuildResourceID(
				tagOr(inst.Tags, subTag, ""), inst.ResourceGroup, providerName, resourceType,
				tagOr(inst.Tags, armNameTag, inst.ID))})
		}
	}

	return out
}

// validateAvailabilitySet reports whether a VM's availabilitySet reference
// resolves to an existing set. An empty reference is valid.
func (h *Handler) validateAvailabilitySet(ctx context.Context, ref *subResource) bool {
	if ref == nil || ref.ID == "" {
		return true
	}

	store, ok := h.compute.(computedriver.AzureAvailabilitySets)
	if !ok {
		return true
	}

	pp, ok := azurearm.ParsePath(ref.ID)
	if !ok || pp.ResourceName == "" {
		return false
	}

	_, err := store.GetAvailabilitySet(ctx, pp.Subscription, pp.ResourceGroup, pp.ResourceName)

	return err == nil
}
