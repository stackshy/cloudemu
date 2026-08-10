package vcn

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Work request operations the CIDR-block actions record.
const (
	operationAddCIDR    = "ADD_VCN_CIDR"
	operationRemoveCIDR = "REMOVE_VCN_CIDR"
)

// serveVCN routes the VCN collection and the actions on a single VCN.
func (h *Handler) serveVCN(w http.ResponseWriter, r *http.Request, rt route) {
	if rt.Sub == subActions {
		h.vcnAction(w, r, rt)
		return
	}

	serveCRUD(w, r, rt, h.vcnOps())
}

// vcnAction serves the CIDR-block mutations. OCI models them as actions on the
// VCN rather than as an update, and runs them asynchronously.
func (h *Handler) vcnAction(w http.ResponseWriter, r *http.Request, rt route) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	switch rt.Action {
	case actionAddVCNCIDR:
		h.changeVCNCIDR(w, r, rt.ID, operationAddCIDR, h.extras.AddVCNCIDR)
	case actionRemoveVCNCIDR:
		h.changeVCNCIDR(w, r, rt.ID, operationRemoveCIDR, h.extras.RemoveVCNCIDR)
	default:
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown action "+rt.Action)
	}
}

// changeVCNCIDR applies one CIDR-block mutation and records the work request
// its caller waits on.
func (h *Handler) changeVCNCIDR(
	w http.ResponseWriter, r *http.Request, id, operation string,
	apply func(ctx context.Context, vcnID, cidr string) error,
) {
	if h.work == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented, "work requests are not configured")
		return
	}

	var req vcnCIDRRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CIDRBlock == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "cidrBlock is required")
		return
	}

	if err := apply(r.Context(), id, req.CIDRBlock); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	wrID := h.work.Accept(operation, h.compartmentOf(id), workrequest.Resource{
		EntityType: ocidType(id),
		ActionType: workrequest.ActionUpdated,
		Identifier: id,
	})

	ocirest.SetWorkRequestID(w, wrID)
	ocirest.WriteJSON(w, r, http.StatusAccepted, nil)
}

func (h *Handler) vcnOps() crud {
	return crud{
		create: h.createVCN,
		list:   h.listVCNs,
		get:    h.getVCN,
		update: h.updateVCN,
		remove: h.deleteVCN,
	}
}

func (h *Handler) createVCN(w http.ResponseWriter, r *http.Request) {
	var req vcnRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	if req.CIDRBlock != "" && len(req.CIDRBlocks) > 0 {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"cidrBlock and cidrBlocks are mutually exclusive")

		return
	}

	cidrs := req.CIDRBlocks
	if len(cidrs) == 0 {
		cidrs = []string{req.CIDRBlock}
	}

	info, err := h.net.CreateVPC(r.Context(), netdriver.VPCConfig{
		CIDRBlock: cidrs[0],
		Tags:      withInternal(req.FreeformTags, tagDisplayName, req.DisplayName, tagDNSLabel, req.DNSLabel),
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.place(info.ID, req.CompartmentID)

	// CreateVcn takes the whole list of blocks; the portable driver takes one,
	// so the rest are added after and a refused block takes the half-built VCN
	// down with it.
	for _, cidr := range cidrs[1:] {
		if err := h.extras.AddVCNCIDR(r.Context(), info.ID, cidr); err != nil {
			_ = h.net.DeleteVPC(r.Context(), info.ID)

			ocirest.WriteDriverError(w, r, err)

			return
		}
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toVCNResponse(info))
}

func (h *Handler) listVCNs(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := h.net.DescribeVPCs(r.Context(), nil)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	scopedList(h, w, r, compartmentID, infos,
		func(v *netdriver.VPCInfo) (string, string) { return v.ID, "" },
		h.toVCNResponse)
}

func (h *Handler) getVCN(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findVCN(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toVCNResponse(info))
}

//nolint:dupl // see updateLPG: the tag-only updates differ by request type.
func (h *Handler) updateVCN(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findVCN(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	var req vcnRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	tags := updatedTags(info.Tags, req.FreeformTags,
		tagDisplayName, req.DisplayName, tagDNSLabel, req.DNSLabel)

	if err := h.extras.SetTags(id, tags); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	info.Tags = tags

	ocirest.WriteJSON(w, r, http.StatusOK, h.toVCNResponse(info))
}

func (h *Handler) deleteVCN(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.net.DeleteVPC(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// findVCN reads one VCN, reporting OCI's not-found for an unknown OCID.
func (h *Handler) findVCN(ctx context.Context, id string) (*netdriver.VPCInfo, error) {
	infos, err := h.net.DescribeVPCs(ctx, []string{id})
	if err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "vcn %s not found", id)
	}

	return &infos[0], nil
}

func (h *Handler) toVCNResponse(info *netdriver.VPCInfo) vcnResponse {
	defaults := h.extras.Defaults(info.ID)
	label := tagOr(info.Tags, tagDNSLabel, "")

	blocks := h.extras.VCNCIDRs(info.ID)
	if len(blocks) == 0 {
		blocks = []string{info.CIDRBlock}
	}

	return vcnResponse{
		ID:                    info.ID,
		CompartmentID:         h.compartmentOf(info.ID),
		CIDRBlock:             blocks[0],
		CIDRBlocks:            blocks,
		DisplayName:           tagOr(info.Tags, tagDisplayName, ""),
		DNSLabel:              label,
		VCNDomainName:         domainName(label, "oraclevcn.com"),
		DefaultRouteTableID:   defaults.RouteTableID,
		DefaultSecurityListID: defaults.SecurityListID,
		DefaultDHCPOptionsID:  defaults.DHCPOptionsID,
		LifecycleState:        info.State,
		TimeCreated:           h.extras.Created(info.ID),
		FreeformTags:          freeformOf(info.Tags),
		DefinedTags:           definedTags{},
	}
}

// updatedTags is the tag map an update leaves behind: freeform tags are
// replaced when the body carries them and kept when it does not, and an
// internal attribute survives unless the update names it.
func updatedTags(existing, freeform map[string]string, kv ...string) map[string]string {
	base := freeform
	if base == nil {
		base = freeformOf(existing)
	}

	keep := make([]string, 0, len(kv))

	for i := 0; i+1 < len(kv); i += pairStride {
		value := kv[i+1]
		if value == "" {
			value = existing[kv[i]]
		}

		keep = append(keep, kv[i], value)
	}

	return withInternal(base, keep...)
}
