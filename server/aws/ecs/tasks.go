package ecs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

func (h *Handler) routeTasks(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "RunTask":
		h.runTask(w, r)
	case "StopTask":
		h.stopTask(w, r)
	case "ListTasks":
		h.listTasks(w, r)
	case "DescribeTasks":
		h.describeTasks(w, r)
	case "ExecuteCommand":
		h.executeCommand(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) executeCommand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster     string `json:"cluster"`
		Task        string `json:"task"`
		Container   string `json:"container"`
		Command     string `json:"command"`
		Interactive bool   `json:"interactive"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	res, err := h.ecs.ExecuteCommand(r.Context(), driver.ExecuteCommandInput{
		Cluster:     req.Cluster,
		Task:        req.Task,
		Container:   req.Container,
		Command:     req.Command,
		Interactive: req.Interactive,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"clusterArn":    res.ClusterARN,
		"containerArn":  res.ContainerARN,
		"containerName": res.ContainerName,
		"interactive":   res.Interactive,
		"taskArn":       res.TaskARN,
		"session": map[string]any{
			"sessionId":  res.Session.SessionID,
			"streamUrl":  res.Session.StreamURL,
			"tokenValue": res.Session.TokenValue,
		},
	})
}

func (h *Handler) runTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskDefinition           string                             `json:"taskDefinition"`
		Cluster                  string                             `json:"cluster"`
		Count                    int                                `json:"count"`
		LaunchType               string                             `json:"launchType"`
		PlatformVersion          string                             `json:"platformVersion"`
		Group                    string                             `json:"group"`
		StartedBy                string                             `json:"startedBy"`
		NetworkConfiguration     *wireNetworkConfiguration          `json:"networkConfiguration"`
		CapacityProviderStrategy []wireCapacityProviderStrategyItem `json:"capacityProviderStrategy"`
		Tags                     []wireTag                          `json:"tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tasks, failures, err := h.ecs.RunTask(r.Context(), driver.RunTaskInput{
		TaskDefinition:           req.TaskDefinition,
		Cluster:                  req.Cluster,
		Count:                    req.Count,
		LaunchType:               req.LaunchType,
		PlatformVersion:          req.PlatformVersion,
		Group:                    req.Group,
		StartedBy:                req.StartedBy,
		NetworkConfiguration:     toNetworkConfiguration(req.NetworkConfiguration),
		CapacityProviderStrategy: toCapacityProviderStrategy(req.CapacityProviderStrategy),
		Tags:                     toTags(req.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	h.writeTasks(w, tasks, failures)
}

func (h *Handler) stopTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Task    string `json:"task"`
		Cluster string `json:"cluster"`
		Reason  string `json:"reason"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	t, err := h.ecs.StopTask(r.Context(), req.Cluster, req.Task, req.Reason)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"task": taskToWire(t)})
}

func (h *Handler) listTasks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster       string `json:"cluster"`
		Family        string `json:"family"`
		DesiredStatus string `json:"desiredStatus"`
		ServiceName   string `json:"serviceName"`
		MaxResults    int    `json:"maxResults"`
		NextToken     string `json:"nextToken"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tasks, err := h.ecs.ListTasks(r.Context(), req.Cluster, req.Family, req.DesiredStatus, req.ServiceName)
	if err != nil {
		writeErr(w, err)

		return
	}

	arns := make([]string, 0, len(tasks))
	for i := range tasks {
		arns = append(arns, tasks[i].ARN)
	}

	items, next, ok := paginateARNs(w, arns, req.MaxResults, req.NextToken)
	if !ok {
		return
	}

	wire.WriteJSON(w, listResponse("taskArns", items, next))
}

func (h *Handler) describeTasks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tasks   []string `json:"tasks"`
		Cluster string   `json:"cluster"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tasks, failures, err := h.ecs.DescribeTasks(r.Context(), req.Cluster, req.Tasks)
	if err != nil {
		writeErr(w, err)

		return
	}

	h.writeTasks(w, tasks, failures)
}

func (*Handler) writeTasks(w http.ResponseWriter, tasks []driver.Task, failures []driver.Failure) {
	out := make([]wireTask, 0, len(tasks))
	for i := range tasks {
		out = append(out, taskToWire(&tasks[i]))
	}

	wire.WriteJSON(w, map[string]any{"tasks": out, "failures": fromFailures(failures)})
}
