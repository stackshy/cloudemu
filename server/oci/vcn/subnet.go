package vcn

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) subnetOps() crud {
	return crud{
		create: h.createSubnet,
		list:   h.listSubnets,
		get:    h.getSubnet,
		update: h.updateSubnet,
		remove: h.deleteSubnet,
	}
}

func (h *Handler) createSubnet(w http.ResponseWriter, r *http.Request) {
	var req subnetRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	info, err := h.net.CreateSubnet(r.Context(), netdriver.SubnetConfig{
		VPCID:            req.VCNID,
		CIDRBlock:        req.CIDRBlock,
		AvailabilityZone: req.AvailabilityDomain,
		Tags:             subnetTags(nil, &req),
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.place(info.ID, req.CompartmentID)

	if err := h.attachRouteTable(r.Context(), info, req.RouteTableID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toSubnetResponse(r.Context(), info))
}

func (h *Handler) listSubnets(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := h.net.DescribeSubnets(r.Context(), nil)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	scopedList(h, w, r, compartmentID, infos,
		func(v *netdriver.SubnetInfo) (string, string) { return v.ID, v.VPCID },
		h.subnetRenderer(r.Context()))
}

func (h *Handler) getSubnet(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findSubnet(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toSubnetResponse(r.Context(), info))
}

func (h *Handler) updateSubnet(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findSubnet(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	var req subnetRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	tags := subnetTags(info.Tags, &req)

	if err := h.extras.SetTags(id, tags); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	info.Tags = tags

	if err := h.attachRouteTable(r.Context(), info, req.RouteTableID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toSubnetResponse(r.Context(), info))
}

func (h *Handler) deleteSubnet(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.net.DeleteSubnet(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// subnetTags folds the subnet's OCI-only attributes into its tag map. On
// create existing is nil, so every attribute comes from the request.
func subnetTags(existing map[string]string, req *subnetRequest) map[string]string {
	prohibit := tagOr(existing, tagProhibitPublicIP, "")
	if req.ProhibitPublicIPOnVnic != nil {
		prohibit = boolText(*req.ProhibitPublicIPOnVnic)
	}

	return updatedTags(existing, req.FreeformTags,
		tagDisplayName, req.DisplayName,
		tagDNSLabel, req.DNSLabel,
		tagSecurityLists, strings.Join(req.SecurityListIDs, ","),
		tagDHCPOptions, req.DHCPOptionsID,
		tagProhibitPublicIP, prohibit,
	)
}

// attachRouteTable points a subnet at a route table, which OCI models as a
// field on the subnet and the portable driver as an association.
func (h *Handler) attachRouteTable(ctx context.Context, info *netdriver.SubnetInfo, routeTableID string) error {
	if routeTableID == "" {
		return nil
	}

	_, err := h.net.AssociateRouteTable(ctx, routeTableID, info.ID)

	return err
}

// routeTableOf returns the route table a subnet is attached to, falling back
// to its VCN's default table.
func (h *Handler) routeTableOf(ctx context.Context, info *netdriver.SubnetInfo) string {
	tables, err := h.net.DescribeRouteTables(ctx, nil)
	if err == nil {
		for i := range tables {
			for _, assoc := range tables[i].Associations {
				if assoc.SubnetID == info.ID {
					return tables[i].ID
				}
			}
		}
	}

	return h.extras.Defaults(info.VPCID).RouteTableID
}

// findSubnet reads one subnet, reporting OCI's not-found for an unknown OCID.
func (h *Handler) findSubnet(ctx context.Context, id string) (*netdriver.SubnetInfo, error) {
	infos, err := h.net.DescribeSubnets(ctx, []string{id})
	if err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "subnet %s not found", id)
	}

	return &infos[0], nil
}

func (h *Handler) toSubnetResponse(ctx context.Context, info *netdriver.SubnetInfo) subnetResponse {
	label := tagOr(info.Tags, tagDNSLabel, "")
	defaults := h.extras.Defaults(info.VPCID)

	securityLists := defaults.SecurityListID
	if listed := tagOr(info.Tags, tagSecurityLists, ""); listed != "" {
		securityLists = listed
	}

	return subnetResponse{
		ID:                     info.ID,
		CompartmentID:          h.compartmentOf(info.ID),
		VCNID:                  info.VPCID,
		CIDRBlock:              info.CIDRBlock,
		DisplayName:            tagOr(info.Tags, tagDisplayName, ""),
		DNSLabel:               label,
		SubnetDomainName:       domainName(label, "subnet.oraclevcn.com"),
		AvailabilityDomain:     info.AvailabilityZone,
		RouteTableID:           h.routeTableOf(ctx, info),
		SecurityListIDs:        strings.Split(securityLists, ","),
		DHCPOptionsID:          tagOr(info.Tags, tagDHCPOptions, defaults.DHCPOptionsID),
		ProhibitPublicIPOnVnic: boolTag(info.Tags, tagProhibitPublicIP),
		VirtualRouterIP:        firstHostIP(info.CIDRBlock),
		LifecycleState:         info.State,
		TimeCreated:            h.extras.Created(info.ID),
		FreeformTags:           freeformOf(info.Tags),
		DefinedTags:            definedTags{},
	}
}

// subnetRenderer binds the request context to the subnet projection, which has
// to look a subnet's route table up as it goes.
func (h *Handler) subnetRenderer(ctx context.Context) func(*netdriver.SubnetInfo) subnetResponse {
	return func(info *netdriver.SubnetInfo) subnetResponse {
		return h.toSubnetResponse(ctx, info)
	}
}
