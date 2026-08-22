package compute

import (
	"net/http"
	"strings"

	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// Instance pool actions, which OCI puts under /actions rather than on a query
// parameter as it does for a single instance.
const (
	poolActionStart     = "start"
	poolActionStop      = "stop"
	poolActionReset     = "reset"
	poolActionSoftReset = "softreset"
)

// serveInstancePool routes the instancePools collection, its actions and its
// member listing.
func (h *Handler) serveInstancePool(w http.ResponseWriter, r *http.Request, rt route) {
	switch rt.Sub {
	case subActions:
		h.instancePoolAction(w, r, rt.ID, rt.Action)
	case subInstances:
		h.listInstancePoolInstances(w, r, rt.ID)
	default:
		serveCRUD(w, r, rt, crud{
			create: h.createInstancePool,
			list:   h.listInstancePools,
			get:    h.getInstancePool,
			update: h.updateInstancePool,
			remove: h.terminateInstancePool,
		})
	}
}

func (h *Handler) createInstancePool(w http.ResponseWriter, r *http.Request) {
	var req instancePoolRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	if req.InstanceConfigurationID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"instanceConfigurationId is required")

		return
	}

	size := 0
	if req.Size != nil {
		size = *req.Size
	}

	pool, err := h.extras.CreateInstancePool(r.Context(), req.DisplayName, req.InstanceConfigurationID,
		size, toPoolPlacements(req.Placements), freeformOf(req.FreeformTags))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.placePool(pool, req.CompartmentID)
	h.accept(w, "CREATE_INSTANCE_POOL", req.CompartmentID, "instancepool", workrequest.ActionCreated, pool.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, h.toInstancePoolResponse(pool))
}

// placePool records the compartment for a pool and every instance it launched,
// which the driver created in the provider's default compartment.
func (h *Handler) placePool(pool *ocicompute.InstancePool, compartmentID string) {
	h.place(pool.ID, compartmentID)

	for _, id := range pool.InstanceIDs {
		h.place(id, compartmentID)
	}
}

func (h *Handler) listInstancePools(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	pools, err := h.extras.ListInstancePools(r.Context(), compartmentID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	renderPage(w, r, pools, h.toInstancePoolResponse)
}

func (h *Handler) getInstancePool(w http.ResponseWriter, r *http.Request, id string) {
	pool, err := h.extras.GetInstancePool(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toInstancePoolResponse(pool))
}

func (h *Handler) updateInstancePool(w http.ResponseWriter, r *http.Request, id string) {
	var req instancePoolRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	size := -1
	if req.Size != nil {
		size = *req.Size
	}

	pool, err := h.extras.UpdateInstancePool(r.Context(), id,
		displayNameUpdate(req.DisplayName, req.FreeformTags), size)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.placePool(pool, h.compartmentOf(id))
	h.accept(w, "UPDATE_INSTANCE_POOL", h.compartmentOf(id), "instancepool", workrequest.ActionUpdated, id)
	ocirest.WriteJSON(w, r, http.StatusOK, h.toInstancePoolResponse(pool))
}

func (h *Handler) terminateInstancePool(w http.ResponseWriter, r *http.Request, id string) {
	compartmentID := h.compartmentOf(id)

	if err := h.extras.TerminateInstancePool(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, "TERMINATE_INSTANCE_POOL", compartmentID, "instancepool", workrequest.ActionDeleted, id)
	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) instancePoolAction(w http.ResponseWriter, r *http.Request, id, action string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	var driverAction string

	switch strings.ToLower(action) {
	case poolActionStart:
		driverAction = ocicompute.PoolActionStart
	case poolActionStop:
		driverAction = ocicompute.PoolActionStop
	case poolActionReset, poolActionSoftReset:
		driverAction = ocicompute.PoolActionReset
	case actionChangeCompartment:
		h.changeCompartment(w, r, id)
		return
	default:
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
			"instance pool action "+action+" is not emulated; use start, stop, reset or softreset")

		return
	}

	pool, err := h.extras.InstancePoolAction(r.Context(), id, driverAction)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, "INSTANCE_POOL_"+strings.ToUpper(action), h.compartmentOf(id), "instancepool",
		workrequest.ActionUpdated, id)
	ocirest.WriteJSON(w, r, http.StatusOK, h.toInstancePoolResponse(pool))
}

func (h *Handler) listInstancePoolInstances(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}

	if _, given := ocirest.RequireCompartmentID(w, r); !given {
		return
	}

	members, err := h.extras.ListInstancePoolInstances(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := make([]instancePoolInstanceResponse, 0, len(members))

	for i := range members {
		out = append(out, instancePoolInstanceResponse{
			ID:                 members[i].ID,
			InstanceID:         members[i].InstanceID,
			CompartmentID:      h.compartmentOf(members[i].InstanceID),
			AvailabilityDomain: members[i].AvailabilityDomain,
			DisplayName:        members[i].DisplayName,
			Shape:              members[i].Shape,
			State:              instanceLifecycle(members[i].State),
			TimeCreated:        members[i].TimeCreated,
		})
	}

	writePage(w, r, out)
}

func toPoolPlacements(wire []instancePoolPlacementWire) []ocicompute.PoolPlacement {
	out := make([]ocicompute.PoolPlacement, 0, len(wire))

	for i := range wire {
		out = append(out, ocicompute.PoolPlacement{
			AvailabilityDomain: wire[i].AvailabilityDomain,
			PrimarySubnetID:    wire[i].PrimarySubnetID,
			FaultDomains:       wire[i].FaultDomains,
		})
	}

	return out
}

func (h *Handler) toInstancePoolResponse(p *ocicompute.InstancePool) instancePoolResponse {
	placements := make([]instancePoolPlacementWire, 0, len(p.Placements))

	for i := range p.Placements {
		placements = append(placements, instancePoolPlacementWire{
			AvailabilityDomain: p.Placements[i].AvailabilityDomain,
			PrimarySubnetID:    p.Placements[i].PrimarySubnetID,
			FaultDomains:       p.Placements[i].FaultDomains,
		})
	}

	return instancePoolResponse{
		ID:                      p.ID,
		CompartmentID:           h.compartmentOf(p.ID),
		DisplayName:             p.DisplayName,
		InstanceConfigurationID: p.InstanceConfigurationID,
		Size:                    p.Size,
		Placements:              placements,
		LifecycleState:          p.LifecycleState,
		TimeCreated:             p.TimeCreated,
		FreeformTags:            freeformOf(p.Tags),
		DefinedTags:             definedTags{},
	}
}
