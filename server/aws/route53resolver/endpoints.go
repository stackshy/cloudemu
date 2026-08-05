package route53resolver

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

func (h *Handler) createResolverEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CreatorRequestID          string                 `json:"CreatorRequestId"`
		Name                      string                 `json:"Name"`
		Direction                 string                 `json:"Direction"`
		IPAddresses               []wireIPAddressRequest `json:"IpAddresses"`
		SecurityGroupIDs          []string               `json:"SecurityGroupIds"`
		ResolverEndpointType      string                 `json:"ResolverEndpointType"`
		Protocols                 []string               `json:"Protocols"`
		OutpostArn                string                 `json:"OutpostArn"`
		PreferredInstanceType     string                 `json:"PreferredInstanceType"`
		DNS64Enabled              bool                   `json:"Dns64Enabled"`
		IPv6InternetAccessEnabled bool                   `json:"Ipv6InternetAccessEnabled"`
		Tags                      []wireTag              `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ep, err := h.r53r.CreateResolverEndpoint(r.Context(), &driver.CreateResolverEndpointInput{
		CreatorRequestID:          req.CreatorRequestID,
		Name:                      req.Name,
		Direction:                 req.Direction,
		IPAddresses:               toDriverIPAddresses(req.IPAddresses),
		SecurityGroupIDs:          req.SecurityGroupIDs,
		ResolverEndpointType:      req.ResolverEndpointType,
		Protocols:                 req.Protocols,
		OutpostARN:                req.OutpostArn,
		PreferredInstanceType:     req.PreferredInstanceType,
		DNS64Enabled:              req.DNS64Enabled,
		IPv6InternetAccessEnabled: req.IPv6InternetAccessEnabled,
		Tags:                      toDriverTags(req.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverEndpoint": endpointToWire(ep)})
}

func (h *Handler) getResolverEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResolverEndpointID string `json:"ResolverEndpointId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ep, err := h.r53r.GetResolverEndpoint(r.Context(), req.ResolverEndpointID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverEndpoint": endpointToWire(ep)})
}

func (h *Handler) updateResolverEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResolverEndpointID   string   `json:"ResolverEndpointId"`
		Name                 string   `json:"Name"`
		ResolverEndpointType string   `json:"ResolverEndpointType"`
		Protocols            []string `json:"Protocols"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ep, err := h.r53r.UpdateResolverEndpoint(r.Context(), req.ResolverEndpointID, driver.UpdateResolverEndpointInput{
		Name:                 req.Name,
		ResolverEndpointType: req.ResolverEndpointType,
		Protocols:            req.Protocols,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverEndpoint": endpointToWire(ep)})
}

func (h *Handler) deleteResolverEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResolverEndpointID string `json:"ResolverEndpointId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ep, err := h.r53r.DeleteResolverEndpoint(r.Context(), req.ResolverEndpointID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverEndpoint": endpointToWire(ep)})
}

func (h *Handler) listResolverEndpoints(w http.ResponseWriter, r *http.Request) {
	eps, err := h.r53r.ListResolverEndpoints(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]wireResolverEndpoint, 0, len(eps))
	for i := range eps {
		out = append(out, endpointToWire(&eps[i]))
	}

	wire.WriteJSON(w, map[string]any{"ResolverEndpoints": out})
}

func (h *Handler) associateResolverEndpointIP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResolverEndpointID string              `json:"ResolverEndpointId"`
		IPAddress          wireIPAddressUpdate `json:"IpAddress"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ep, err := h.r53r.AssociateResolverEndpointIPAddress(r.Context(), req.ResolverEndpointID, &driver.IPAddress{
		SubnetID: req.IPAddress.SubnetID,
		IP:       req.IPAddress.IP,
		IPv6:     req.IPAddress.IPv6,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverEndpoint": endpointToWire(ep)})
}

func (h *Handler) disassociateResolverEndpointIP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResolverEndpointID string              `json:"ResolverEndpointId"`
		IPAddress          wireIPAddressUpdate `json:"IpAddress"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ep, err := h.r53r.DisassociateResolverEndpointIPAddress(r.Context(), req.ResolverEndpointID, &driver.IPAddress{
		IPID:     req.IPAddress.IPID,
		SubnetID: req.IPAddress.SubnetID,
		IP:       req.IPAddress.IP,
		IPv6:     req.IPAddress.IPv6,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverEndpoint": endpointToWire(ep)})
}

func (h *Handler) listResolverEndpointIPs(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResolverEndpointID string `json:"ResolverEndpointId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ips, err := h.r53r.ListResolverEndpointIPAddresses(r.Context(), req.ResolverEndpointID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"IpAddresses": ipsToWire(ips)})
}
