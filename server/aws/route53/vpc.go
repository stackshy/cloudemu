package route53

import (
	"context"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// defaultOwningAccount is the account id reported as the owner of a private
// hosted zone in ListHostedZonesByVPC — cloudemu's single default account.
const defaultOwningAccount = "123456789012"

// vpcAssociator is the AWS-only extension a Route 53 backend implements to
// associate and disassociate Amazon VPCs with a private hosted zone. The wire
// layer does all the state-dependent validation (zone privacy, last-VPC,
// not-associated) from the zone returned by GetZone, so these mutators just
// add or remove one association idempotently. Backends without it (Azure/GCP
// DNS) don't support the private-hosted-zone VPC association API.
type vpcAssociator interface {
	AssociateVPC(ctx context.Context, zoneID string, vpc dnsdriver.VPCAssociation) error
	DisassociateVPC(ctx context.Context, zoneID string, vpc dnsdriver.VPCAssociation) error
}

// containsVPC reports whether a zone's association list already holds vpc.
func containsVPC(vpcs []dnsdriver.VPCAssociation, vpc dnsdriver.VPCAssociation) bool {
	for _, v := range vpcs {
		if v == vpc {
			return true
		}
	}

	return false
}

// associateVPCWithHostedZone answers AssociateVPCWithHostedZone: it adds a VPC
// to a private hosted zone's association list. Real Route 53 rejects a public
// zone (PublicZoneVPCAssociation) and a missing zone (NoSuchHostedZone), and is
// idempotent for an already-associated VPC.
func (h *Handler) associateVPCWithHostedZone(w http.ResponseWriter, r *http.Request, id string) {
	assoc, ok := h.dns.(vpcAssociator)
	if !ok {
		writeError(w, http.StatusBadRequest, "InvalidInput", "VPC association is not supported")
		return
	}

	var req associateVPCRequest
	if !decodeXML(w, r, &req) {
		return
	}

	zoneID := trimZonePrefix(id)

	zone, err := h.dns.GetZone(r.Context(), zoneID)
	if err != nil {
		writeErr(w, err)
		return
	}

	if !zone.Private {
		writeError(w, http.StatusBadRequest, "PublicZoneVPCAssociation",
			"You're trying to associate a VPC with a public hosted zone. "+
				"Amazon Route 53 doesn't support associating a VPC with a public hosted zone.")

		return
	}

	vpc := dnsdriver.VPCAssociation{VPCID: req.VPC.VPCId, VPCRegion: req.VPC.VPCRegion}

	if err := assoc.AssociateVPC(r.Context(), zoneID, vpc); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteXML(w, http.StatusOK, associateVPCResponse{
		Xmlns:      xmlns,
		ChangeInfo: newChangeInfo(),
	})
}

// disassociateVPCFromHostedZone answers DisassociateVPCFromHostedZone: it removes
// a VPC from a private hosted zone. Real Route 53 rejects removing a VPC that
// isn't associated (VPCAssociationNotFound) and removing the last VPC of a zone
// (LastVPCAssociation).
func (h *Handler) disassociateVPCFromHostedZone(w http.ResponseWriter, r *http.Request, id string) {
	assoc, ok := h.dns.(vpcAssociator)
	if !ok {
		writeError(w, http.StatusBadRequest, "InvalidInput", "VPC association is not supported")
		return
	}

	var req disassociateVPCRequest
	if !decodeXML(w, r, &req) {
		return
	}

	zoneID := trimZonePrefix(id)

	zone, err := h.dns.GetZone(r.Context(), zoneID)
	if err != nil {
		writeErr(w, err)
		return
	}

	vpc := dnsdriver.VPCAssociation{VPCID: req.VPC.VPCId, VPCRegion: req.VPC.VPCRegion}

	if !containsVPC(zone.VPCs, vpc) {
		writeError(w, http.StatusNotFound, "VPCAssociationNotFound",
			"The specified VPC and hosted zone are not currently associated.")

		return
	}

	if len(zone.VPCs) == 1 {
		writeError(w, http.StatusBadRequest, "LastVPCAssociation",
			"The VPC that you're trying to disassociate from the private hosted zone is the last VPC "+
				"that is associated with the hosted zone. Amazon Route 53 doesn't support disassociating "+
				"the last VPC from a hosted zone.")

		return
	}

	if err := assoc.DisassociateVPC(r.Context(), zoneID, vpc); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteXML(w, http.StatusOK, disassociateVPCResponse{
		Xmlns:      xmlns,
		ChangeInfo: newChangeInfo(),
	})
}

// listHostedZonesByVPC answers ListHostedZonesByVPC, returning the private hosted
// zones associated with the requested VPC. vpcid and vpcregion are required.
func (h *Handler) listHostedZonesByVPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	q := r.URL.Query()
	vpcID := q.Get("vpcid")
	vpcRegion := q.Get("vpcregion")

	if vpcID == "" || vpcRegion == "" {
		writeError(w, http.StatusBadRequest, "InvalidInput", "vpcid and vpcregion are required")
		return
	}

	infos, err := h.dns.ListZones(r.Context(), scope.Scope{})
	if err != nil {
		writeErr(w, err)
		return
	}

	want := dnsdriver.VPCAssociation{VPCID: vpcID, VPCRegion: vpcRegion}
	summaries := make([]hostedZoneSummaryXML, 0)

	for i := range infos {
		if !containsVPC(infos[i].VPCs, want) {
			continue
		}

		summaries = append(summaries, hostedZoneSummaryXML{
			HostedZoneId: infos[i].ID,
			Name:         infos[i].Name,
			Owner:        hostedZoneOwnerXML{OwningAccount: defaultOwningAccount},
		})
	}

	maxItems := listMaxItems

	if v := q.Get("maxitems"); v != "" {
		if n, cerr := strconv.Atoi(v); cerr == nil && n > 0 {
			maxItems = n
		}
	}

	wire.WriteXML(w, http.StatusOK, listHostedZonesByVPCResponse{
		Xmlns:               xmlns,
		HostedZoneSummaries: summaries,
		MaxItems:            strconv.Itoa(maxItems),
	})
}
