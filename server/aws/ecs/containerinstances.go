package ecs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

func (h *Handler) routeContainerInstances(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "ListContainerInstances":
		h.listContainerInstances(w, r)
	case "DescribeContainerInstances":
		h.describeContainerInstances(w, r)
	default:
		return false
	}

	return true
}

//nolint:dupl // decode-cluster/list-arns and decode-ids/describe shapes recur per resource family; typing them apart is intentional.
func (h *Handler) listContainerInstances(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string `json:"cluster"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	instances, err := h.ecs.ListContainerInstances(r.Context(), req.Cluster)
	if err != nil {
		writeErr(w, err)

		return
	}

	arns := make([]string, 0, len(instances))
	for i := range instances {
		arns = append(arns, instances[i].ARN)
	}

	wire.WriteJSON(w, map[string]any{"containerInstanceArns": arns})
}

func (h *Handler) describeContainerInstances(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ContainerInstances []string `json:"containerInstances"`
		Cluster            string   `json:"cluster"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	instances, failures, err := h.ecs.DescribeContainerInstances(r.Context(), req.Cluster, req.ContainerInstances)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]wireContainerInstance, 0, len(instances))
	for i := range instances {
		out = append(out, instanceToWire(&instances[i]))
	}

	wire.WriteJSON(w, map[string]any{"containerInstances": out, "failures": fromFailures(failures)})
}
