package ecs

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// describeCapacityProvidersDefaultPage is the page size DescribeCapacityProviders
// uses when maxResults is omitted (the documented default and maximum is 10).
const describeCapacityProvidersDefaultPage = 10

// Response body keys shared by the capacity-provider operations.
const (
	keyCapacityProvider = "capacityProvider"
	keyFailures         = "failures"
)

type wireManagedScaling struct {
	Status                 string `json:"status,omitempty"`
	TargetCapacity         *int   `json:"targetCapacity,omitempty"`
	MinimumScalingStepSize *int   `json:"minimumScalingStepSize,omitempty"`
	MaximumScalingStepSize *int   `json:"maximumScalingStepSize,omitempty"`
	InstanceWarmupPeriod   *int   `json:"instanceWarmupPeriod,omitempty"`
}

type wireAutoScalingGroupProvider struct {
	AutoScalingGroupArn          string              `json:"autoScalingGroupArn,omitempty"`
	ManagedScaling               *wireManagedScaling `json:"managedScaling,omitempty"`
	ManagedTerminationProtection string              `json:"managedTerminationProtection,omitempty"`
	ManagedDraining              string              `json:"managedDraining,omitempty"`
}

type wireCapacityProvider struct {
	CapacityProviderArn      string                        `json:"capacityProviderArn"`
	Name                     string                        `json:"name"`
	Status                   string                        `json:"status"`
	Type                     string                        `json:"type,omitempty"`
	Cluster                  string                        `json:"cluster,omitempty"`
	UpdateStatus             string                        `json:"updateStatus,omitempty"`
	UpdateStatusReason       string                        `json:"updateStatusReason,omitempty"`
	AutoScalingGroupProvider *wireAutoScalingGroupProvider `json:"autoScalingGroupProvider,omitempty"`
	ManagedInstancesProvider json.RawMessage               `json:"managedInstancesProvider,omitempty"`
	Tags                     []wireTag                     `json:"tags,omitempty"`
}

func (h *Handler) routeCapacityProviders(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateCapacityProvider":
		h.createCapacityProvider(w, r)
	case "DescribeCapacityProviders":
		h.describeCapacityProviders(w, r)
	case "UpdateCapacityProvider":
		h.updateCapacityProvider(w, r)
	case "DeleteCapacityProvider":
		h.deleteCapacityProvider(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) createCapacityProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                     string                        `json:"name"`
		Cluster                  string                        `json:"cluster"`
		AutoScalingGroupProvider *wireAutoScalingGroupProvider `json:"autoScalingGroupProvider"`
		ManagedInstancesProvider json.RawMessage               `json:"managedInstancesProvider"`
		Tags                     []wireTag                     `json:"tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	cp, err := h.ecs.CreateCapacityProvider(r.Context(), driver.CreateCapacityProviderInput{
		Name:                     req.Name,
		Cluster:                  req.Cluster,
		AutoScalingGroupProvider: toASGProvider(req.AutoScalingGroupProvider),
		ManagedInstancesProvider: nonNullRaw(req.ManagedInstancesProvider),
		Tags:                     toTags(req.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{keyCapacityProvider: capacityProviderToWire(cp)})
}

func (h *Handler) describeCapacityProviders(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CapacityProviders []string `json:"capacityProviders"`
		Cluster           string   `json:"cluster"`
		Include           []string `json:"include"`
		MaxResults        int      `json:"maxResults"`
		NextToken         string   `json:"nextToken"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	providers, failures, err := h.ecs.DescribeCapacityProviders(r.Context(), req.Cluster, req.CapacityProviders)
	if err != nil {
		writeErr(w, err)

		return
	}

	maxResults := req.MaxResults
	if maxResults <= 0 || maxResults > describeCapacityProvidersDefaultPage {
		maxResults = describeCapacityProvidersDefaultPage
	}

	page, err := pagination.Paginate(providers, req.NextToken, maxResults)
	if err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", "invalid nextToken: "+err.Error())

		return
	}

	// Tags are only returned when the caller opts in via include=TAGS.
	wantTags := includes(req.Include, "TAGS")

	out := make([]wireCapacityProvider, 0, len(page.Items))

	for i := range page.Items {
		wcp := capacityProviderToWire(&page.Items[i])
		if !wantTags {
			wcp.Tags = nil
		}

		out = append(out, wcp)
	}

	resp := map[string]any{"capacityProviders": out, keyFailures: fromFailures(failures)}
	if page.NextPageToken != "" {
		resp["nextToken"] = page.NextPageToken
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) updateCapacityProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                     string                        `json:"name"`
		Cluster                  string                        `json:"cluster"`
		AutoScalingGroupProvider *wireAutoScalingGroupProvider `json:"autoScalingGroupProvider"`
		ManagedInstancesProvider json.RawMessage               `json:"managedInstancesProvider"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	cp, err := h.ecs.UpdateCapacityProvider(r.Context(), driver.UpdateCapacityProviderInput{
		Name:                     req.Name,
		Cluster:                  req.Cluster,
		AutoScalingGroupProvider: toASGProvider(req.AutoScalingGroupProvider),
		ManagedInstancesProvider: nonNullRaw(req.ManagedInstancesProvider),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{keyCapacityProvider: capacityProviderToWire(cp)})
}

func (h *Handler) deleteCapacityProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CapacityProvider string `json:"capacityProvider"`
		Cluster          string `json:"cluster"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	cp, err := h.ecs.DeleteCapacityProvider(r.Context(), req.Cluster, req.CapacityProvider)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{keyCapacityProvider: capacityProviderToWire(cp)})
}

// nonNullRaw drops an absent or JSON-null raw block so the driver sees "not set".
func nonNullRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	return raw
}

func toASGProvider(in *wireAutoScalingGroupProvider) *driver.AutoScalingGroupProvider {
	if in == nil {
		return nil
	}

	out := &driver.AutoScalingGroupProvider{
		AutoScalingGroupARN:          in.AutoScalingGroupArn,
		ManagedTerminationProtection: in.ManagedTerminationProtection,
		ManagedDraining:              in.ManagedDraining,
	}

	if ms := in.ManagedScaling; ms != nil {
		out.ManagedScaling = &driver.ManagedScaling{
			Status:                 ms.Status,
			TargetCapacity:         ms.TargetCapacity,
			MinimumScalingStepSize: ms.MinimumScalingStepSize,
			MaximumScalingStepSize: ms.MaximumScalingStepSize,
			InstanceWarmupPeriod:   ms.InstanceWarmupPeriod,
		}
	}

	return out
}

func capacityProviderToWire(cp *driver.CapacityProvider) wireCapacityProvider {
	out := wireCapacityProvider{
		CapacityProviderArn:      cp.ARN,
		Name:                     cp.Name,
		Status:                   cp.Status,
		Type:                     cp.Type,
		Cluster:                  cp.Cluster,
		UpdateStatus:             cp.UpdateStatus,
		UpdateStatusReason:       cp.UpdateStatusReason,
		ManagedInstancesProvider: cp.ManagedInstancesProvider,
		Tags:                     fromTags(cp.Tags),
	}

	if asg := cp.AutoScalingGroupProvider; asg != nil {
		out.AutoScalingGroupProvider = &wireAutoScalingGroupProvider{
			AutoScalingGroupArn:          asg.AutoScalingGroupARN,
			ManagedTerminationProtection: asg.ManagedTerminationProtection,
			ManagedDraining:              asg.ManagedDraining,
		}

		if ms := asg.ManagedScaling; ms != nil {
			out.AutoScalingGroupProvider.ManagedScaling = &wireManagedScaling{
				Status:                 ms.Status,
				TargetCapacity:         ms.TargetCapacity,
				MinimumScalingStepSize: ms.MinimumScalingStepSize,
				MaximumScalingStepSize: ms.MaximumScalingStepSize,
				InstanceWarmupPeriod:   ms.InstanceWarmupPeriod,
			}
		}
	}

	return out
}
