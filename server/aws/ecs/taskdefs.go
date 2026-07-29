package ecs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

func (h *Handler) routeTaskDefs(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "RegisterTaskDefinition":
		h.registerTaskDefinition(w, r)
	case "ListTaskDefinitions":
		h.listTaskDefinitions(w, r)
	case "DescribeTaskDefinition":
		h.describeTaskDefinition(w, r)
	case "DeregisterTaskDefinition":
		h.deregisterTaskDefinition(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) registerTaskDefinition(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Family                  string             `json:"family"`
		ContainerDefinitions    []wireContainerDef `json:"containerDefinitions"`
		CPU                     string             `json:"cpu"`
		Memory                  string             `json:"memory"`
		NetworkMode             string             `json:"networkMode"`
		RequiresCompatibilities []string           `json:"requiresCompatibilities"`
		TaskRoleArn             string             `json:"taskRoleArn"`
		ExecutionRoleArn        string             `json:"executionRoleArn"`
		Tags                    []wireTag          `json:"tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	td, err := h.ecs.RegisterTaskDefinition(r.Context(), driver.RegisterTaskDefinitionInput{
		Family:                  req.Family,
		ContainerDefinitions:    toContainerDefs(req.ContainerDefinitions),
		CPU:                     req.CPU,
		Memory:                  req.Memory,
		NetworkMode:             req.NetworkMode,
		RequiresCompatibilities: req.RequiresCompatibilities,
		TaskRoleARN:             req.TaskRoleArn,
		ExecutionRoleARN:        req.ExecutionRoleArn,
		Tags:                    toTags(req.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"taskDefinition": taskDefToWire(td), "tags": fromTags(td.Tags)})
}

func (h *Handler) listTaskDefinitions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FamilyPrefix string `json:"familyPrefix"`
		Status       string `json:"status"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	defs, err := h.ecs.ListTaskDefinitions(r.Context(), req.FamilyPrefix, req.Status)
	if err != nil {
		writeErr(w, err)

		return
	}

	arns := make([]string, 0, len(defs))
	for i := range defs {
		arns = append(arns, defs[i].ARN)
	}

	wire.WriteJSON(w, map[string]any{"taskDefinitionArns": arns})
}

func (h *Handler) describeTaskDefinition(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskDefinition string `json:"taskDefinition"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	td, err := h.ecs.DescribeTaskDefinition(r.Context(), req.TaskDefinition)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"taskDefinition": taskDefToWire(td), "tags": fromTags(td.Tags)})
}

func (h *Handler) deregisterTaskDefinition(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskDefinition string `json:"taskDefinition"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	td, err := h.ecs.DeregisterTaskDefinition(r.Context(), req.TaskDefinition)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"taskDefinition": taskDefToWire(td)})
}
