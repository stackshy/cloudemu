package ecs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

func (h *Handler) routeContainerInstances(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "ListContainerInstances":
		h.listContainerInstances(w, r)
	case "DescribeContainerInstances":
		h.describeContainerInstances(w, r)
	case "RegisterContainerInstance":
		h.registerContainerInstance(w, r)
	case "DeregisterContainerInstance":
		h.deregisterContainerInstance(w, r)
	case "UpdateContainerInstancesState":
		h.updateContainerInstancesState(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) registerContainerInstance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster                  string          `json:"cluster"`
		InstanceIdentityDocument string          `json:"instanceIdentityDocument"`
		TotalResources           []wireResource  `json:"totalResources"`
		Attributes               []wireAttribute `json:"attributes"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ci, err := h.ecs.RegisterContainerInstance(r.Context(), driver.RegisterContainerInstanceInput{
		Cluster:                  req.Cluster,
		InstanceIdentityDocument: req.InstanceIdentityDocument,
		TotalResources:           toResources(req.TotalResources),
		Attributes:               toAttributes(req.Attributes),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"containerInstance": instanceToWire(ci)})
}

func (h *Handler) deregisterContainerInstance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster           string `json:"cluster"`
		ContainerInstance string `json:"containerInstance"`
		Force             bool   `json:"force"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ci, err := h.ecs.DeregisterContainerInstance(r.Context(), req.Cluster, req.ContainerInstance, req.Force)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"containerInstance": instanceToWire(ci)})
}

func (h *Handler) updateContainerInstancesState(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster            string   `json:"cluster"`
		ContainerInstances []string `json:"containerInstances"`
		Status             string   `json:"status"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	instances, failures, err := h.ecs.UpdateContainerInstancesState(
		r.Context(), req.Cluster, req.ContainerInstances, req.Status)
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
