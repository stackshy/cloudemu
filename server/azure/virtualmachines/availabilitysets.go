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
	codeInvalidParameter         = "InvalidParameter"
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
		id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName,
			resourceTypeAvailabilitySets, rp.ResourceName)
		if len(h.availabilitySetMembers(r.Context(), id)) > 0 {
			azurearm.WriteError(w, http.StatusConflict, "OperationNotAllowed",
				"Availability Set '"+rp.ResourceName+"' cannot be deleted. Before deleting an Availability Set "+
					"please ensure that it does not contain any VM.")

			return
		}

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
// stored set). A create needs a location. The fault and update domain counts
// are fixed once the set exists: a request that changes either is a 409, on
// PUT and PATCH alike.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) putAvailabilitySet(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store computedriver.AzureAvailabilitySets,
) {
	var req availabilitySetRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	cur, getErr := store.GetAvailabilitySet(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	set, ok := baseAvailabilitySet(w, r.Method, rp, &req, cur, getErr)
	if !ok {
		return
	}

	req.applyTo(&set)

	if cur != nil && (set.PlatformFaultDomainCount != cur.PlatformFaultDomainCount ||
		set.PlatformUpdateDomainCount != cur.PlatformUpdateDomainCount) {
		azurearm.WriteError(w, http.StatusConflict, "PropertyChangeNotAllowed",
			"Changing property 'platformFaultDomainCount' or 'platformUpdateDomainCount' is not allowed.")

		return
	}

	stored, err := store.PutAvailabilitySet(r.Context(), set)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// AvailabilitySets CreateOrUpdate is synchronous and answers 200 for both
	// create and update; the SDK rejects a 201.
	azurearm.WriteJSON(w, http.StatusOK, h.toAvailabilitySetResponse(r.Context(), stored))
}

// baseAvailabilitySet is the set a request applies to: the stored one for
// PATCH (which must exist) and for a PUT that replaces an existing set, or a
// new one with the defaults. It writes the error and reports false when the
// request cannot proceed.
//
//nolint:gocritic // rp is a request-scoped value
func baseAvailabilitySet(
	w http.ResponseWriter, method string, rp azurearm.ResourcePath, req *availabilitySetRequest,
	cur *computedriver.AzureAvailabilitySet, getErr error,
) (computedriver.AzureAvailabilitySet, bool) {
	switch {
	case method == http.MethodPatch && cur == nil:
		azurearm.WriteCErr(w, getErr)
		return computedriver.AzureAvailabilitySet{}, false
	case method == http.MethodPatch:
		set := *cur
		if req.Tags != nil {
			set.Tags = req.Tags
		}

		return set, true
	case cur != nil:
		set := *cur
		set.Tags = req.Tags

		return set, true
	case req.Location == "":
		azurearm.WriteError(w, http.StatusBadRequest, "LocationRequired",
			"The location property is required for this definition.")

		return computedriver.AzureAvailabilitySet{}, false
	}

	return computedriver.AzureAvailabilitySet{
		Name: rp.ResourceName, Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup,
		Location: req.Location, Tags: req.Tags, SKUName: "Classic",
		PlatformFaultDomainCount: defaultFaultDomains, PlatformUpdateDomainCount: defaultUpdateDomains,
	}, true
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

// armError is an ARM error response to write.
type armError struct {
	status  int
	code    string
	message string
}

// checkAvailabilitySet validates a VM's availabilitySet reference: the set
// must exist (404) and sit in the VM's subscription, resource group and
// location (400 InvalidParameter). An empty reference is valid.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) checkAvailabilitySet(
	ctx context.Context, rp azurearm.ResourcePath, ref *subResource, vmLocation string,
) *armError {
	if ref == nil || ref.ID == "" {
		return nil
	}

	store, ok := h.compute.(computedriver.AzureAvailabilitySets)
	if !ok {
		return nil
	}

	pp, ok := azurearm.ParsePath(ref.ID)
	if !ok || pp.ResourceName == "" {
		return &armError{http.StatusBadRequest, codeInvalidParameter, "Availability set id '" + ref.ID + "' is malformed."}
	}

	set, err := store.GetAvailabilitySet(ctx, pp.Subscription, pp.ResourceGroup, pp.ResourceName)
	if err != nil {
		return &armError{http.StatusNotFound, "NotFound", "The Resource '" + ref.ID + "' was not found."}
	}

	return availabilitySetScopeError(rp, &pp, ref.ID, set.Location, vmLocation)
}

// availabilitySetScopeError rejects a set outside the VM's resource group or
// location.
//
//nolint:gocritic // rp is a request-scoped value
func availabilitySetScopeError(
	rp azurearm.ResourcePath, setPath *azurearm.ResourcePath, setID, setLocation, vmLocation string,
) *armError {
	if !strings.EqualFold(setPath.Subscription, rp.Subscription) ||
		!strings.EqualFold(setPath.ResourceGroup, rp.ResourceGroup) {
		return &armError{http.StatusBadRequest, codeInvalidParameter, "Availability set '" + setID +
			"' must be in the same resource group as virtual machine '" + rp.ResourceName + "'."}
	}

	if vmLocation != "" && !strings.EqualFold(setLocation, vmLocation) {
		return &armError{http.StatusBadRequest, codeInvalidParameter, "Availability set '" + setID +
			"' is in location '" + setLocation + "', which differs from virtual machine location '" + vmLocation + "'."}
	}

	return nil
}
