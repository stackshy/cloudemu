package networkfirewall

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

type vpcEndpointAssociationJSON struct {
	VpcEndpointAssociationArn string         `json:"VpcEndpointAssociationArn"`
	VpcEndpointAssociationID  string         `json:"VpcEndpointAssociationId"`
	FirewallArn               string         `json:"FirewallArn"`
	VpcID                     string         `json:"VpcId"`
	SubnetMapping             *subnetMapping `json:"SubnetMapping,omitempty"`
	Description               string         `json:"Description,omitempty"`
	Tags                      []tag          `json:"Tags,omitempty"`
}

type associationStatusJSON struct {
	Status               string                   `json:"Status"`
	AssociationSyncState map[string]syncStateJSON `json:"AssociationSyncState,omitempty"`
}

func toAssociationJSON(a *nfdriver.VPCEndpointAssociation) vpcEndpointAssociationJSON {
	return vpcEndpointAssociationJSON{
		VpcEndpointAssociationArn: a.ARN, VpcEndpointAssociationID: a.ID, FirewallArn: a.FirewallARN, VpcID: a.VPCID,
		SubnetMapping: &subnetMapping{SubnetID: a.SubnetID, IPAddressType: a.IPAddressType},
		Description:   a.Description, Tags: mapToTags(a.Tags),
	}
}

func toAssociationStatusJSON(a *nfdriver.VPCEndpointAssociation) associationStatusJSON {
	return associationStatusJSON{
		Status: a.Status,
		AssociationSyncState: map[string]syncStateJSON{
			a.AvailabilityZone: {Attachment: attachmentJSON{SubnetID: a.SubnetID, EndpointID: "vpce-" + a.ID, Status: "READY"}},
		},
	}
}

func writeAssociation(w http.ResponseWriter, a *nfdriver.VPCEndpointAssociation) {
	wire.WriteJSON(w, map[string]any{
		"VpcEndpointAssociation":       toAssociationJSON(a),
		"VpcEndpointAssociationStatus": toAssociationStatusJSON(a),
	})
}

type createAssociationRequest struct {
	FirewallArn   string         `json:"FirewallArn"`
	VpcID         string         `json:"VpcId"`
	SubnetMapping *subnetMapping `json:"SubnetMapping"`
	Description   string         `json:"Description"`
	Tags          []tag          `json:"Tags"`
}

func (h *Handler) createVpcEndpointAssociation(w http.ResponseWriter, r *http.Request) {
	var req createAssociationRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	assoc, ok := capability[nfdriver.VPCEndpointAssociations](h, w)
	if !ok {
		return
	}

	cfg := nfdriver.CreateVPCEndpointAssociationConfig{
		FirewallARN: req.FirewallArn, VPCID: req.VpcID, Description: req.Description, Tags: tagsToMap(req.Tags),
	}
	if req.SubnetMapping != nil {
		cfg.SubnetID, cfg.IPAddressType = req.SubnetMapping.SubnetID, req.SubnetMapping.IPAddressType
	}

	a, err := assoc.CreateVpcEndpointAssociation(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeAssociation(w, a)
}

type associationArnRequest struct {
	VpcEndpointAssociationArn string `json:"VpcEndpointAssociationArn"`
}

func (h *Handler) describeVpcEndpointAssociation(w http.ResponseWriter, r *http.Request) {
	var req associationArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	assoc, ok := capability[nfdriver.VPCEndpointAssociations](h, w)
	if !ok {
		return
	}

	a, err := assoc.DescribeVpcEndpointAssociation(r.Context(), req.VpcEndpointAssociationArn)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeAssociation(w, a)
}

func (h *Handler) deleteVpcEndpointAssociation(w http.ResponseWriter, r *http.Request) {
	var req associationArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	assoc, ok := capability[nfdriver.VPCEndpointAssociations](h, w)
	if !ok {
		return
	}

	a, err := assoc.DeleteVpcEndpointAssociation(r.Context(), req.VpcEndpointAssociationArn)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeAssociation(w, a)
}

type listAssociationsRequest struct {
	listRequest
	FirewallArn string `json:"FirewallArn"`
}

func (h *Handler) listVpcEndpointAssociations(w http.ResponseWriter, r *http.Request) {
	var req listAssociationsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	assoc, ok := capability[nfdriver.VPCEndpointAssociations](h, w)
	if !ok {
		return
	}

	items, next, err := assoc.ListVpcEndpointAssociations(r.Context(), req.FirewallArn, req.options())
	if err != nil {
		writeErr(w, err)
		return
	}

	metas := make([]map[string]string, 0, len(items))
	for i := range items {
		metas = append(metas, map[string]string{"VpcEndpointAssociationArn": items[i].ARN})
	}

	wire.WriteJSON(w, withNextToken(map[string]any{"VpcEndpointAssociations": metas}, next))
}
