package loadbalancer

import (
	"context"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// Private Service Connect (PSC) consumer forwarding rules.
//
// A PSC consumer rule targets either a producer's service attachment (a
// regional rule whose target is a .../serviceAttachments/{name} self-link) or
// a Google APIs bundle (a global rule whose target is "all-apis" or "vpc-sc").
// Such a rule carries no loadBalancingScheme, so the EXTERNAL default applied
// to ordinary rules must not be synthesized for it, and GCP reports the
// connection it established through pscConnectionStatus / pscConnectionId.
const (
	pscTargetAllAPIs = "all-apis"
	pscTargetVPCSC   = "vpc-sc"

	pscServiceAttachmentsSegment = "/serviceAttachments/"

	// pscStatusAccepted is the connection status of a Google APIs bundle
	// endpoint: there is no producer to decide it. A service-attachment
	// endpoint's status comes from the attachment (GCPServiceAttachmentStore).
	pscStatusAccepted = lbdriver.PSCStatusAccepted
)

// isGoogleAPIsBundle reports whether target names a PSC Google APIs bundle.
func isGoogleAPIsBundle(target string) bool {
	return target == pscTargetAllAPIs || target == pscTargetVPCSC
}

// isPSCTarget reports whether a forwarding rule with this target is a PSC
// consumer rule.
func isPSCTarget(target string) bool {
	return isGoogleAPIsBundle(target) || strings.Contains(target, pscServiceAttachmentsSegment)
}

// validatePSCTarget checks the fields GCP constrains on a Private Service
// Connect consumer rule; a rule whose target is not a PSC target passes:
//
//   - a Google APIs bundle (all-apis / vpc-sc) is only valid on a global rule;
//   - the consumer VPC must be named: the endpoint is an internal address in
//     that network, so a PSC rule without `network` is refused;
//   - loadBalancingScheme must be empty — a PSC endpoint is not a load
//     balancer, and GCP refuses any explicit scheme (EXTERNAL, INTERNAL, …).
//
//nolint:gocritic // rp is a request-scoped value
func validatePSCTarget(rp gcprest.ResourcePath, req *forwardingRuleRequest) error {
	if !isPSCTarget(req.Target) {
		return nil
	}

	if isGoogleAPIsBundle(req.Target) && rp.Scope != gcprest.ScopeGlobal {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Invalid value for field 'resource.target': '%s'. A Google APIs bundle target is only valid on a global forwarding rule.",
			req.Target)
	}

	if req.Network == "" {
		return cerrors.New(cerrors.InvalidArgument,
			"Invalid value for field 'resource.network': ''. A network must be specified for a Private Service Connect forwarding rule.")
	}

	if req.LoadBalancingScheme != "" {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Invalid value for field 'resource.loadBalancingScheme': '%s'. "+
				"The load balancing scheme must be empty for a Private Service Connect forwarding rule.",
			req.LoadBalancingScheme)
	}

	return nil
}

// pscInternalIP derives the stable internal (RFC 1918) address a PSC consumer
// rule sent without an IPAddress gets: the endpoint lives in the consumer's
// VPC, so it is never an external 34.x address.
func pscInternalIP(lb *lbdriver.LBInfo) string {
	h := fnvHash("pscip:" + lb.ID + lb.Name)

	const octetMod = 254

	o2 := byte(h%octetMod) + 1
	o3 := byte((h>>8)%octetMod) + 1
	o4 := byte((h>>16)%octetMod) + 1

	return "10." + strconv.Itoa(int(o2)) + "." + strconv.Itoa(int(o3)) + "." + strconv.Itoa(int(o4))
}

// pscConnectionID is the stable pscConnectionId of a PSC consumer rule.
func pscConnectionID(lb *lbdriver.LBInfo) string {
	return strconv.FormatUint(positiveID(fnvHash("psc:"+lb.ID)), 10)
}

// applyPSCFields sets pscConnectionStatus and a stable pscConnectionId on a
// PSC consumer rule's response; other rules are left untouched. A Google APIs
// bundle endpoint is always ACCEPTED; a service-attachment endpoint reports
// the status the attachment gave it (CLOSED once the attachment is gone).
func (h *Handler) applyPSCFields(ctx context.Context, out *forwardingRuleResponse, lb *lbdriver.LBInfo) {
	target := lb.Tags[frTargetTag]
	if !isPSCTarget(target) {
		return
	}

	out.PscConnectionID = pscConnectionID(lb)
	out.PscConnectionStatus = pscStatusAccepted

	store, ok := h.serviceAttachmentStore()
	if !ok {
		return
	}

	if region, name, parsed := attachmentRef(target); parsed {
		out.PscConnectionStatus = store.GCPPSCConnectionStatus(ctx, region, name, out.PscConnectionID)
	}
}

// connectPSCEndpoint records a newly created service-attachment consumer rule
// on its attachment, which decides the connection's status.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) connectPSCEndpoint(ctx context.Context, rp gcprest.ResourcePath, host string,
	req *forwardingRuleRequest, lb *lbdriver.LBInfo,
) error {
	store, ok := h.serviceAttachmentStore()
	if !ok {
		return nil
	}

	region, name, parsed := attachmentRef(req.Target)
	if !parsed {
		return nil
	}

	_, err := store.ConnectGCPServiceAttachment(ctx, region, name, lbdriver.GCPPSCEndpoint{
		Endpoint:        gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, resourceForwardingRules, req.Name),
		PscConnectionID: pscConnectionID(lb),
		ConsumerNetwork: req.Network,
	})

	return err
}

// disconnectPSCEndpoint removes a deleted consumer rule from its attachment.
func (h *Handler) disconnectPSCEndpoint(ctx context.Context, lb *lbdriver.LBInfo) {
	store, ok := h.serviceAttachmentStore()
	if !ok {
		return
	}

	if region, name, parsed := attachmentRef(lb.Tags[frTargetTag]); parsed {
		_ = store.DisconnectGCPServiceAttachment(ctx, region, name, pscConnectionID(lb))
	}
}
