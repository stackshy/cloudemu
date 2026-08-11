package kafka

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// listClusterOperations handles GET /v1/clusters/{arn}/operations and
// GET /api/v2/clusters/{arn}/operations, rendering the v1 or v2 wire shape.
func (h *Handler) listClusterOperations(w http.ResponseWriter, r *http.Request, arn string, v2 bool) {
	if v2 {
		h.listClusterOperationsV2(w, r, arn)

		return
	}

	list, next, err := h.k.ListClusterOperations(r.Context(), arn, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	ops := make([]map[string]any, 0, len(list))
	for i := range list {
		ops = append(ops, operationToWire(list[i]))
	}

	writeJSON(w, withNext(map[string]any{"clusterOperationInfoList": ops}, next))
}

func (h *Handler) listClusterOperationsV2(w http.ResponseWriter, r *http.Request, arn string) {
	clusterType := h.clusterTypeOf(r, arn)

	list, next, err := h.k.ListClusterOperationsV2(r.Context(), arn, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	ops := make([]map[string]any, 0, len(list))
	for i := range list {
		ops = append(ops, operationToWireV2(list[i], clusterType))
	}

	writeJSON(w, withNext(map[string]any{"clusterOperationInfoList": ops}, next))
}

// routeOperations handles /v1/operations/{arn} and /api/v2/operations/{arn}.
func (h *Handler) routeOperations(w http.ResponseWriter, r *http.Request, rest []string, v2 bool) {
	if len(rest) != 1 || r.Method != http.MethodGet {
		notFoundPath(w, r.URL.Path)

		return
	}

	if v2 {
		op, err := h.k.DescribeClusterOperationV2(r.Context(), rest[0])
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, map[string]any{
			"clusterOperationInfo": operationToWireV2(*op, h.clusterTypeOf(r, op.ClusterARN)),
		})

		return
	}

	op, err := h.k.DescribeClusterOperation(r.Context(), rest[0])
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"clusterOperationInfo": operationToWire(*op)})
}

// clusterTypeOf resolves a cluster's type for v2 operation rendering, defaulting
// to PROVISIONED when the cluster cannot be resolved.
func (h *Handler) clusterTypeOf(r *http.Request, arn string) string {
	c, err := h.k.DescribeCluster(r.Context(), arn)
	if err != nil || c.ClusterType == "" {
		return driver.ClusterTypeProvisioned
	}

	return c.ClusterType
}
