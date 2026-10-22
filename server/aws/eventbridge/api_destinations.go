package eventbridge

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

type createAPIDestinationRequest struct {
	Name                         string `json:"Name"`
	Description                  string `json:"Description"`
	ConnectionArn                string `json:"ConnectionArn"`
	InvocationEndpoint           string `json:"InvocationEndpoint"`
	HTTPMethod                   string `json:"HttpMethod"`
	InvocationRateLimitPerSecond *int   `json:"InvocationRateLimitPerSecond"`
}

type updateAPIDestinationRequest struct {
	Name                         string  `json:"Name"`
	Description                  *string `json:"Description"`
	ConnectionArn                *string `json:"ConnectionArn"`
	InvocationEndpoint           *string `json:"InvocationEndpoint"`
	HTTPMethod                   *string `json:"HttpMethod"`
	InvocationRateLimitPerSecond *int    `json:"InvocationRateLimitPerSecond"`
}

// apiDestinationStateResponse is the reply of Create and UpdateApiDestination.
type apiDestinationStateResponse struct {
	APIDestinationArn   string   `json:"ApiDestinationArn"`
	APIDestinationState string   `json:"ApiDestinationState"`
	CreationTime        *float64 `json:"CreationTime,omitempty"`
	LastModifiedTime    *float64 `json:"LastModifiedTime,omitempty"`
}

type apiDestinationJSON struct {
	APIDestinationArn            string   `json:"ApiDestinationArn"`
	Name                         string   `json:"Name"`
	Description                  string   `json:"Description,omitempty"`
	APIDestinationState          string   `json:"ApiDestinationState"`
	ConnectionArn                string   `json:"ConnectionArn"`
	InvocationEndpoint           string   `json:"InvocationEndpoint"`
	HTTPMethod                   string   `json:"HttpMethod"`
	InvocationRateLimitPerSecond int      `json:"InvocationRateLimitPerSecond"`
	CreationTime                 *float64 `json:"CreationTime,omitempty"`
	LastModifiedTime             *float64 `json:"LastModifiedTime,omitempty"`
}

type listAPIDestinationsResponse struct {
	APIDestinations []apiDestinationJSON `json:"ApiDestinations"`
	NextToken       string               `json:"NextToken,omitempty"`
}

func toAPIDestinationState(d *ebdriver.APIDestination) apiDestinationStateResponse {
	return apiDestinationStateResponse{
		APIDestinationArn:   d.ARN,
		APIDestinationState: d.State,
		CreationTime:        timeToEpoch(d.CreatedAt),
		LastModifiedTime:    timeToEpoch(d.LastModifiedAt),
	}
}

func toAPIDestinationJSON(d *ebdriver.APIDestination, full bool) apiDestinationJSON {
	out := apiDestinationJSON{
		APIDestinationArn:            d.ARN,
		Name:                         d.Name,
		APIDestinationState:          d.State,
		ConnectionArn:                d.ConnectionARN,
		InvocationEndpoint:           d.InvocationEndpoint,
		HTTPMethod:                   d.HTTPMethod,
		InvocationRateLimitPerSecond: d.InvocationRateLimitPerSecond,
		CreationTime:                 timeToEpoch(d.CreatedAt),
		LastModifiedTime:             timeToEpoch(d.LastModifiedAt),
	}

	if full {
		out.Description = d.Description
	}

	return out
}

// writeDestinationState writes the state reply of a create or update, or the
// error that call returned.
func writeDestinationState(w http.ResponseWriter, d *ebdriver.APIDestination, err error) {
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, toAPIDestinationState(d))
}

func createAPIDestination(mgr connectionManager, w http.ResponseWriter, r *http.Request) {
	var req createAPIDestinationRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	d, err := mgr.CreateAPIDestination(r.Context(), &ebdriver.APIDestinationConfig{
		Name:                         req.Name,
		Description:                  req.Description,
		ConnectionARN:                req.ConnectionArn,
		InvocationEndpoint:           req.InvocationEndpoint,
		HTTPMethod:                   req.HTTPMethod,
		InvocationRateLimitPerSecond: req.InvocationRateLimitPerSecond,
	})

	writeDestinationState(w, d, err)
}

func describeAPIDestination(mgr connectionManager, w http.ResponseWriter, r *http.Request) {
	var req connectionNameRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	d, err := mgr.DescribeAPIDestination(r.Context(), req.Name)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, toAPIDestinationJSON(d, true))
}

func updateAPIDestination(mgr connectionManager, w http.ResponseWriter, r *http.Request) {
	var req updateAPIDestinationRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	d, err := mgr.UpdateAPIDestination(r.Context(), &ebdriver.APIDestinationUpdate{
		Name:                         req.Name,
		Description:                  req.Description,
		ConnectionARN:                req.ConnectionArn,
		InvocationEndpoint:           req.InvocationEndpoint,
		HTTPMethod:                   req.HTTPMethod,
		InvocationRateLimitPerSecond: req.InvocationRateLimitPerSecond,
	})

	writeDestinationState(w, d, err)
}

func deleteAPIDestination(mgr connectionManager, w http.ResponseWriter, r *http.Request) {
	var req connectionNameRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := mgr.DeleteAPIDestination(r.Context(), req.Name); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func fetchDestinations(ctx context.Context, mgr connectionManager, req *listFilterRequest) ([]ebdriver.APIDestination, error) {
	return mgr.ListAPIDestinations(ctx, ebdriver.APIDestinationFilter{NamePrefix: req.NamePrefix, ConnectionARN: req.ConnectionArn})
}

func destinationListItem(d *ebdriver.APIDestination) apiDestinationJSON {
	return toAPIDestinationJSON(d, false)
}

func destinationPage(page []apiDestinationJSON, next string) any {
	return listAPIDestinationsResponse{APIDestinations: page, NextToken: next}
}
