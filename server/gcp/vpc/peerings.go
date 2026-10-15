package vpc

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"slices"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/projectctx"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// netPeeringsTag holds a network's peerings[] as JSON. Each side of a GCP
// peering is its own entry on its own network, so the record lives with the
// network and snapshots with it.
const netPeeringsTag = "cloudemu:gcpNetPeerings"

const (
	addPeeringAction    = "addPeering"
	removePeeringAction = "removePeering"
	updatePeeringAction = "updatePeering"

	peeringStateActive   = "ACTIVE"
	peeringStateInactive = "INACTIVE"
)

// networkPeering is one entry of a network's peerings[].
type networkPeering struct {
	Name                           string `json:"name"`
	Network                        string `json:"network"`
	State                          string `json:"state,omitempty"`
	StateDetails                   string `json:"stateDetails,omitempty"`
	AutoCreateRoutes               bool   `json:"autoCreateRoutes"`
	ExchangeSubnetRoutes           bool   `json:"exchangeSubnetRoutes"`
	ExportCustomRoutes             *bool  `json:"exportCustomRoutes,omitempty"`
	ImportCustomRoutes             *bool  `json:"importCustomRoutes,omitempty"`
	ExportSubnetRoutesWithPublicIP *bool  `json:"exportSubnetRoutesWithPublicIp,omitempty"`
	ImportSubnetRoutesWithPublicIP *bool  `json:"importSubnetRoutesWithPublicIp,omitempty"`
	StackType                      string `json:"stackType,omitempty"`
	UpdateStrategy                 string `json:"updateStrategy,omitempty"`
}

// peeringRequest is the body of addPeering, removePeering and updatePeering.
// addPeering takes either networkPeering or the older top-level name and
// peerNetwork.
type peeringRequest struct {
	Name           string          `json:"name"`
	PeerNetwork    string          `json:"peerNetwork"`
	NetworkPeering *networkPeering `json:"networkPeering"`
}

func decodePeerings(tags map[string]string) []networkPeering {
	var out []networkPeering

	if raw := tags[netPeeringsTag]; raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}

	return out
}

func (h *Handler) savePeerings(ctx context.Context, vpcID string, peerings []networkPeering) error {
	if len(peerings) == 0 {
		return h.net.RemoveVPCTags(ctx, vpcID, []string{netPeeringsTag})
	}

	b, err := json.Marshal(peerings)
	if err != nil {
		return err
	}

	return h.net.UpdateVPCTags(ctx, vpcID, map[string]string{netPeeringsTag: string(b)})
}

// routeNetworkAction serves the network peering actions: addPeering,
// removePeering and updatePeering.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeNetworkAction(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	// updatePeering is a PATCH; addPeering and removePeering are POSTs.
	wantMethod := http.MethodPost
	if rp.Action == updatePeeringAction {
		wantMethod = http.MethodPatch
	}

	switch rp.Action {
	case addPeeringAction, removePeeringAction, updatePeeringAction:
		if r.Method != wantMethod {
			gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
			return
		}
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unknown network action "+rp.Action)
		return
	}

	var req peeringRequest

	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	h.peeringMu.Lock()
	defer h.peeringMu.Unlock()

	v, err := findNetByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	peerings, err := h.applyPeering(r.Context(), rp, decodePeerings(v.Tags), &req)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if err := h.savePeerings(r.Context(), v.ID, peerings); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.ops.RecordDone(hostOf(r), rp.Project, gcprest.ScopeGlobal, "",
		resourceNetworks, rp.ResourceName, rp.Action))
}

// applyPeering returns the network's peerings after the action, or the error
// that rejects it.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) applyPeering(ctx context.Context, rp gcprest.ResourcePath, peerings []networkPeering,
	req *peeringRequest,
) ([]networkPeering, error) {
	p := requestedPeering(req)
	if p.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "peering name required")
	}

	idx := slices.IndexFunc(peerings, func(e networkPeering) bool { return e.Name == p.Name })

	if rp.Action == addPeeringAction {
		if err := h.validateNewPeering(ctx, rp, p, peerings); err != nil {
			return nil, err
		}

		return append(peerings, newPeering(p, rp.Project)), nil
	}

	if idx < 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "peering %s not found", p.Name)
	}

	if rp.Action == removePeeringAction {
		return slices.Delete(peerings, idx, idx+1), nil
	}

	mergePeeringUpdate(&peerings[idx], p)

	return peerings, nil
}

// requestedPeering reads the peering a request names, from networkPeering or
// the older top-level name and peerNetwork.
func requestedPeering(req *peeringRequest) *networkPeering {
	p := req.NetworkPeering
	if p == nil {
		p = &networkPeering{Network: req.PeerNetwork}
	}

	if p.Name == "" {
		p.Name = req.Name
	}

	return p
}

// validateNewPeering rejects an addPeering, with 400 as the compute API does,
// whose name is taken, whose peer network is already peered with this one,
// or that peers a network with itself, and with 404 when the peer network is
// missing. Once the peer has the reverse peering, the peering would go ACTIVE,
// so overlapping subnet ranges between the two networks are 400 too.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) validateNewPeering(ctx context.Context, rp gcprest.ResourcePath, p *networkPeering,
	existing []networkPeering,
) error {
	if p.Network == "" {
		return cerrors.New(cerrors.InvalidArgument, "peer network required")
	}

	peerProj, peerName := refProject(p.Network, rp.Project), lastSegment(p.Network)

	if peerName == rp.ResourceName && peerProj == rp.Project {
		return cerrors.New(cerrors.InvalidArgument, "a network cannot peer with itself")
	}

	if err := checkPeeringUnique(existing, p.Name, rp, peerProj, peerName); err != nil {
		return err
	}

	peerCtx := projectctx.WithProject(ctx, peerProj)

	peer, err := findNetByName(peerCtx, h.net, peerName)
	if err != nil {
		return cerrors.Newf(cerrors.NotFound, "network %s not found", p.Network)
	}

	if !peersBack(peer, rp.ResourceName, rp.Project, peerProj) {
		return nil
	}

	self, err := findNetByName(ctx, h.net, rp.ResourceName)
	if err != nil {
		return err
	}

	return h.checkNoOverlap(ctx, peerCtx, self.ID, peer.ID)
}

// checkPeeringUnique rejects a peering name already on the network, or a
// second peering to the same peer network.
//
//nolint:gocritic // rp is a request-scoped value
func checkPeeringUnique(existing []networkPeering, name string, rp gcprest.ResourcePath, peerProj, peerName string) error {
	for i := range existing {
		if existing[i].Name == name {
			return cerrors.Newf(cerrors.InvalidArgument, "There is already a peering %s on network %s", name, rp.ResourceName)
		}

		if lastSegment(existing[i].Network) == peerName && refProject(existing[i].Network, rp.Project) == peerProj {
			return cerrors.Newf(cerrors.InvalidArgument, "Network %s is already peered with %s as %s",
				rp.ResourceName, peerName, existing[i].Name)
		}
	}

	return nil
}

// peersBack reports whether the peer network has a peering to the named
// network.
func peersBack(peer *netdriver.VPCInfo, name, project, peerProj string) bool {
	backs := decodePeerings(peer.Tags)

	for i := range backs {
		if lastSegment(backs[i].Network) == name && refProject(backs[i].Network, peerProj) == project {
			return true
		}
	}

	return false
}

// checkNoOverlap returns InvalidArgument when a subnet range, primary or
// secondary, of one network overlaps one of the other.
func (h *Handler) checkNoOverlap(ctx, peerCtx context.Context, selfID, peerID string) error {
	mine, err := h.networkRanges(ctx, selfID)
	if err != nil {
		return err
	}

	theirs, err := h.networkRanges(peerCtx, peerID)
	if err != nil {
		return err
	}

	for _, a := range mine {
		for _, b := range theirs {
			if a.Contains(b.IP) || b.Contains(a.IP) {
				return cerrors.Newf(cerrors.InvalidArgument,
					"An IP range in the local network (%s) overlaps with an IP range (%s) in the peer network", a, b)
			}
		}
	}

	return nil
}

// networkRanges returns the primary and secondary ranges of a network's
// subnetworks.
func (h *Handler) networkRanges(ctx context.Context, vpcID string) ([]*net.IPNet, error) {
	subnets, err := h.net.DescribeSubnets(ctx, nil)
	if err != nil {
		return nil, err
	}

	var out []*net.IPNet

	add := func(cidr string) {
		if _, n, err := net.ParseCIDR(cidr); err == nil {
			out = append(out, n)
		}
	}

	for i := range subnets {
		if subnets[i].VPCID != vpcID {
			continue
		}

		add(subnets[i].CIDRBlock)

		var secondary []secondaryRange

		if raw := subnets[i].Tags[subnetSecondaryTag]; raw != "" && json.Unmarshal([]byte(raw), &secondary) == nil {
			for _, s := range secondary {
				add(s.IPCIDRRange)
			}
		}
	}

	return out, nil
}

// newPeering stores a peering with the defaults GCP applies: routes are always
// exchanged for subnets, subnet routes with public IPs are exported unless the
// caller opts out, and the stack is IPv4 only.
func newPeering(p *networkPeering, project string) networkPeering {
	out := *p
	out.Network = "projects/" + refProject(p.Network, project) + "/global/networks/" + lastSegment(p.Network)
	out.State, out.StateDetails = "", ""
	out.AutoCreateRoutes = true
	out.ExchangeSubnetRoutes = true

	out.ExportSubnetRoutesWithPublicIP = boolOr(out.ExportSubnetRoutesWithPublicIP, true)
	out.ExportCustomRoutes = boolOr(out.ExportCustomRoutes, false)
	out.ImportCustomRoutes = boolOr(out.ImportCustomRoutes, false)
	out.ImportSubnetRoutesWithPublicIP = boolOr(out.ImportSubnetRoutesWithPublicIP, false)

	if out.StackType == "" {
		out.StackType = defaultStackType
	}

	return out
}

// boolOr returns p, or a pointer to def when p is nil.
func boolOr(p *bool, def bool) *bool {
	if p != nil {
		return p
	}

	return &def
}

// mergePeeringUpdate applies updatePeering: the route exchange flags, stack
// type and update strategy are mutable, and only the fields the request sets
// change.
func mergePeeringUpdate(dst, src *networkPeering) {
	for _, f := range []struct{ dst, src **bool }{
		{&dst.ExportCustomRoutes, &src.ExportCustomRoutes},
		{&dst.ImportCustomRoutes, &src.ImportCustomRoutes},
		{&dst.ExportSubnetRoutesWithPublicIP, &src.ExportSubnetRoutesWithPublicIP},
		{&dst.ImportSubnetRoutesWithPublicIP, &src.ImportSubnetRoutesWithPublicIP},
	} {
		if *f.src != nil {
			*f.dst = *f.src
		}
	}

	if src.StackType != "" {
		dst.StackType = src.StackType
	}

	if src.UpdateStrategy != "" {
		dst.UpdateStrategy = src.UpdateStrategy
	}
}

// peeringsView renders a network's peerings[] with absolute network links and
// the live state: ACTIVE once the peer network has a peering back to this
// one, INACTIVE until then.
func (h *Handler) peeringsView(ctx context.Context, info *netdriver.VPCInfo, project, host string) []networkPeering {
	peerings := decodePeerings(info.Tags)
	name := tagOr(info.Tags, netNameTag, info.ID)

	for i := range peerings {
		p := &peerings[i]
		peerProj := refProject(p.Network, project)
		peerName := lastSegment(p.Network)

		p.Network = gcprest.SelfLink(host, peerProj, gcprest.ScopeGlobal, "", resourceNetworks, peerName)
		p.State, p.StateDetails = peeringStateInactive, "Waiting for peer network to connect."

		peer, err := findNetByName(projectctx.WithProject(ctx, peerProj), h.net, peerName)
		if err != nil {
			continue
		}

		if peersBack(peer, name, project, peerProj) {
			p.State, p.StateDetails = peeringStateActive, "Connected."
		}
	}

	return peerings
}
