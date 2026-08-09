package vcn

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// assignedEntityPrivateIP is the only entity kind a public IP can be assigned
// to in OCI.
const assignedEntityPrivateIP = "PRIVATE_IP"

// publicIPScopeRegion is the scope a reserved public IP lives at.
const publicIPScopeRegion = "REGION"

// Public IPs.

func (h *Handler) publicIPOps() crud {
	return crud{
		create: h.createPublicIP,
		list:   h.listPublicIPs,
		get:    h.getPublicIP,
		update: h.updatePublicIP,
		remove: h.deletePublicIP,
	}
}

func (h *Handler) createPublicIP(w http.ResponseWriter, r *http.Request) {
	var req publicIPRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	info, err := h.net.AllocateAddress(r.Context(), netdriver.ElasticIPConfig{
		AllocationMethod: req.Lifetime,
		Tags:             withInternal(req.FreeformTags, tagDisplayName, req.DisplayName),
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.place(info.AllocationID, req.CompartmentID)

	// OCI assigns at create; the portable driver splits allocate from
	// associate, so an associate that fails has to release the address the
	// first half already allocated.
	if req.PrivateIPID != "" {
		if _, err := h.net.AssociateAddress(r.Context(), info.AllocationID, req.PrivateIPID); err != nil {
			_ = h.net.ReleaseAddress(r.Context(), info.AllocationID)

			ocirest.WriteDriverError(w, r, err)

			return
		}

		info.AssociationID = req.PrivateIPID
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toPublicIPResponse(info))
}

func (h *Handler) listPublicIPs(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := h.net.DescribeAddresses(r.Context(), nil)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	lifetime := r.URL.Query().Get("lifetime")
	out := make([]publicIPResponse, 0, len(infos))

	for i := range infos {
		if !h.inCompartment(infos[i].AllocationID, compartmentID) {
			continue
		}

		if lifetime != "" && infos[i].AllocationMethod != lifetime {
			continue
		}

		out = append(out, h.toPublicIPResponse(&infos[i]))
	}

	ocirest.WriteJSON(w, r, http.StatusOK, paginate(w, r, out))
}

func (h *Handler) getPublicIP(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findPublicIP(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toPublicIPResponse(info))
}

// updatePublicIP assigns or unassigns the address, which is what OCI's
// UpdatePublicIp does with privateIpId.
func (h *Handler) updatePublicIP(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findPublicIP(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	var req publicIPRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.reassign(r.Context(), info, req.PrivateIPID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	tags := updatedTags(info.Tags, req.FreeformTags, tagDisplayName, req.DisplayName)

	if err := h.extras.SetTags(id, tags); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	info.Tags = tags
	info.AssociationID = req.PrivateIPID

	ocirest.WriteJSON(w, r, http.StatusOK, h.toPublicIPResponse(info))
}

// reassign moves a public IP onto the named private IP, detaching first when
// it is already assigned elsewhere.
//
// AssociateAddress refuses a private IP that already holds one, so the detach
// has to be undone when the move fails — otherwise a rejected reassign leaves
// the address bound to nothing.
func (h *Handler) reassign(ctx context.Context, info *netdriver.ElasticIP, privateIPID string) error {
	if info.AssociationID == privateIPID {
		return nil
	}

	previous := info.AssociationID

	if previous != "" {
		if err := h.net.DisassociateAddress(ctx, previous); err != nil {
			return err
		}
	}

	if privateIPID == "" {
		return nil
	}

	if _, err := h.net.AssociateAddress(ctx, info.AllocationID, privateIPID); err != nil {
		if previous != "" {
			// The original target was free a moment ago, so this restores the
			// binding; nothing better is available if it does not.
			_, _ = h.net.AssociateAddress(ctx, info.AllocationID, previous)
		}

		return err
	}

	return nil
}

func (h *Handler) deletePublicIP(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.net.ReleaseAddress(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) findPublicIP(ctx context.Context, id string) (*netdriver.ElasticIP, error) {
	infos, err := h.net.DescribeAddresses(ctx, []string{id})
	if err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "publicIp %s not found", id)
	}

	return &infos[0], nil
}

func (h *Handler) toPublicIPResponse(info *netdriver.ElasticIP) publicIPResponse {
	out := publicIPResponse{
		ID:             info.AllocationID,
		CompartmentID:  h.compartmentOf(info.AllocationID),
		IPAddress:      info.PublicIP,
		DisplayName:    tagOr(info.Tags, tagDisplayName, ""),
		Lifetime:       info.AllocationMethod,
		Scope:          publicIPScopeRegion,
		LifecycleState: lifecycleAvailable,
		TimeCreated:    h.extras.Created(info.AllocationID),
		FreeformTags:   freeformOf(info.Tags),
		DefinedTags:    definedTags{},
	}

	if info.AssociationID != "" {
		out.AssignedEntityID = info.AssociationID
		out.AssignedEntityType = assignedEntityPrivateIP
		out.PrivateIPID = info.AssociationID
	}

	return out
}
