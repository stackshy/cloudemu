package ecs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

func (h *Handler) routeServices(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateService":
		h.createService(w, r)
	case "UpdateService":
		h.updateService(w, r)
	case "ListServices":
		h.listServices(w, r)
	case "DescribeServices":
		h.describeServices(w, r)
	case "DeleteService":
		h.deleteService(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) createService(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServiceName                   string                             `json:"serviceName"`
		Cluster                       string                             `json:"cluster"`
		TaskDefinition                string                             `json:"taskDefinition"`
		DesiredCount                  int                                `json:"desiredCount"`
		LaunchType                    string                             `json:"launchType"`
		SchedulingStrategy            string                             `json:"schedulingStrategy"`
		PlatformVersion               string                             `json:"platformVersion"`
		PropagateTags                 string                             `json:"propagateTags"`
		EnableExecuteCommand          bool                               `json:"enableExecuteCommand"`
		HealthCheckGracePeriodSeconds *int                               `json:"healthCheckGracePeriodSeconds"`
		DeploymentController          *wireDeploymentController          `json:"deploymentController"`
		DeploymentConfiguration       *wireDeploymentConfiguration       `json:"deploymentConfiguration"`
		NetworkConfiguration          *wireNetworkConfiguration          `json:"networkConfiguration"`
		CapacityProviderStrategy      []wireCapacityProviderStrategyItem `json:"capacityProviderStrategy"`
		LoadBalancers                 []wireLoadBalancer                 `json:"loadBalancers"`
		ServiceRegistries             []wireServiceRegistry              `json:"serviceRegistries"`
		Tags                          []wireTag                          `json:"tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	svc, err := h.ecs.CreateService(r.Context(), driver.CreateServiceInput{
		ServiceName:                   req.ServiceName,
		Cluster:                       req.Cluster,
		TaskDefinition:                req.TaskDefinition,
		DesiredCount:                  req.DesiredCount,
		LaunchType:                    req.LaunchType,
		SchedulingStrategy:            req.SchedulingStrategy,
		DeploymentController:          deploymentControllerType(req.DeploymentController),
		PlatformVersion:               req.PlatformVersion,
		PropagateTags:                 req.PropagateTags,
		EnableExecuteCommand:          req.EnableExecuteCommand,
		HealthCheckGracePeriodSeconds: req.HealthCheckGracePeriodSeconds,
		DeploymentConfiguration:       toDeploymentConfiguration(req.DeploymentConfiguration),
		NetworkConfiguration:          toNetworkConfiguration(req.NetworkConfiguration),
		CapacityProviderStrategy:      toCapacityProviderStrategy(req.CapacityProviderStrategy),
		LoadBalancers:                 toLoadBalancers(req.LoadBalancers),
		ServiceRegistries:             toServiceRegistries(req.ServiceRegistries),
		Tags:                          toTags(req.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"service": serviceToWire(svc)})
}

func (h *Handler) updateService(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Service                       string                             `json:"service"`
		Cluster                       string                             `json:"cluster"`
		TaskDefinition                string                             `json:"taskDefinition"`
		DesiredCount                  *int                               `json:"desiredCount"`
		ForceNewDeployment            bool                               `json:"forceNewDeployment"`
		PlatformVersion               string                             `json:"platformVersion"`
		PropagateTags                 string                             `json:"propagateTags"`
		EnableExecuteCommand          *bool                              `json:"enableExecuteCommand"`
		HealthCheckGracePeriodSeconds *int                               `json:"healthCheckGracePeriodSeconds"`
		DeploymentConfiguration       *wireDeploymentConfiguration       `json:"deploymentConfiguration"`
		NetworkConfiguration          *wireNetworkConfiguration          `json:"networkConfiguration"`
		CapacityProviderStrategy      []wireCapacityProviderStrategyItem `json:"capacityProviderStrategy"`
		LoadBalancers                 []wireLoadBalancer                 `json:"loadBalancers"`
		ServiceRegistries             []wireServiceRegistry              `json:"serviceRegistries"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	svc, err := h.ecs.UpdateService(r.Context(), driver.UpdateServiceInput{
		Service:                       req.Service,
		Cluster:                       req.Cluster,
		TaskDefinition:                req.TaskDefinition,
		DesiredCount:                  req.DesiredCount,
		ForceNewDeployment:            req.ForceNewDeployment,
		PlatformVersion:               req.PlatformVersion,
		PropagateTags:                 req.PropagateTags,
		EnableExecuteCommand:          req.EnableExecuteCommand,
		HealthCheckGracePeriodSeconds: req.HealthCheckGracePeriodSeconds,
		DeploymentConfiguration:       toDeploymentConfiguration(req.DeploymentConfiguration),
		NetworkConfiguration:          toNetworkConfiguration(req.NetworkConfiguration),
		CapacityProviderStrategy:      toCapacityProviderStrategy(req.CapacityProviderStrategy),
		LoadBalancers:                 toLoadBalancers(req.LoadBalancers),
		ServiceRegistries:             toServiceRegistries(req.ServiceRegistries),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"service": serviceToWire(svc)})
}

// deploymentControllerType extracts the controller type from the wire shape.
func deploymentControllerType(dc *wireDeploymentController) string {
	if dc == nil {
		return ""
	}

	return dc.Type
}

//nolint:dupl // decode-cluster/list-arns and decode-ids/describe shapes recur per resource family; typing them apart is intentional.
func (h *Handler) listServices(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string `json:"cluster"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	services, err := h.ecs.ListServices(r.Context(), req.Cluster)
	if err != nil {
		writeErr(w, err)

		return
	}

	arns := make([]string, 0, len(services))
	for i := range services {
		arns = append(arns, services[i].ARN)
	}

	wire.WriteJSON(w, map[string]any{"serviceArns": arns})
}

func (h *Handler) describeServices(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Services []string `json:"services"`
		Cluster  string   `json:"cluster"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	services, failures, err := h.ecs.DescribeServices(r.Context(), req.Cluster, req.Services)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]wireService, 0, len(services))
	for i := range services {
		out = append(out, serviceToWire(&services[i]))
	}

	wire.WriteJSON(w, map[string]any{"services": out, "failures": fromFailures(failures)})
}

func (h *Handler) deleteService(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Service string `json:"service"`
		Cluster string `json:"cluster"`
		Force   bool   `json:"force"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	svc, err := h.ecs.DeleteService(r.Context(), req.Cluster, req.Service, req.Force)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"service": serviceToWire(svc)})
}
