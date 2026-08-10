package opensearch

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// serveVpcEndpoints routes /opensearch/vpcEndpoints and its sub-paths.
func (h *Handler) serveVpcEndpoints(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createVpcEndpoint(w, r)
		case http.MethodGet:
			h.listVpcEndpoints(w, r)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if len(rest) == 1 {
		switch rest[0] {
		case segDescribe:
			h.describeVpcEndpoints(w, r)
		case segUpdate:
			h.updateVpcEndpoint(w, r)
		default:
			h.deleteVpcEndpointByID(w, r, rest[0])
		}

		return
	}

	notFoundPath(w, r.URL.Path)
}

func (h *Handler) deleteVpcEndpointByID(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodDelete {
		methodNotAllowed(w)

		return
	}

	epID, status, err := h.os.DeleteVpcEndpoint(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"VpcEndpointSummary": map[string]any{"VpcEndpointId": epID, "Status": status}})
}

func (h *Handler) createVpcEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainArn   string     `json:"DomainArn"`
		VpcOptions  vpcOptions `json:"VpcOptions"`
		ClientToken string     `json:"ClientToken"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	out, err := h.os.CreateVpcEndpoint(r.Context(), req.DomainArn, req.VpcOptions.toDriver(), req.ClientToken)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"VpcEndpoint": vpcEndpointToWire(out)})
}

func (h *Handler) updateVpcEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		VpcEndpointID string     `json:"VpcEndpointId"`
		VpcOptions    vpcOptions `json:"VpcOptions"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	out, err := h.os.UpdateVpcEndpoint(r.Context(), req.VpcEndpointID, req.VpcOptions.toDriver())
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"VpcEndpoint": vpcEndpointToWire(out)})
}

func (h *Handler) describeVpcEndpoints(w http.ResponseWriter, r *http.Request) {
	var req struct {
		VpcEndpointIDs []string `json:"VpcEndpointIds"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	found, errs, err := h.os.DescribeVpcEndpoints(r.Context(), req.VpcEndpointIDs)
	if err != nil {
		writeErr(w, err)

		return
	}

	eps := make([]map[string]any, 0, len(found))
	for i := range found {
		eps = append(eps, vpcEndpointToWire(&found[i]))
	}

	writeJSON(w, map[string]any{"VpcEndpoints": eps, "VpcEndpointErrors": errs})
}

func (h *Handler) listVpcEndpoints(w http.ResponseWriter, r *http.Request) {
	list, next, err := h.os.ListVpcEndpoints(r.Context(), pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"VpcEndpointSummaryList": list}, next))
}

func (h *Handler) listVpcEndpointsForDomain(w http.ResponseWriter, r *http.Request, domainName string) {
	list, next, err := h.os.ListVpcEndpointsForDomain(r.Context(), domainName, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"VpcEndpointSummaryList": list}, next))
}

func (h *Handler) authorizeVpcEndpointAccess(w http.ResponseWriter, r *http.Request, domainName string) {
	var req struct {
		Account string `json:"Account"`
		Service string `json:"Service"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	out, err := h.os.AuthorizeVpcEndpointAccess(r.Context(), domainName, req.Account, req.Service)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, out)
}

func (h *Handler) revokeVpcEndpointAccess(w http.ResponseWriter, r *http.Request, domainName string) {
	var req struct {
		Account string `json:"Account"`
		Service string `json:"Service"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.os.RevokeVpcEndpointAccess(r.Context(), domainName, req.Account, req.Service); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listVpcEndpointAccess(w http.ResponseWriter, r *http.Request, domainName string) {
	list, next, err := h.os.ListVpcEndpointAccess(r.Context(), domainName, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"AuthorizedPrincipalList": list}, next))
}

// vpcOptions is the wire shape of VPCOptions.
type vpcOptions struct {
	SubnetIDs        []string `json:"SubnetIds"`
	SecurityGroupIDs []string `json:"SecurityGroupIds"`
}

func (v vpcOptions) toDriver() driver.VpcOptions {
	return driver.VpcOptions{SubnetIDs: v.SubnetIDs, SecurityGroupIDs: v.SecurityGroupIDs}
}
