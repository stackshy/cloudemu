// Package cloudtrail implements the AWS CloudTrail JSON 1.1 protocol as a
// server.Handler. Point the real aws-sdk-go-v2/service/cloudtrail client (or the
// `aws cloudtrail` CLI) at a Server registered with this handler and CloudTrail
// operations run against an in-memory CloudTrail driver.
//
// CloudTrail uses the AWS JSON 1.1 wire shape (POST + JSON body dispatched on
// the X-Amz-Target header, prefix "CloudTrail_20131101.").
package cloudtrail

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	ctdriver "github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

const targetPrefix = "CloudTrail_20131101."

// Handler serves CloudTrail JSON-RPC requests against a CloudTrail driver.
type Handler struct {
	ct     ctdriver.CloudTrail
	routes map[string]http.HandlerFunc
}

// New returns a CloudTrail handler backed by d.
func New(d ctdriver.CloudTrail) *Handler {
	h := &Handler{ct: d}
	h.routes = map[string]http.HandlerFunc{
		"CreateTrail":    h.createTrail,
		"GetTrail":       h.getTrail,
		"UpdateTrail":    h.updateTrail,
		"DeleteTrail":    h.deleteTrail,
		"DescribeTrails": h.describeTrails,
		"ListTrails":     h.listTrails,
		"GetTrailStatus": h.getTrailStatus,
		"StartLogging":   h.startLogging,
		"StopLogging":    h.stopLogging,

		"PutEventSelectors":   h.putEventSelectors,
		"GetEventSelectors":   h.getEventSelectors,
		"PutInsightSelectors": h.putInsightSelectors,
		"GetInsightSelectors": h.getInsightSelectors,

		"CreateEventDataStore":         h.createEventDataStore,
		"GetEventDataStore":            h.getEventDataStore,
		"UpdateEventDataStore":         h.updateEventDataStore,
		"DeleteEventDataStore":         h.deleteEventDataStore,
		"RestoreEventDataStore":        h.restoreEventDataStore,
		"ListEventDataStores":          h.listEventDataStores,
		"StartEventDataStoreIngestion": h.startEDSIngestion,
		"StopEventDataStoreIngestion":  h.stopEDSIngestion,

		"CreateChannel": h.createChannel,
		"GetChannel":    h.getChannel,
		"UpdateChannel": h.updateChannel,
		"DeleteChannel": h.deleteChannel,
		"ListChannels":  h.listChannels,

		"CreateDashboard":       h.createDashboard,
		"GetDashboard":          h.getDashboard,
		"UpdateDashboard":       h.updateDashboard,
		"DeleteDashboard":       h.deleteDashboard,
		"ListDashboards":        h.listDashboards,
		"StartDashboardRefresh": h.startDashboardRefresh,

		"StartImport":        h.startImport,
		"GetImport":          h.getImport,
		"StopImport":         h.stopImport,
		"ListImports":        h.listImports,
		"ListImportFailures": h.listImportFailures,

		"StartQuery":      h.startQuery,
		"DescribeQuery":   h.describeQuery,
		"GetQueryResults": h.getQueryResults,
		"CancelQuery":     h.cancelQuery,
		"ListQueries":     h.listQueries,
		"GenerateQuery":   h.generateQuery,

		"PutResourcePolicy":    h.putResourcePolicy,
		"GetResourcePolicy":    h.getResourcePolicy,
		"DeleteResourcePolicy": h.deleteResourcePolicy,

		"PutEventConfiguration": h.putEventConfiguration,
		"GetEventConfiguration": h.getEventConfiguration,

		"EnableFederation":  h.enableFederation,
		"DisableFederation": h.disableFederation,

		"AddTags":    h.addTags,
		"RemoveTags": h.removeTags,
		"ListTags":   h.listTags,

		"RegisterOrganizationDelegatedAdmin":   h.registerOrgAdmin,
		"DeregisterOrganizationDelegatedAdmin": h.deregisterOrgAdmin,

		"LookupEvents":           h.lookupEvents,
		"ListPublicKeys":         h.listPublicKeys,
		"ListInsightsData":       h.listInsightsData,
		"ListInsightsMetricData": h.listInsightsMetricData,
		"SearchSampleQueries":    h.searchSampleQueries,
	}

	return h
}

// Matches returns true for CloudTrail-shaped requests (X-Amz-Target of
// "CloudTrail_20131101.<Operation>").
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches CloudTrail operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported CloudTrail operation: "+r.Header.Get("X-Amz-Target"))
}

// dispatch decodes a JSON request of type Req, invokes call, and writes the
// returned value as JSON (or maps the error).
func dispatch[Req any](
	h *Handler, w http.ResponseWriter, r *http.Request,
	call func(*Handler, context.Context, *Req) (any, error),
) {
	var req Req
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	out, err := call(h, r.Context(), &req)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, out)
}

// writeErr maps a driver error to the closest CloudTrail JSON error type. Errors
// tagged with a specific CloudTrail exception (via driver.APIError) take
// precedence so distinct exceptions surface as themselves.
func writeErr(w http.ResponseWriter, err error) {
	var apiErr *ctdriver.APIError
	if errors.As(err, &apiErr) {
		wire.WriteJSONError(w, http.StatusBadRequest, apiErr.Exception, err.Error())

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", err.Error())
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", err.Error())
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", err.Error())
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ConflictException", err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalFailureException", err.Error())
	}
}
