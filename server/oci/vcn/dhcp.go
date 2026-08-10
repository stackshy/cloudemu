package vcn

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	vcnprovider "github.com/stackshy/cloudemu/v2/providers/oci/vcn"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// DHCP option discriminators.
const (
	optionDomainNameServer = "DomainNameServer"
	optionSearchDomain     = "SearchDomain"
)

func (h *Handler) dhcpOps() crud {
	return crud{
		create: h.createDHCPOptions,
		list:   h.listDHCPOptions,
		get:    h.getDHCPOptions,
		update: h.updateDHCPOptions,
		remove: h.deleteDHCPOptions,
	}
}

func (h *Handler) createDHCPOptions(w http.ResponseWriter, r *http.Request) {
	var req dhcpRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	serverType, customDNS, searchDomains := splitOptions(req.Options)

	info, err := h.extras.CreateDHCPOptions(r.Context(), req.VCNID, req.DisplayName, serverType, customDNS, searchDomains)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.place(info.ID, req.CompartmentID)

	ocirest.WriteJSON(w, r, http.StatusOK, h.toDHCPResponse(info))
}

func (h *Handler) listDHCPOptions(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := h.extras.DescribeDHCPOptions(r.Context(), nil)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	scopedList(h, w, r, compartmentID, infos,
		func(v *vcnprovider.DHCPOptions) (string, string) { return v.ID, v.VCNID },
		h.toDHCPResponse)
}

func (h *Handler) getDHCPOptions(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findDHCPOptions(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toDHCPResponse(info))
}

func (h *Handler) updateDHCPOptions(w http.ResponseWriter, r *http.Request, id string) {
	var req dhcpRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	var name *string

	if req.DisplayName != "" {
		name = &req.DisplayName
	}

	serverType, customDNS, searchDomains := splitOptions(req.Options)

	info, err := h.extras.UpdateDHCPOptions(r.Context(), id, name, serverType, customDNS, searchDomains)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toDHCPResponse(info))
}

func (h *Handler) deleteDHCPOptions(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.extras.DeleteDHCPOptions(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// splitOptions unpacks OCI's discriminated option list.
func splitOptions(options []dhcpOption) (serverType string, customDNS, searchDomains []string) {
	for i := range options {
		switch options[i].Type {
		case optionDomainNameServer:
			serverType = options[i].ServerType
			customDNS = options[i].CustomDNSServers
		case optionSearchDomain:
			searchDomains = options[i].SearchDomainNames
		}
	}

	return serverType, customDNS, searchDomains
}

// findDHCPOptions reads one DHCP options set, reporting OCI's not-found for an
// unknown OCID.
func (h *Handler) findDHCPOptions(ctx context.Context, id string) (*vcnprovider.DHCPOptions, error) {
	infos, err := h.extras.DescribeDHCPOptions(ctx, []string{id})
	if err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "dhcpOptions %s not found", id)
	}

	return &infos[0], nil
}

func (h *Handler) toDHCPResponse(info *vcnprovider.DHCPOptions) dhcpResponse {
	options := []dhcpOption{{
		Type:             optionDomainNameServer,
		ServerType:       info.ServerType,
		CustomDNSServers: info.CustomDNSServer,
	}}

	if len(info.SearchDomains) > 0 {
		options = append(options, dhcpOption{
			Type:              optionSearchDomain,
			SearchDomainNames: info.SearchDomains,
		})
	}

	return dhcpResponse{
		ID:             info.ID,
		CompartmentID:  h.compartmentOf(info.ID),
		VCNID:          info.VCNID,
		DisplayName:    info.Name,
		Options:        options,
		LifecycleState: info.State,
		TimeCreated:    h.extras.Created(info.ID),
		FreeformTags:   map[string]string{},
		DefinedTags:    definedTags{},
	}
}
