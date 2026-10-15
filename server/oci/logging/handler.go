// Package logging implements OCI's Logging REST API against a CloudEmu
// logging driver. Real github.com/oracle/oci-go-sdk logging, loggingingestion
// and loggingsearch clients hit this handler the same way they hit
// logging.<region>.oci.oraclecloud.com and its ingestion and search siblings.
//
// OCI splits the service across three API surfaces, each with its own version
// prefix. CloudEmu collapses them onto one server, so Matches claims each
// prefix's collections exactly and nothing else:
//
//	logging — the control plane, /20200531
//	  POST/GET             /20200531/logGroups                        — create, list
//	  GET/PUT/DELETE       /20200531/logGroups/{logGroupId}           — get, update, delete
//	  POST                 /20200531/logGroups/{logGroupId}/actions/changeCompartment
//	  POST/GET             /20200531/logGroups/{logGroupId}/logs      — create, list
//	  GET/PUT/DELETE       /20200531/logGroups/{logGroupId}/logs/{logId}
//	  POST                 /20200531/logGroups/{logGroupId}/logs/{logId}/actions/changeLogGroup
//
//	loggingingestion — the data plane, /20200831
//	  POST                 /20200831/logs/{logId}/actions/push        — PutLogs
//
//	loggingsearch — the query plane, /20190909
//	  POST                 /20190909/search                           — SearchLogs
//
// A log lives at a top-level /logs collection only under the ingestion prefix;
// the control plane nests it under its log group. That is what keeps the two
// apart, and Matches claims /logs for /20200831 alone.
//
// Not emulated: /20200531/unifiedAgentConfigurations and
// /20200531/logSavedSearches, which the logging driver has no shape for — the
// handler claims them so a caller gets a 501 naming the gap rather than a bare
// 404. Log groups and logs report ACTIVE from the moment they are created:
// every CloudEmu mutation is synchronous, so the CREATING state an SDK waiter
// may poll for is never observable.
package logging

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	logprovider "github.com/stackshy/cloudemu/v2/providers/oci/logging"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// The three API version prefixes OCI Logging is published under.
const (
	versionControl   = "20200531"
	versionIngestion = "20200831"
	versionSearch    = "20190909"
)

// Collections this handler claims.
const (
	segLogGroups      = "logGroups"
	segLogs           = "logs"
	segSearch         = "search"
	segUnifiedAgent   = "unifiedAgentConfigurations"
	segSavedSearches  = "logSavedSearches"
	subActions        = "actions"
	actionChangeComp  = "changeCompartment"
	actionChangeLogGp = "changeLogGroup"
	actionPush        = "push"
	entityTypeGroup   = "loggroup"
	entityTypeLog     = "log"
	specVersionOCI    = "1.0"
)

// Work request operations the asynchronous mutations record.
const (
	operationCreateGroup = "CREATE_LOG_GROUP"
	operationUpdateGroup = "UPDATE_LOG_GROUP"
	operationDeleteGroup = "DELETE_LOG_GROUP"
	operationMoveGroup   = "MOVE_LOG_GROUP"
	operationCreateLog   = "CREATE_LOG"
	operationUpdateLog   = "UPDATE_LOG"
	operationDeleteLog   = "DELETE_LOG"
	operationMoveLog     = "MOVE_LOG"
)

// OCI error codes the handler raises itself.
const (
	codeInvalidParameter = "InvalidParameter"
	codeMethodNotAllowed = "MethodNotAllowed"
	codeNotImplemented   = "NotImplemented"
	codeNotFound         = "NotAuthorizedOrNotFound"
)

// maxPathSegments is /{version}/{collection}/{id}/{sub}/{subId}/actions/{action},
// the shape of an action on a log nested under its group.
const maxPathSegments = 7

// Extras is the OCI-only surface the portable logging driver cannot express:
// log groups and logs are addressed by OCID inside a compartment, a log
// carries a type and a service source, ingestion batches entries, and search
// speaks OCI's own query language.
// *providers/oci/logging.Mock satisfies it; any driver that does not is served
// 501 for every path this handler claims.
type Extras interface {
	CreateGroup(ctx context.Context, spec logprovider.LogGroupSpec) (*logprovider.LogGroup, error)
	GetGroup(ctx context.Context, id string) (*logprovider.LogGroup, error)
	ListGroups(ctx context.Context, compartmentID, displayName string) ([]logprovider.LogGroup, error)
	UpdateGroup(ctx context.Context, id string, u logprovider.LogGroupUpdate) (*logprovider.LogGroup, error)
	DeleteGroup(ctx context.Context, id string) error
	MoveGroup(ctx context.Context, id, compartmentID string) error

	CreateLog(ctx context.Context, groupID string, spec logprovider.LogSpec) (*logprovider.Log, error)
	GetLog(ctx context.Context, groupID, logID string) (*logprovider.Log, error)
	ListLogs(ctx context.Context, groupID string, f logprovider.LogFilter) ([]logprovider.Log, error)
	UpdateLog(ctx context.Context, groupID, logID string, u logprovider.LogUpdate) (*logprovider.Log, error)
	DeleteLog(ctx context.Context, groupID, logID string) error
	MoveLog(ctx context.Context, groupID, logID, targetGroupID string) error

	PutLogs(ctx context.Context, logID string, batches []logprovider.LogEntryBatch) error
	SearchLogs(ctx context.Context, req logprovider.SearchRequest) (*logprovider.SearchResult, error)
}

// Handler serves OCI Logging against a logging driver.
type Handler struct {
	extras Extras
	work   *workrequest.Store

	// compartmentExists reports whether a compartment OCID exists. It is nil
	// unless SetCompartmentChecker wires it from Identity; a nil checker skips
	// the check so handlers built without identity keep working.
	compartmentExists func(id string) bool
}

// New returns a Logging handler. work records the asynchronous log group and
// log mutations; a nil store leaves those paths unserved.
func New(l logdriver.Logging, work *workrequest.Store) *Handler {
	extras, _ := l.(Extras)

	return &Handler{extras: extras, work: work}
}

// SetCompartmentChecker wires a compartment-existence check so a create or a
// move into a compartment that does not exist is rejected with 404
// NotAuthorizedOrNotFound, as real OCI does. When unset (nil) the check is
// skipped, so handlers constructed without identity keep working.
func (h *Handler) SetCompartmentChecker(fn func(id string) bool) { h.compartmentExists = fn }

// requireCompartment reports whether compartmentID exists; if not it writes
// the OCI 404 NotAuthorizedOrNotFound and returns false. A nil checker (no
// identity wired) is a no-op that allows the request.
func (h *Handler) requireCompartment(w http.ResponseWriter, r *http.Request, compartmentID string) bool {
	if h.compartmentExists == nil || compartmentID == "" || h.compartmentExists(compartmentID) {
		return true
	}

	ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, compartmentID+" not found")

	return false
}

// route is a parsed Logging path.
type route struct {
	Version    string
	Collection string
	ID         string
	Sub        string
	SubID      string
	// SubActions and SubAction address an action on a nested resource, as in
	// /logGroups/{id}/logs/{logId}/actions/changeLogGroup.
	SubActions string
	SubAction  string
}

// Matches claims each of the three Logging API surfaces at its own version
// prefix, and nothing else. A top-level /logs collection belongs to the
// ingestion plane alone — the control plane nests logs under their group.
func (*Handler) Matches(r *http.Request) bool {
	rt, ok := parsePath(r.URL.Path)
	if !ok {
		return false
	}

	switch rt.Version {
	case versionControl:
		return rt.Collection == segLogGroups ||
			rt.Collection == segUnifiedAgent ||
			rt.Collection == segSavedSearches
	case versionIngestion:
		return rt.Collection == segLogs
	case versionSearch:
		return rt.Collection == segSearch && rt.ID == ""
	default:
		return false
	}
}

// ServeHTTP routes on the API surface the path names, then on path shape and
// method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parsePath(r.URL.Path)
	if !ok {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "malformed logging path")
		return
	}

	if h.extras == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
			"the wired logging driver does not implement OCI log groups, ingestion and search")

		return
	}

	switch rt.Version {
	case versionControl:
		h.serveControlPlane(w, r, &rt)
	case versionIngestion:
		h.servePush(w, r, &rt)
	case versionSearch:
		h.serveSearch(w, r)
	default:
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown logging API version "+rt.Version)
	}
}

// serveControlPlane dispatches the log group and log collections.
func (h *Handler) serveControlPlane(w http.ResponseWriter, r *http.Request, rt *route) {
	switch rt.Collection {
	case segLogGroups:
		h.serveLogGroups(w, r, rt)
	case segUnifiedAgent, segSavedSearches:
		unemulated(w, r, rt.Collection)
	default:
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown collection "+rt.Collection)
	}
}

// serveLogGroups routes the log group collection, the actions on one group,
// and the logs nested under it.
func (h *Handler) serveLogGroups(w http.ResponseWriter, r *http.Request, rt *route) {
	switch {
	case rt.ID != "" && rt.Sub == segLogs:
		h.serveLogs(w, r, rt)
	case rt.ID != "" && rt.Sub == subActions:
		h.serveGroupAction(w, r, rt)
	case rt.Sub != "":
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown sub-collection "+rt.Sub)
	default:
		h.serveGroupCRUD(w, r, rt)
	}
}

// unemulated reports a collection the handler claims but cannot serve. The
// logging driver models no unified monitoring agent and no saved search, so
// both would be shapes with nothing behind them.
func unemulated(w http.ResponseWriter, r *http.Request, collection string) {
	ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
		collection+" is not emulated; CloudEmu models log groups, logs, ingestion and search")
}

// accept records a work request for an asynchronous mutation and answers 202,
// which is what real OCI returns from every log group and log mutation.
func (h *Handler) accept(
	w http.ResponseWriter, r *http.Request, operation, compartmentID, entityType, actionType, id string,
) {
	wrID := h.work.Accept(operation, compartmentID, workrequest.Resource{
		EntityType: entityType,
		ActionType: actionType,
		Identifier: id,
	})

	ocirest.SetWorkRequestID(w, wrID)
	ocirest.WriteJSON(w, r, http.StatusAccepted, nil)
}

// requireWork reports whether asynchronous mutations can be served at all.
func (h *Handler) requireWork(w http.ResponseWriter, r *http.Request) bool {
	if h.work == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented, "work requests are not configured")
		return false
	}

	return true
}

// parsePath splits /{version}/{collection}[/{id}[/{sub}[/{subId}[/actions/{action}]]]].
func parsePath(urlPath string) (route, bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) < 2 || len(parts) > maxPathSegments {
		return route{}, false
	}

	for _, part := range parts {
		if part == "" {
			return route{}, false
		}
	}

	rt := route{Version: parts[0], Collection: parts[1]}

	if len(parts) > 2 { //nolint:mnd // the id follows the collection
		rt.ID = parts[2]
	}

	if len(parts) > 3 { //nolint:mnd // then the sub-collection
		rt.Sub = parts[3]
	}

	if len(parts) > 4 { //nolint:mnd // then the resource or action on it
		rt.SubID = parts[4]
	}

	if len(parts) > 5 { //nolint:mnd // then an action on the nested resource
		rt.SubActions = parts[5]
	}

	if len(parts) > 6 { //nolint:mnd // and the action's name
		rt.SubAction = parts[6]
	}

	return rt, true
}

// pageOffset reads OCI's opaque page cursor, which CloudEmu mints as the
// offset the next page starts at. An absent cursor is the first page; one
// CloudEmu did not mint is rejected rather than silently restarting at zero,
// which would loop a paginating client forever.
func pageOffset(w http.ResponseWriter, r *http.Request) (int, bool) {
	token := ocirest.Page(r)
	if token == "" {
		return 0, true
	}

	n, err := strconv.Atoi(token)
	if err != nil || n < 0 {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"page "+strconv.Quote(token)+" is not a cursor this API returned in opc-next-page")

		return 0, false
	}

	return n, true
}

// paginate applies OCI's limit and page cursor, stamping opc-next-page when
// more items follow.
func paginate[T any](w http.ResponseWriter, r *http.Request, items []T) ([]T, bool) {
	start, ok := pageOffset(w, r)
	if !ok {
		return nil, false
	}

	// items[:0] rather than nil: an empty page is [] on the wire, not null.
	if start >= len(items) {
		return items[:0], true
	}

	end := min(start+ocirest.Limit(r), len(items))
	if end < len(items) {
		ocirest.SetNextPage(w, strconv.Itoa(end))
	}

	return items[start:end], true
}

// methodNotAllowed is the response for a verb a collection does not serve.
func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	ocirest.WriteError(w, r, http.StatusMethodNotAllowed, codeMethodNotAllowed, "method not allowed")
}
