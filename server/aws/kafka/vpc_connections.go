package kafka

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// routeVpcConnection handles POST /v1/vpc-connection and GET|DELETE
// /v1/vpc-connection/{arn}.
func (h *Handler) routeVpcConnection(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		if r.Method == http.MethodPost {
			h.createVpcConnection(w, r)

			return
		}

		methodNotAllowed(w)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.describeVpcConnection(w, r, rest[0])
	case http.MethodDelete:
		h.deleteVpcConnection(w, r, rest[0])
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createVpcConnection(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	out, err := h.k.CreateVpcConnection(r.Context(), body)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, vpcCreateToWire(out))
}

func (h *Handler) describeVpcConnection(w http.ResponseWriter, r *http.Request, arn string) {
	out, err := h.k.DescribeVpcConnection(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, vpcDescribeToWire(out))
}

func (h *Handler) deleteVpcConnection(w http.ResponseWriter, r *http.Request, arn string) {
	out, err := h.k.DeleteVpcConnection(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"vpcConnectionArn": out.VpcConnectionARN,
		"state":            out.State,
	})
}

// routeVpcConnections handles GET /v1/vpc-connections.
func (h *Handler) routeVpcConnections(w http.ResponseWriter, r *http.Request, _ []string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	list, next, err := h.k.ListVpcConnections(r.Context(), pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	conns := make([]map[string]any, 0, len(list))
	for i := range list {
		conns = append(conns, vpcSummaryToWire(&list[i]))
	}

	writeJSON(w, withNext(map[string]any{"vpcConnections": conns}, next))
}

// listClientVpcConnections handles GET /v1/clusters/{arn}/client-vpc-connections.
func (h *Handler) listClientVpcConnections(w http.ResponseWriter, r *http.Request, arn string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	list, next, err := h.k.ListClientVpcConnections(r.Context(), arn, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	conns := make([]map[string]any, 0, len(list))
	for i := range list {
		conns = append(conns, clientVpcToWire(&list[i]))
	}

	writeJSON(w, withNext(map[string]any{"clientVpcConnections": conns}, next))
}

// rejectClientVpcConnection handles PUT /v1/clusters/{arn}/client-vpc-connection.
func (h *Handler) rejectClientVpcConnection(w http.ResponseWriter, r *http.Request, arn string) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)

		return
	}

	body, ok := readBody(w, r)
	if !ok {
		return
	}

	if err := h.k.RejectClientVpcConnection(r.Context(), arn, body); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

// vpcCreateToWire renders the CreateVpcConnection response, promoting the
// clientSubnets/securityGroups raw options alongside the modeled fields.
func vpcCreateToWire(v *driver.VpcConnection) map[string]any {
	out := map[string]any{
		"vpcConnectionArn": v.VpcConnectionARN,
		"state":            v.State,
		"authentication":   v.Authentication,
		"vpcId":            v.VpcID,
		"creationTime":     timeRFC3339(v.CreationTime),
	}

	if v.Tags != nil {
		out["tags"] = v.Tags
	}

	overlayRaw(out, v.RawOptions, "subnets", "clientSubnets")
	overlayRaw(out, v.RawOptions, "securityGroups", "securityGroups")

	return out
}

// vpcDescribeToWire renders the DescribeVpcConnection response.
func vpcDescribeToWire(v *driver.VpcConnection) map[string]any {
	out := map[string]any{
		"vpcConnectionArn": v.VpcConnectionARN,
		"targetClusterArn": v.TargetClusterARN,
		"state":            v.State,
		"authentication":   v.Authentication,
		"vpcId":            v.VpcID,
		"creationTime":     timeRFC3339(v.CreationTime),
	}

	if v.Tags != nil {
		out["tags"] = v.Tags
	}

	overlayRaw(out, v.RawOptions, "subnets", "subnets")
	overlayRaw(out, v.RawOptions, "securityGroups", "securityGroups")

	return out
}

// vpcSummaryToWire renders a VpcConnection summary (ListVpcConnections item).
func vpcSummaryToWire(v *driver.VpcConnection) map[string]any {
	return map[string]any{
		"vpcConnectionArn": v.VpcConnectionARN,
		"targetClusterArn": v.TargetClusterARN,
		"state":            v.State,
		"authentication":   v.Authentication,
		"vpcId":            v.VpcID,
		"creationTime":     timeRFC3339(v.CreationTime),
	}
}

// clientVpcToWire renders a ClientVpcConnection (ListClientVpcConnections item).
func clientVpcToWire(v *driver.VpcConnection) map[string]any {
	return map[string]any{
		"vpcConnectionArn": v.VpcConnectionARN,
		"state":            v.State,
		"authentication":   v.Authentication,
		"creationTime":     timeRFC3339(v.CreationTime),
	}
}
