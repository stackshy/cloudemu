package vcn

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	vcnprovider "github.com/stackshy/cloudemu/v2/providers/oci/vcn"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// VNICs. OCI creates them through Compute's VNIC attachments, so the
// networking surface only reads and updates them.

func (h *Handler) serveVNIC(w http.ResponseWriter, r *http.Request, rt route) {
	if rt.ID == "" {
		methodNotAllowed(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getVNIC(w, r, rt.ID)
	case http.MethodPut:
		h.updateVNIC(w, r, rt.ID)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) getVNIC(w http.ResponseWriter, r *http.Request, id string) {
	vnics, err := h.extras.DescribeVNICs(r.Context(), []string{id})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	if len(vnics) == 0 {
		ocirest.WriteDriverError(w, r, cerrors.Newf(cerrors.NotFound, "vnic %s not found", id))
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toVNICResponse(&vnics[0]))
}

func (h *Handler) updateVNIC(w http.ResponseWriter, r *http.Request, id string) {
	var req vnicRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	vnic, err := h.extras.UpdateVNIC(r.Context(), id, req.DisplayName, req.HostnameLabel, req.NSGIDs)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toVNICResponse(vnic))
}

func (h *Handler) toVNICResponse(v *vcnprovider.VNIC) vnicResponse {
	nsgIDs := v.NSGIDs
	if nsgIDs == nil {
		nsgIDs = []string{}
	}

	return vnicResponse{
		ID:             v.ID,
		CompartmentID:  h.compartmentOf(v.ID),
		SubnetID:       v.SubnetID,
		DisplayName:    v.Name,
		HostnameLabel:  v.Hostname,
		PrivateIP:      v.PrivateIP,
		PublicIP:       v.PublicIP,
		MacAddress:     v.MacAddress,
		IsPrimary:      v.IsPrimary,
		NSGIDs:         nsgIDs,
		LifecycleState: v.State,
		TimeCreated:    h.extras.Created(v.ID),
	}
}

// Private IPs.

func (h *Handler) privateIPOps() crud {
	return crud{
		create: h.createPrivateIP,
		list:   h.listPrivateIPs,
		get:    h.getPrivateIP,
		update: h.updatePrivateIP,
		remove: h.deletePrivateIP,
	}
}

func (h *Handler) createPrivateIP(w http.ResponseWriter, r *http.Request) {
	var req privateIPRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.VNICID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "vnicId is required")
		return
	}

	info, err := h.extras.CreatePrivateIP(r.Context(), req.VNICID, req.IPAddress,
		derefString(req.DisplayName), derefString(req.HostnameLabel))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toPrivateIPResponse(info))
}

// listPrivateIPs answers OCI's ListPrivateIps, which filters by VNIC or
// subnet rather than by compartment: the address belongs to whatever
// compartment its subnet does, so one of the two is required and the results
// never span compartments.
func (h *Handler) listPrivateIPs(w http.ResponseWriter, r *http.Request) {
	vnicID := r.URL.Query().Get("vnicId")
	subnetID := r.URL.Query().Get("subnetId")

	if vnicID == "" && subnetID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "vnicId or subnetId is required")
		return
	}

	infos, err := h.extras.DescribePrivateIPs(r.Context(), nil)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	filter := privateIPFilter{vnicID: vnicID, subnetID: subnetID, address: r.URL.Query().Get("ipAddress")}
	out := make([]privateIPResponse, 0, len(infos))

	for i := range infos {
		if filter.matches(&infos[i]) {
			out = append(out, h.toPrivateIPResponse(&infos[i]))
		}
	}

	ocirest.WriteJSON(w, r, http.StatusOK, paginate(w, r, out))
}

// privateIPFilter is the set of narrowing parameters ListPrivateIps accepts.
type privateIPFilter struct {
	vnicID   string
	subnetID string
	address  string
}

// matches reports whether a private IP passes every parameter the caller set.
func (f privateIPFilter) matches(p *vcnprovider.PrivateIP) bool {
	switch {
	case f.vnicID != "" && p.VNICID != f.vnicID:
		return false
	case f.subnetID != "" && p.SubnetID != f.subnetID:
		return false
	case f.address != "" && p.Address != f.address:
		return false
	default:
		return true
	}
}

func (h *Handler) getPrivateIP(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findPrivateIP(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toPrivateIPResponse(info))
}

func (h *Handler) updatePrivateIP(w http.ResponseWriter, r *http.Request, id string) {
	var req privateIPRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	info, err := h.extras.UpdatePrivateIP(r.Context(), id, req.DisplayName, req.HostnameLabel)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toPrivateIPResponse(info))
}

func (h *Handler) deletePrivateIP(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.extras.DeletePrivateIP(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) findPrivateIP(ctx context.Context, id string) (*vcnprovider.PrivateIP, error) {
	infos, err := h.extras.DescribePrivateIPs(ctx, []string{id})
	if err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "privateIp %s not found", id)
	}

	return &infos[0], nil
}

func (h *Handler) toPrivateIPResponse(info *vcnprovider.PrivateIP) privateIPResponse {
	return privateIPResponse{
		ID:            info.ID,
		CompartmentID: h.compartmentOf(info.ID),
		SubnetID:      info.SubnetID,
		VNICID:        info.VNICID,
		IPAddress:     info.Address,
		DisplayName:   info.Name,
		HostnameLabel: info.Hostname,
		IsPrimary:     info.IsPrimary,
		TimeCreated:   h.extras.Created(info.ID),
	}
}

// derefString reads an optional string field, defaulting to empty.
func derefString(v *string) string {
	if v == nil {
		return ""
	}

	return *v
}
