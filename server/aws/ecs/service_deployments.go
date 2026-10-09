package ecs

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

type wireRevisionSummary struct {
	Arn                string `json:"arn"`
	RequestedTaskCount int    `json:"requestedTaskCount"`
	RunningTaskCount   int    `json:"runningTaskCount"`
	PendingTaskCount   int    `json:"pendingTaskCount"`
}

type wireRollback struct {
	Reason             string  `json:"reason,omitempty"`
	ServiceRevisionArn string  `json:"serviceRevisionArn,omitempty"`
	StartedAt          float64 `json:"startedAt,omitempty"`
}

// wireServiceDeploymentBrief is the ServiceDeploymentBrief shape ListServiceDeployments returns.
type wireServiceDeploymentBrief struct {
	ServiceDeploymentArn     string  `json:"serviceDeploymentArn"`
	ServiceArn               string  `json:"serviceArn"`
	ClusterArn               string  `json:"clusterArn"`
	TargetServiceRevisionArn string  `json:"targetServiceRevisionArn"`
	Status                   string  `json:"status"`
	StatusReason             string  `json:"statusReason,omitempty"`
	CreatedAt                float64 `json:"createdAt,omitempty"`
	StartedAt                float64 `json:"startedAt,omitempty"`
	FinishedAt               float64 `json:"finishedAt,omitempty"`
}

// wireServiceDeployment is the full ServiceDeployment shape DescribeServiceDeployments returns.
type wireServiceDeployment struct {
	ServiceDeploymentArn    string                       `json:"serviceDeploymentArn"`
	ServiceArn              string                       `json:"serviceArn"`
	ClusterArn              string                       `json:"clusterArn"`
	Status                  string                       `json:"status"`
	StatusReason            string                       `json:"statusReason,omitempty"`
	LifecycleStage          string                       `json:"lifecycleStage,omitempty"`
	TargetServiceRevision   wireRevisionSummary          `json:"targetServiceRevision"`
	SourceServiceRevisions  []wireRevisionSummary        `json:"sourceServiceRevisions"`
	DeploymentConfiguration *wireDeploymentConfiguration `json:"deploymentConfiguration,omitempty"`
	Rollback                *wireRollback                `json:"rollback,omitempty"`
	CreatedAt               float64                      `json:"createdAt,omitempty"`
	StartedAt               float64                      `json:"startedAt,omitempty"`
	FinishedAt              float64                      `json:"finishedAt,omitempty"`
	StoppedAt               float64                      `json:"stoppedAt,omitempty"`
	UpdatedAt               float64                      `json:"updatedAt,omitempty"`
}

type wireServiceRevision struct {
	ServiceRevisionArn       string                             `json:"serviceRevisionArn"`
	ServiceArn               string                             `json:"serviceArn"`
	ClusterArn               string                             `json:"clusterArn"`
	TaskDefinition           string                             `json:"taskDefinition"`
	LaunchType               string                             `json:"launchType,omitempty"`
	PlatformVersion          string                             `json:"platformVersion,omitempty"`
	CapacityProviderStrategy []wireCapacityProviderStrategyItem `json:"capacityProviderStrategy,omitempty"`
	NetworkConfiguration     *wireNetworkConfiguration          `json:"networkConfiguration,omitempty"`
	LoadBalancers            []wireLoadBalancer                 `json:"loadBalancers,omitempty"`
	ServiceRegistries        []wireServiceRegistry              `json:"serviceRegistries,omitempty"`
	ServiceConnectConfig     json.RawMessage                    `json:"serviceConnectConfiguration,omitempty"`
	CreatedAt                float64                            `json:"createdAt,omitempty"`
}

func revisionSummariesToWire(in []driver.ServiceRevisionSummary) []wireRevisionSummary {
	out := make([]wireRevisionSummary, 0, len(in))
	for _, s := range in {
		out = append(out, wireRevisionSummary{
			Arn: s.ARN, RequestedTaskCount: s.RequestedTaskCount, RunningTaskCount: s.RunningTaskCount, PendingTaskCount: s.PendingTaskCount,
		})
	}

	return out
}

func serviceDeploymentToWire(d *driver.ServiceDeployment) wireServiceDeployment {
	out := wireServiceDeployment{
		ServiceDeploymentArn: d.ARN, ServiceArn: d.ServiceARN, ClusterArn: d.ClusterARN,
		Status: d.Status, StatusReason: d.StatusReason, LifecycleStage: d.LifecycleStage,
		TargetServiceRevision:   revisionSummariesToWire([]driver.ServiceRevisionSummary{d.TargetServiceRevision})[0],
		SourceServiceRevisions:  revisionSummariesToWire(d.SourceServiceRevisions),
		DeploymentConfiguration: fromDeploymentConfiguration(d.DeploymentConfiguration),
		CreatedAt:               epoch(d.CreatedAt), StartedAt: epoch(d.StartedAt), FinishedAt: epoch(d.FinishedAt),
		StoppedAt: epoch(d.StoppedAt), UpdatedAt: epoch(d.UpdatedAt),
	}

	if d.Rollback != nil {
		out.Rollback = &wireRollback{
			Reason: d.Rollback.Reason, ServiceRevisionArn: d.Rollback.ServiceRevisionARN, StartedAt: epoch(d.Rollback.StartedAt),
		}
	}

	return out
}

func serviceRevisionToWire(r *driver.ServiceRevision) wireServiceRevision {
	out := wireServiceRevision{
		ServiceRevisionArn: r.ARN, ServiceArn: r.ServiceARN, ClusterArn: r.ClusterARN, TaskDefinition: r.TaskDefinition,
		LaunchType: r.LaunchType, PlatformVersion: r.PlatformVersion,
		CapacityProviderStrategy: fromCapacityProviderStrategy(r.CapacityProviderStrategy),
		NetworkConfiguration:     fromNetworkConfiguration(r.NetworkConfiguration),
		LoadBalancers:            fromLoadBalancers(r.LoadBalancers), ServiceRegistries: fromServiceRegistries(r.ServiceRegistries),
		CreatedAt: epoch(r.CreatedAt),
	}

	if r.ServiceConnect != nil && len(r.ServiceConnect.Raw) > 0 {
		out.ServiceConnectConfig = r.ServiceConnect.Raw
	}

	return out
}

func (h *Handler) routeServiceDeployments(w http.ResponseWriter, r *http.Request, op string) bool {
	handlers := map[string]func(http.ResponseWriter, *http.Request, driver.ServiceDeployments){
		"ListServiceDeployments":     listServiceDeployments,
		"DescribeServiceDeployments": describeServiceDeployments,
		"DescribeServiceRevisions":   describeServiceRevisions,
		"StopServiceDeployment":      stopServiceDeployment,
	}

	if handle, known := handlers[op]; known {
		if sd, ok := capabilityOf[driver.ServiceDeployments](h.ecs, w, op); ok {
			handle(w, r, sd)
		}

		return true
	}

	if op == "ListServicesByNamespace" {
		if sn, ok := capabilityOf[driver.ServiceNamespaces](h.ecs, w, op); ok {
			listServicesByNamespace(w, r, sn)
		}

		return true
	}

	return false
}

// epochToRFC3339 converts a wire epoch-seconds filter to the RFC3339 instant the
// driver takes ("" when unset).
func epochToRFC3339(sec *float64) string {
	if sec == nil {
		return ""
	}

	return time.Unix(int64(*sec), 0).UTC().Format(time.RFC3339)
}

func listServiceDeployments(w http.ResponseWriter, r *http.Request, sd driver.ServiceDeployments) {
	var req struct {
		Service   string   `json:"service"`
		Cluster   string   `json:"cluster"`
		Status    []string `json:"status"`
		CreatedAt *struct {
			Before *float64 `json:"before"`
			After  *float64 `json:"after"`
		} `json:"createdAt"`
		MaxResults int    `json:"maxResults"`
		NextToken  string `json:"nextToken"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	in := driver.ListServiceDeploymentsInput{
		Cluster: req.Cluster, Service: req.Service, Statuses: req.Status, MaxResults: req.MaxResults, NextToken: req.NextToken,
	}

	if req.CreatedAt != nil {
		in.CreatedAtBefore, in.CreatedAtAfter = epochToRFC3339(req.CreatedAt.Before), epochToRFC3339(req.CreatedAt.After)
	}

	deps, next, err := sd.ListServiceDeployments(r.Context(), in)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]wireServiceDeploymentBrief, 0, len(deps))

	for i := range deps {
		d := &deps[i]
		out = append(out, wireServiceDeploymentBrief{
			ServiceDeploymentArn: d.ARN, ServiceArn: d.ServiceARN, ClusterArn: d.ClusterARN,
			TargetServiceRevisionArn: d.TargetServiceRevision.ARN, Status: d.Status, StatusReason: d.StatusReason,
			CreatedAt: epoch(d.CreatedAt), StartedAt: epoch(d.StartedAt), FinishedAt: epoch(d.FinishedAt),
		})
	}

	resp := map[string]any{"serviceDeployments": out}
	if next != "" {
		resp["nextToken"] = next
	}

	wire.WriteJSON(w, resp)
}

func describeServiceDeployments(w http.ResponseWriter, r *http.Request, sd driver.ServiceDeployments) {
	describeByARNs(w, r, "serviceDeploymentArns", "serviceDeployments", sd.DescribeServiceDeployments,
		func(d *driver.ServiceDeployment) any { return serviceDeploymentToWire(d) })
}

func describeServiceRevisions(w http.ResponseWriter, r *http.Request, sd driver.ServiceDeployments) {
	describeByARNs(w, r, "serviceRevisionArns", "serviceRevisions", sd.DescribeServiceRevisions,
		func(rev *driver.ServiceRevision) any { return serviceRevisionToWire(rev) })
}

// describeByARNs serves a Describe* call that takes a list of ARNs under arnsKey
// and answers with the converted items under itemsKey plus failures.
func describeByARNs[T any](
	w http.ResponseWriter, r *http.Request, arnsKey, itemsKey string,
	call func(context.Context, []string) ([]T, []driver.Failure, error), toWire func(*T) any,
) {
	var req map[string][]string

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	items, failures, err := call(r.Context(), req[arnsKey])
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]any, 0, len(items))

	for i := range items {
		out = append(out, toWire(&items[i]))
	}

	wire.WriteJSON(w, map[string]any{itemsKey: out, "failures": fromFailures(failures)})
}

func stopServiceDeployment(w http.ResponseWriter, r *http.Request, sd driver.ServiceDeployments) {
	var req struct {
		ServiceDeploymentArn string `json:"serviceDeploymentArn"`
		StopType             string `json:"stopType"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	arn, err := sd.StopServiceDeployment(r.Context(), req.ServiceDeploymentArn, req.StopType)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"serviceDeploymentArn": arn})
}

func listServicesByNamespace(w http.ResponseWriter, r *http.Request, sn driver.ServiceNamespaces) {
	var req struct {
		Namespace  string `json:"namespace"`
		MaxResults int    `json:"maxResults"`
		NextToken  string `json:"nextToken"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	arns, next, err := sn.ListServicesByNamespace(r.Context(), req.Namespace, req.MaxResults, req.NextToken)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, listResponse("serviceArns", arns, next))
}

// toServiceConnect converts the wire serviceConnectConfiguration to the driver
// shape, keeping the raw JSON and extracting the namespace.
func toServiceConnect(raw json.RawMessage) *driver.ServiceConnectConfiguration {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	var cfg struct {
		Namespace string `json:"namespace"`
	}

	_ = json.Unmarshal(raw, &cfg)

	return &driver.ServiceConnectConfiguration{Namespace: cfg.Namespace, Raw: append([]byte(nil), raw...)}
}
