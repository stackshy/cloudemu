package eventbridge

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// endpointManager is the AWS-specific EventBridge global endpoint surface,
// asserted against the provider (not part of the portable EventBus driver).
type endpointManager interface {
	CreateEndpoint(ctx context.Context, cfg *ebdriver.EndpointConfig) (*ebdriver.Endpoint, error)
	DescribeEndpoint(ctx context.Context, name, homeRegion string) (*ebdriver.Endpoint, error)
	UpdateEndpoint(ctx context.Context, upd *ebdriver.EndpointUpdate) (*ebdriver.Endpoint, error)
	DeleteEndpoint(ctx context.Context, name string) error
	ListEndpoints(ctx context.Context, filter ebdriver.EndpointFilter) ([]ebdriver.Endpoint, string, error)
}

type endpointOp func(mgr endpointManager, w http.ResponseWriter, r *http.Request)

func endpointOps() map[string]endpointOp {
	return map[string]endpointOp{
		"CreateEndpoint":   createEndpoint,
		"DescribeEndpoint": describeEndpoint,
		"UpdateEndpoint":   updateEndpoint,
		"DeleteEndpoint":   deleteEndpoint,
		"ListEndpoints":    listEndpoints,
	}
}

// serveEndpointOp serves op when it is a global endpoint operation, reporting
// whether it did.
func (h *Handler) serveEndpointOp(w http.ResponseWriter, r *http.Request, op string) bool {
	fn, ok := endpointOps()[op]
	if !ok {
		return false
	}

	mgr, ok := h.bus.(endpointManager)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "global endpoints not supported"))
		return true
	}

	fn(mgr, w, r)

	return true
}

type primaryJSON struct {
	HealthCheck string `json:"HealthCheck,omitempty"`
}

type secondaryJSON struct {
	Route string `json:"Route,omitempty"`
}

type failoverJSON struct {
	Primary   *primaryJSON   `json:"Primary,omitempty"`
	Secondary *secondaryJSON `json:"Secondary,omitempty"`
}

type routingConfigJSON struct {
	FailoverConfig *failoverJSON `json:"FailoverConfig,omitempty"`
}

type replicationConfigJSON struct {
	State string `json:"State,omitempty"`
}

type endpointBusJSON struct {
	EventBusArn string `json:"EventBusArn"`
}

type endpointRequest struct {
	Name              string                 `json:"Name"`
	Description       *string                `json:"Description"`
	RoutingConfig     *routingConfigJSON     `json:"RoutingConfig"`
	ReplicationConfig *replicationConfigJSON `json:"ReplicationConfig"`
	EventBuses        []endpointBusJSON      `json:"EventBuses"`
	RoleArn           *string                `json:"RoleArn"`
	HomeRegion        string                 `json:"HomeRegion"`
}

type listEndpointsRequest struct {
	NamePrefix string `json:"NamePrefix"`
	HomeRegion string `json:"HomeRegion"`
	MaxResults int    `json:"MaxResults"`
	NextToken  string `json:"NextToken"`
}

type endpointJSON struct {
	Name              string                 `json:"Name"`
	Description       string                 `json:"Description,omitempty"`
	Arn               string                 `json:"Arn"`
	RoutingConfig     *routingConfigJSON     `json:"RoutingConfig"`
	ReplicationConfig *replicationConfigJSON `json:"ReplicationConfig"`
	EventBuses        []endpointBusJSON      `json:"EventBuses"`
	RoleArn           string                 `json:"RoleArn,omitempty"`
	EndpointID        string                 `json:"EndpointId,omitempty"`
	EndpointURL       string                 `json:"EndpointUrl,omitempty"`
	State             string                 `json:"State"`
	StateReason       string                 `json:"StateReason,omitempty"`
	CreationTime      *float64               `json:"CreationTime,omitempty"`
	LastModifiedTime  *float64               `json:"LastModifiedTime,omitempty"`
}

type listEndpointsResponse struct {
	Endpoints []endpointJSON `json:"Endpoints"`
	NextToken string         `json:"NextToken,omitempty"`
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

// toRouting converts a wire RoutingConfig; nil stays nil (omitted).
func toRouting(rc *routingConfigJSON) *ebdriver.EndpointRouting {
	if rc == nil {
		return nil
	}

	out := &ebdriver.EndpointRouting{}

	if fc := rc.FailoverConfig; fc != nil {
		if fc.Primary != nil {
			out.HealthCheck = fc.Primary.HealthCheck
		}

		if fc.Secondary != nil {
			out.SecondaryRoute = fc.Secondary.Route
		}
	}

	return out
}

func toBusARNs(buses []endpointBusJSON) []string {
	if buses == nil {
		return nil
	}

	out := make([]string, len(buses))
	for i, b := range buses {
		out[i] = b.EventBusArn
	}

	return out
}

func (req *endpointRequest) replicationState() *string {
	if req.ReplicationConfig == nil {
		return nil
	}

	return &req.ReplicationConfig.State
}

// toEndpointJSON renders an endpoint in full, as DescribeEndpoint and
// ListEndpoints return it.
func toEndpointJSON(e *ebdriver.Endpoint) endpointJSON {
	buses := make([]endpointBusJSON, len(e.EventBusARNs))
	for i, arn := range e.EventBusARNs {
		buses[i] = endpointBusJSON{EventBusArn: arn}
	}

	return endpointJSON{
		Name:        e.Name,
		Description: e.Description,
		Arn:         e.ARN,
		RoutingConfig: &routingConfigJSON{FailoverConfig: &failoverJSON{
			Primary:   &primaryJSON{HealthCheck: e.Routing.HealthCheck},
			Secondary: &secondaryJSON{Route: e.Routing.SecondaryRoute},
		}},
		ReplicationConfig: &replicationConfigJSON{State: e.ReplicationState},
		EventBuses:        buses,
		RoleArn:           e.RoleARN,
		EndpointID:        e.EndpointID,
		EndpointURL:       e.EndpointURL,
		State:             e.State,
		StateReason:       e.StateReason,
		CreationTime:      timeToEpoch(e.CreatedAt),
		LastModifiedTime:  timeToEpoch(e.LastModifiedAt),
	}
}

func createEndpoint(mgr endpointManager, w http.ResponseWriter, r *http.Request) {
	var req endpointRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	cfg := &ebdriver.EndpointConfig{
		Name: req.Name, Description: deref(req.Description), ReplicationState: deref(req.replicationState()),
		EventBusARNs: toBusARNs(req.EventBuses), RoleARN: deref(req.RoleArn),
	}

	if rc := toRouting(req.RoutingConfig); rc != nil {
		cfg.Routing = *rc
	}

	e, err := mgr.CreateEndpoint(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	// CreateEndpoint answers without the description, id, URL and times.
	out := toEndpointJSON(e)
	out.Description, out.EndpointID, out.EndpointURL = "", "", ""
	out.StateReason, out.CreationTime, out.LastModifiedTime = "", nil, nil

	wire.WriteJSON(w, out)
}

func describeEndpoint(mgr endpointManager, w http.ResponseWriter, r *http.Request) {
	var req endpointRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	e, err := mgr.DescribeEndpoint(r.Context(), req.Name, req.HomeRegion)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, toEndpointJSON(e))
}

func updateEndpoint(mgr endpointManager, w http.ResponseWriter, r *http.Request) {
	var req endpointRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	e, err := mgr.UpdateEndpoint(r.Context(), &ebdriver.EndpointUpdate{
		Name: req.Name, Description: req.Description, Routing: toRouting(req.RoutingConfig),
		ReplicationState: req.replicationState(), EventBusARNs: toBusARNs(req.EventBuses), RoleARN: req.RoleArn,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	// UpdateEndpoint answers without the description, state reason and times.
	out := toEndpointJSON(e)
	out.Description, out.StateReason, out.CreationTime, out.LastModifiedTime = "", "", nil, nil

	wire.WriteJSON(w, out)
}

func deleteEndpoint(mgr endpointManager, w http.ResponseWriter, r *http.Request) {
	var req endpointRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := mgr.DeleteEndpoint(r.Context(), req.Name); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func listEndpoints(mgr endpointManager, w http.ResponseWriter, r *http.Request) {
	var req listEndpointsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	items, next, err := mgr.ListEndpoints(r.Context(), ebdriver.EndpointFilter{
		NamePrefix: req.NamePrefix, HomeRegion: req.HomeRegion, MaxResults: req.MaxResults, NextToken: req.NextToken,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	out := listEndpointsResponse{Endpoints: make([]endpointJSON, 0, len(items)), NextToken: next}
	for i := range items {
		out.Endpoints = append(out.Endpoints, toEndpointJSON(&items[i]))
	}

	wire.WriteJSON(w, out)
}
