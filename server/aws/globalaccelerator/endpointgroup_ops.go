package globalaccelerator

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/globalaccelerator/driver"
)

// registerEndpointGroupRoutes wires the endpoint-group operations.
func (h *Handler) registerEndpointGroupRoutes() {
	h.routes["CreateEndpointGroup"] = h.createEndpointGroup
	h.routes["DescribeEndpointGroup"] = h.describeEndpointGroup
	h.routes["UpdateEndpointGroup"] = h.updateEndpointGroup
	h.routes["DeleteEndpointGroup"] = h.deleteEndpointGroup
	h.routes["ListEndpointGroups"] = h.listEndpointGroups
}

type createEndpointGroupRequest struct {
	ListenerArn                string                      `json:"ListenerArn"`
	EndpointGroupRegion        string                      `json:"EndpointGroupRegion"`
	EndpointConfigurations     []endpointConfigurationJSON `json:"EndpointConfigurations"`
	TrafficDialPercentage      *float64                    `json:"TrafficDialPercentage"`
	HealthCheckPort            *int32                      `json:"HealthCheckPort"`
	HealthCheckProtocol        string                      `json:"HealthCheckProtocol"`
	HealthCheckPath            string                      `json:"HealthCheckPath"`
	HealthCheckIntervalSeconds *int32                      `json:"HealthCheckIntervalSeconds"`
	ThresholdCount             *int32                      `json:"ThresholdCount"`
	PortOverrides              []portOverrideJSON          `json:"PortOverrides"`
	IdempotencyToken           string                      `json:"IdempotencyToken"`
}

type endpointGroupResponse struct {
	EndpointGroup endpointGroupJSON `json:"EndpointGroup"`
}

func (h *Handler) createEndpointGroup(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createEndpointGroupRequest) (any, error) {
		g, err := h.ga.CreateEndpointGroup(ctx, &driver.CreateEndpointGroupInput{
			ListenerArn:                req.ListenerArn,
			EndpointGroupRegion:        req.EndpointGroupRegion,
			EndpointConfigurations:     endpointConfigsFromWire(req.EndpointConfigurations),
			TrafficDialPercentage:      req.TrafficDialPercentage,
			HealthCheckPort:            req.HealthCheckPort,
			HealthCheckProtocol:        req.HealthCheckProtocol,
			HealthCheckPath:            req.HealthCheckPath,
			HealthCheckIntervalSeconds: req.HealthCheckIntervalSeconds,
			ThresholdCount:             req.ThresholdCount,
			PortOverrides:              portOverridesFromWire(req.PortOverrides),
			IdempotencyToken:           req.IdempotencyToken,
		})
		if err != nil {
			return nil, err
		}

		return endpointGroupResponse{EndpointGroup: endpointGroupToWire(g)}, nil
	})
}

type describeEndpointGroupRequest struct {
	EndpointGroupArn string `json:"EndpointGroupArn"`
}

func (h *Handler) describeEndpointGroup(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeEndpointGroupRequest) (any, error) {
		g, err := h.ga.DescribeEndpointGroup(ctx, req.EndpointGroupArn)
		if err != nil {
			return nil, err
		}

		return endpointGroupResponse{EndpointGroup: endpointGroupToWire(g)}, nil
	})
}

type updateEndpointGroupRequest struct {
	EndpointGroupArn           string                       `json:"EndpointGroupArn"`
	EndpointConfigurations     *[]endpointConfigurationJSON `json:"EndpointConfigurations"`
	TrafficDialPercentage      *float64                     `json:"TrafficDialPercentage"`
	HealthCheckPort            *int32                       `json:"HealthCheckPort"`
	HealthCheckProtocol        *string                      `json:"HealthCheckProtocol"`
	HealthCheckPath            *string                      `json:"HealthCheckPath"`
	HealthCheckIntervalSeconds *int32                       `json:"HealthCheckIntervalSeconds"`
	ThresholdCount             *int32                       `json:"ThresholdCount"`
	PortOverrides              *[]portOverrideJSON          `json:"PortOverrides"`
}

func (h *Handler) updateEndpointGroup(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateEndpointGroupRequest) (any, error) {
		g, err := h.ga.UpdateEndpointGroup(ctx, buildUpdateEndpointGroupInput(req))
		if err != nil {
			return nil, err
		}

		return endpointGroupResponse{EndpointGroup: endpointGroupToWire(g)}, nil
	})
}

// buildUpdateEndpointGroupInput translates the wire request into the driver
// input, mapping the optional endpoint-configuration and port-override lists.
func buildUpdateEndpointGroupInput(req *updateEndpointGroupRequest) *driver.UpdateEndpointGroupInput {
	in := &driver.UpdateEndpointGroupInput{
		EndpointGroupArn:           req.EndpointGroupArn,
		TrafficDialPercentage:      req.TrafficDialPercentage,
		HealthCheckPort:            req.HealthCheckPort,
		HealthCheckProtocol:        req.HealthCheckProtocol,
		HealthCheckPath:            req.HealthCheckPath,
		HealthCheckIntervalSeconds: req.HealthCheckIntervalSeconds,
		ThresholdCount:             req.ThresholdCount,
	}

	if req.EndpointConfigurations != nil {
		cfgs := endpointConfigsFromWire(*req.EndpointConfigurations)
		in.EndpointConfigurations = &cfgs
	}

	if req.PortOverrides != nil {
		pos := portOverridesFromWire(*req.PortOverrides)
		in.PortOverrides = &pos
	}

	return in
}

type deleteEndpointGroupRequest struct {
	EndpointGroupArn string `json:"EndpointGroupArn"`
}

func (h *Handler) deleteEndpointGroup(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteEndpointGroupRequest) (any, error) {
		if err := h.ga.DeleteEndpointGroup(ctx, req.EndpointGroupArn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listEndpointGroupsRequest struct {
	ListenerArn string `json:"ListenerArn"`
	MaxResults  int32  `json:"MaxResults"`
	NextToken   string `json:"NextToken"`
}

type listEndpointGroupsResponse struct {
	EndpointGroups []endpointGroupJSON `json:"EndpointGroups"`
	NextToken      string              `json:"NextToken,omitempty"`
}

func (h *Handler) listEndpointGroups(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listEndpointGroupsRequest) (any, error) {
		gs, next, err := h.ga.ListEndpointGroups(ctx, req.ListenerArn,
			driver.Page{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		return listEndpointGroupsResponse{
			EndpointGroups: toWireList(gs, endpointGroupToWire),
			NextToken:      next,
		}, nil
	})
}
