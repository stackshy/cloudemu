package vcn

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	vcnprovider "github.com/stackshy/cloudemu/v2/providers/oci/vcn"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// assignedEntityPrivateIP is the only entity kind a public IP can be assigned
// to in OCI.
const assignedEntityPrivateIP = "PRIVATE_IP"

// Public IP scopes. A reserved address is drawn from a regional pool and
// survives reassignment; an ephemeral one lives and dies with the private IP
// it is attached to, so its scope is that IP's availability domain.
const (
	publicIPScopeRegion = "REGION"
	publicIPScopeAD     = "AVAILABILITY_DOMAIN"
)

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

	lifetime := req.Lifetime
	if lifetime == "" {
		lifetime = vcnprovider.LifetimeReserved
	}

	privateIPID := ""
	if req.PrivateIPID != nil {
		privateIPID = *req.PrivateIPID
	}

	// An ephemeral address exists only as an attachment, so OCI refuses to
	// create one that names no private IP.
	if lifetime == vcnprovider.LifetimeEphemeral && privateIPID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"privateIpId is required for an ephemeral public IP")

		return
	}

	info, err := h.net.AllocateAddress(r.Context(), netdriver.ElasticIPConfig{
		AllocationMethod: lifetime,
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
	if privateIPID != "" {
		if err := h.assign(r.Context(), info.AllocationID, privateIPID); err != nil {
			_ = h.net.ReleaseAddress(r.Context(), info.AllocationID)

			ocirest.WriteDriverError(w, r, err)

			return
		}

		info.AssociationID = privateIPID
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

	// An omitted privateIpId leaves the assignment alone; an empty one asks
	// for the address to be unassigned.
	if req.PrivateIPID != nil {
		if err := h.moveAssignment(r.Context(), info, *req.PrivateIPID); err != nil {
			ocirest.WriteDriverError(w, r, err)
			return
		}
	}

	tags := updatedTags(info.Tags, req.FreeformTags, tagDisplayName, req.DisplayName)

	if err := h.extras.SetTags(id, tags); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	info.Tags = tags

	ocirest.WriteJSON(w, r, http.StatusOK, h.toPublicIPResponse(info))
}

// moveAssignment applies an update's privateIpId. An ephemeral address is
// pinned to the private IP it was created on for its whole life, so OCI
// refuses to move or unassign one and deleting it is the only way to let go.
func (h *Handler) moveAssignment(ctx context.Context, info *netdriver.ElasticIP, privateIPID string) error {
	if info.AllocationMethod == vcnprovider.LifetimeEphemeral && privateIPID != info.AssociationID {
		return cerrors.Newf(cerrors.InvalidArgument,
			"ephemeral public IP %s cannot be reassigned or unassigned; delete it instead", info.AllocationID)
	}

	if err := h.reassign(ctx, info, privateIPID); err != nil {
		return err
	}

	info.AssociationID = privateIPID

	return nil
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

	if err := h.assign(ctx, info.AllocationID, privateIPID); err != nil {
		if previous != "" {
			// The original target was free a moment ago, so this restores the
			// binding; nothing better is available if it does not.
			_, _ = h.net.AssociateAddress(ctx, info.AllocationID, previous)
		}

		return err
	}

	return nil
}

// assign binds a public IP to a private IP, refusing one that sits in a subnet
// created with prohibitPublicIpOnVnic.
func (h *Handler) assign(ctx context.Context, allocationID, privateIPID string) error {
	if err := h.publicIPAllowed(ctx, privateIPID); err != nil {
		return err
	}

	_, err := h.net.AssociateAddress(ctx, allocationID, privateIPID)

	return err
}

// publicIPAllowed reports OCI's refusal to put a public IP on a VNIC in a
// subnet that prohibits them. An unknown private IP passes; AssociateAddress
// is the one that reports it as not found.
func (h *Handler) publicIPAllowed(ctx context.Context, privateIPID string) error {
	ips, err := h.extras.DescribePrivateIPs(ctx, []string{privateIPID})
	if err != nil || len(ips) == 0 {
		return err
	}

	subnets, err := h.net.DescribeSubnets(ctx, []string{ips[0].SubnetID})
	if err != nil || len(subnets) == 0 {
		return err
	}

	if boolTag(subnets[0].Tags, tagProhibitPublicIP) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"subnet %s prohibits public IPs on its VNICs", subnets[0].ID)
	}

	return nil
}

func (h *Handler) deletePublicIP(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findPublicIP(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	if err := h.release(r.Context(), info); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// release unassigns an assigned address before deleting it, which is what
// OCI's DeletePublicIp does and the only way an ephemeral can be let go. The
// portable driver keeps EC2's refusal to release an assigned address, so the
// unassign happens here and is undone when the release is refused.
func (h *Handler) release(ctx context.Context, info *netdriver.ElasticIP) error {
	if info.AssociationID == "" {
		return h.net.ReleaseAddress(ctx, info.AllocationID)
	}

	if err := h.net.DisassociateAddress(ctx, info.AssociationID); err != nil {
		return err
	}

	if err := h.net.ReleaseAddress(ctx, info.AllocationID); err != nil {
		// The private IP was this address's a moment ago, so this restores the
		// binding; nothing better is available if it does not.
		_, _ = h.net.AssociateAddress(ctx, info.AllocationID, info.AssociationID)

		return err
	}

	return nil
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
		Scope:          publicIPScope(info.AllocationMethod),
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

// publicIPScope reports the scope an address of the given lifetime lives at.
func publicIPScope(lifetime string) string {
	if lifetime == vcnprovider.LifetimeEphemeral {
		return publicIPScopeAD
	}

	return publicIPScopeRegion
}
