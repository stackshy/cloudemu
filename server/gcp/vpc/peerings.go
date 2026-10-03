package vpc

import (
	"context"
	"encoding/json"
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
	ExportCustomRoutes             bool   `json:"exportCustomRoutes"`
	ImportCustomRoutes             bool   `json:"importCustomRoutes"`
	ExportSubnetRoutesWithPublicIP *bool  `json:"exportSubnetRoutesWithPublicIp,omitempty"`
	ImportSubnetRoutesWithPublicIP bool   `json:"importSubnetRoutesWithPublicIp"`
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

	v, err := findNetByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	var req peeringRequest

	if !gcprest.DecodeJSON(w, r, &req) {
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
		if err := h.validateNewPeering(ctx, rp, p, idx >= 0); err != nil {
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

// validateNewPeering rejects an addPeering whose name is taken, whose peer
// network is missing, or that peers a network with itself.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) validateNewPeering(ctx context.Context, rp gcprest.ResourcePath, p *networkPeering, exists bool) error {
	switch {
	case exists:
		return cerrors.Newf(cerrors.AlreadyExists, "peering %s already exists", p.Name)
	case p.Network == "":
		return cerrors.New(cerrors.InvalidArgument, "peer network required")
	case lastSegment(p.Network) == rp.ResourceName && refProject(p.Network, rp.Project) == rp.Project:
		return cerrors.New(cerrors.InvalidArgument, "a network cannot peer with itself")
	}

	if _, err := findNetByName(projectctx.WithProject(ctx, projectctx.FromPath(p.Network)),
		h.net, lastSegment(p.Network)); err != nil {
		return cerrors.Newf(cerrors.NotFound, "network %s not found", p.Network)
	}

	return nil
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

	if out.ExportSubnetRoutesWithPublicIP == nil {
		v := true
		out.ExportSubnetRoutesWithPublicIP = &v
	}

	if out.StackType == "" {
		out.StackType = defaultStackType
	}

	return out
}

// mergePeeringUpdate applies updatePeering: the route exchange flags, stack
// type and update strategy are mutable.
func mergePeeringUpdate(dst, src *networkPeering) {
	dst.ExportCustomRoutes = src.ExportCustomRoutes
	dst.ImportCustomRoutes = src.ImportCustomRoutes
	dst.ImportSubnetRoutesWithPublicIP = src.ImportSubnetRoutesWithPublicIP

	if src.ExportSubnetRoutesWithPublicIP != nil {
		dst.ExportSubnetRoutesWithPublicIP = src.ExportSubnetRoutesWithPublicIP
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

		for _, back := range decodePeerings(peer.Tags) {
			if lastSegment(back.Network) == name && refProject(back.Network, peerProj) == project {
				p.State, p.StateDetails = peeringStateActive, "Connected."
				break
			}
		}
	}

	return peerings
}
