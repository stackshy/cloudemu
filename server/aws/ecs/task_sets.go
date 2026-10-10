package ecs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

type wireScale struct {
	Unit  string  `json:"unit,omitempty"`
	Value float64 `json:"value"`
}

type wireTaskSet struct {
	ID                       string                             `json:"id"`
	TaskSetArn               string                             `json:"taskSetArn"`
	ServiceArn               string                             `json:"serviceArn"`
	ClusterArn               string                             `json:"clusterArn"`
	ExternalID               string                             `json:"externalId,omitempty"`
	StartedBy                string                             `json:"startedBy,omitempty"`
	Status                   string                             `json:"status"`
	TaskDefinition           string                             `json:"taskDefinition"`
	ComputedDesiredCount     int                                `json:"computedDesiredCount"`
	PendingCount             int                                `json:"pendingCount"`
	RunningCount             int                                `json:"runningCount"`
	CreatedAt                float64                            `json:"createdAt,omitempty"`
	UpdatedAt                float64                            `json:"updatedAt,omitempty"`
	LaunchType               string                             `json:"launchType,omitempty"`
	PlatformVersion          string                             `json:"platformVersion,omitempty"`
	PlatformFamily           string                             `json:"platformFamily,omitempty"`
	CapacityProviderStrategy []wireCapacityProviderStrategyItem `json:"capacityProviderStrategy,omitempty"`
	NetworkConfiguration     *wireNetworkConfiguration          `json:"networkConfiguration,omitempty"`
	LoadBalancers            []wireLoadBalancer                 `json:"loadBalancers,omitempty"`
	ServiceRegistries        []wireServiceRegistry              `json:"serviceRegistries,omitempty"`
	Scale                    wireScale                          `json:"scale"`
	StabilityStatus          string                             `json:"stabilityStatus,omitempty"`
	StabilityStatusAt        float64                            `json:"stabilityStatusAt,omitempty"`
	Tags                     []wireTag                          `json:"tags,omitempty"`
}

func taskSetToWire(ts *driver.TaskSet) wireTaskSet {
	return wireTaskSet{
		ID: ts.ID, TaskSetArn: ts.ARN, ServiceArn: ts.ServiceARN, ClusterArn: ts.ClusterARN,
		ExternalID: ts.ExternalID, StartedBy: ts.StartedBy, Status: ts.Status, TaskDefinition: ts.TaskDefinition,
		ComputedDesiredCount: ts.ComputedDesiredCount, PendingCount: ts.PendingCount, RunningCount: ts.RunningCount,
		CreatedAt: epoch(ts.CreatedAt), UpdatedAt: epoch(ts.UpdatedAt), LaunchType: ts.LaunchType,
		PlatformVersion: ts.PlatformVersion, PlatformFamily: ts.PlatformFamily,
		CapacityProviderStrategy: fromCapacityProviderStrategy(ts.CapacityProviderStrategy),
		NetworkConfiguration:     fromNetworkConfiguration(ts.NetworkConfiguration),
		LoadBalancers:            fromLoadBalancers(ts.LoadBalancers),
		ServiceRegistries:        fromServiceRegistries(ts.ServiceRegistries),
		Scale:                    wireScale{Unit: ts.Scale.Unit, Value: ts.Scale.Value},
		StabilityStatus:          ts.StabilityStatus, StabilityStatusAt: epoch(ts.StabilityStatusAt),
		Tags: fromTags(ts.Tags),
	}
}

func fromTaskSets(in []driver.TaskSet, withTags bool) []wireTaskSet {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireTaskSet, 0, len(in))

	for i := range in {
		ts := taskSetToWire(&in[i])
		if !withTags {
			ts.Tags = nil
		}

		out = append(out, ts)
	}

	return out
}

func (h *Handler) routeTaskSets(w http.ResponseWriter, r *http.Request, op string) bool {
	handlers := map[string]func(http.ResponseWriter, *http.Request, driver.TaskSets){
		"CreateTaskSet":               createTaskSet,
		"UpdateTaskSet":               updateTaskSet,
		"DeleteTaskSet":               deleteTaskSet,
		"DescribeTaskSets":            describeTaskSets,
		"UpdateServicePrimaryTaskSet": updatePrimaryTaskSet,
	}

	handle, known := handlers[op]
	if !known {
		return false
	}

	if ts, ok := capabilityOf[driver.TaskSets](h.ecs, w, op); ok {
		handle(w, r, ts)
	}

	return true
}

func createTaskSet(w http.ResponseWriter, r *http.Request, ts driver.TaskSets) {
	var req struct {
		Cluster                  string                             `json:"cluster"`
		Service                  string                             `json:"service"`
		TaskDefinition           string                             `json:"taskDefinition"`
		ExternalID               string                             `json:"externalId"`
		LaunchType               string                             `json:"launchType"`
		PlatformVersion          string                             `json:"platformVersion"`
		CapacityProviderStrategy []wireCapacityProviderStrategyItem `json:"capacityProviderStrategy"`
		NetworkConfiguration     *wireNetworkConfiguration          `json:"networkConfiguration"`
		LoadBalancers            []wireLoadBalancer                 `json:"loadBalancers"`
		ServiceRegistries        []wireServiceRegistry              `json:"serviceRegistries"`
		Scale                    *wireScale                         `json:"scale"`
		ClientToken              string                             `json:"clientToken"`
		Tags                     []wireTag                          `json:"tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	in := driver.CreateTaskSetInput{
		Cluster: req.Cluster, Service: req.Service, TaskDefinition: req.TaskDefinition, ExternalID: req.ExternalID,
		LaunchType: req.LaunchType, PlatformVersion: req.PlatformVersion,
		CapacityProviderStrategy: toCapacityProviderStrategy(req.CapacityProviderStrategy),
		NetworkConfiguration:     toNetworkConfiguration(req.NetworkConfiguration),
		LoadBalancers:            toLoadBalancers(req.LoadBalancers), ServiceRegistries: toServiceRegistries(req.ServiceRegistries),
		ClientToken: req.ClientToken, Tags: toTags(req.Tags),
	}

	if req.Scale != nil {
		in.Scale = &driver.Scale{Unit: req.Scale.Unit, Value: req.Scale.Value}
	}

	out, err := ts.CreateTaskSet(r.Context(), in)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{keyTaskSet: taskSetToWire(out)})
}

func updateTaskSet(w http.ResponseWriter, r *http.Request, ts driver.TaskSets) {
	var req struct {
		Cluster string    `json:"cluster"`
		Service string    `json:"service"`
		TaskSet string    `json:"taskSet"`
		Scale   wireScale `json:"scale"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	out, err := ts.UpdateTaskSet(r.Context(), driver.UpdateTaskSetInput{
		Cluster: req.Cluster, Service: req.Service, TaskSet: req.TaskSet,
		Scale: driver.Scale{Unit: req.Scale.Unit, Value: req.Scale.Value},
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{keyTaskSet: taskSetToWire(out)})
}

func deleteTaskSet(w http.ResponseWriter, r *http.Request, ts driver.TaskSets) {
	var req struct {
		Cluster string `json:"cluster"`
		Service string `json:"service"`
		TaskSet string `json:"taskSet"`
		Force   bool   `json:"force"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	out, err := ts.DeleteTaskSet(r.Context(), driver.DeleteTaskSetInput{
		Cluster: req.Cluster, Service: req.Service, TaskSet: req.TaskSet, Force: req.Force,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{keyTaskSet: taskSetToWire(out)})
}

func describeTaskSets(w http.ResponseWriter, r *http.Request, ts driver.TaskSets) {
	var req struct {
		Cluster  string   `json:"cluster"`
		Service  string   `json:"service"`
		TaskSets []string `json:"taskSets"`
		Include  []string `json:"include"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	sets, failures, err := ts.DescribeTaskSets(r.Context(), req.Cluster, req.Service, req.TaskSets)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := fromTaskSets(sets, includes(req.Include, "TAGS"))
	if out == nil {
		out = []wireTaskSet{}
	}

	wire.WriteJSON(w, map[string]any{"taskSets": out, keyFailures: fromFailures(failures)})
}

func updatePrimaryTaskSet(w http.ResponseWriter, r *http.Request, ts driver.TaskSets) {
	var req struct {
		Cluster        string `json:"cluster"`
		Service        string `json:"service"`
		PrimaryTaskSet string `json:"primaryTaskSet"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	out, err := ts.UpdateServicePrimaryTaskSet(r.Context(), req.Cluster, req.Service, req.PrimaryTaskSet)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{keyTaskSet: taskSetToWire(out)})
}
