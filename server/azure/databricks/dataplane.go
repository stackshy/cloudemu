// Package databricks also implements the Databricks workspace data-plane REST
// API (the /api/2.x surface served at the workspace URL) as a server.Handler,
// distinct from the ARM workspace-management handler in this package. Point the
// real github.com/databricks/databricks-sdk-go WorkspaceClient at a server
// registered with DataPlaneHandler and clusters, instance pools, jobs, and
// permissions work end-to-end against an in-memory backend.
//
// Covered endpoints:
//
//	POST/GET /api/2.0/instance-pools/{create,get,list,edit,delete}
//	POST/GET /api/2.1/clusters/{create,get,list,edit,delete,permanent-delete,start,restart}
//	POST/GET /api/2.2/jobs/{create,get,list,update,reset,delete,run-now}
//	GET/PUT/PATCH /api/2.0/permissions/{object_type}/{object_id}
package databricks

import (
	"encoding/json"
	"net/http"
	"strings"

	dbxdriver "github.com/stackshy/cloudemu/databricks/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
)

const dpMaxBodyBytes = 5 << 20

const (
	resPools       = "instance-pools"
	resClusters    = "clusters"
	resJobs        = "jobs"
	resPermissions = "permissions"
	resPolicies    = "policies"
	resLibraries   = "libraries"
)

// Sub-resource segments (e.g. /jobs/runs/..., /policies/clusters/...).
const (
	subRuns     = "runs"
	subClusters = "clusters"
)

// Data-plane action path segments.
const (
	actCreate          = "create"
	actGet             = "get"
	actList            = "list"
	actEdit            = "edit"
	actDelete          = "delete"
	actPermanentDelete = "permanent-delete"
	actStart           = "start"
	actRestart         = "restart"
	actUpdate          = "update"
	actReset           = "reset"
	actRunNow          = "run-now"
	actCancel          = "cancel"
	actGetOutput       = "get-output"
	actInstall         = "install"
	actUninstall       = "uninstall"
	actClusterStatus   = "cluster-status"
	actAllStatuses     = "all-cluster-statuses"
	actResize          = "resize"
	actPin             = "pin"
	actUnpin           = "unpin"
	actListNodeTypes   = "list-node-types"
	actSparkVersions   = "spark-versions"
	actListZones       = "list-zones"
	actSubmit          = "submit"
	actCancelAll       = "cancel-all"
	actRepair          = "repair"
)

// minSubResourceSegs is the [api, ver, resource, sub, action] segment count
// for nested paths like /jobs/runs/{action} and /policies/clusters/{action}.
const minSubResourceSegs = 5

// minResourceSegs is the [api, ver, resource, action] segment count after
// dpSplit; minPermissionsSegs additionally needs [type, id].
const (
	minResourceSegs    = 4
	minPermissionsSegs = 5
)

// dpRoute binds an action to its required HTTP method and handler.
type dpRoute struct {
	method string
	fn     func(http.ResponseWriter, *http.Request)
}

// DataPlaneHandler serves the Databricks data-plane REST API.
type DataPlaneHandler struct {
	dp dbxdriver.DataPlane
}

// NewDataPlane returns a data-plane handler backed by drv.
func NewDataPlane(drv dbxdriver.DataPlane) *DataPlaneHandler {
	return &DataPlaneHandler{dp: drv}
}

// dispatch looks up action in routes, enforces the method, and invokes it.
func dispatch(w http.ResponseWriter, r *http.Request, action string, routes map[string]dpRoute) {
	rt, ok := routes[action]
	if !ok {
		dpError(w, http.StatusNotFound, "ENDPOINT_NOT_FOUND", "unknown action: "+action)

		return
	}

	if r.Method != rt.method {
		dpMethodNotAllowed(w)

		return
	}

	rt.fn(w, r)
}

// Matches claims /api/{ver}/{instance-pools,clusters,jobs,permissions}/... paths.
func (*DataPlaneHandler) Matches(r *http.Request) bool {
	parts := dpSplit(r.URL.Path)
	if len(parts) < minResourceSegs || parts[0] != "api" {
		return false
	}

	switch parts[2] {
	case resPools, resClusters, resJobs, resPermissions, resPolicies, resLibraries:
		return true
	default:
		return false
	}
}

// ServeHTTP routes by resource and action.
func (h *DataPlaneHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := dpSplit(r.URL.Path)
	if len(parts) < minResourceSegs {
		dpError(w, http.StatusNotFound, "ENDPOINT_NOT_FOUND", "unsupported path")

		return
	}

	resource, action := parts[2], parts[3]

	switch resource {
	case resPools:
		h.servePools(w, r, action)
	case resClusters:
		h.serveClusters(w, r, action)
	case resJobs:
		h.serveJobsResource(w, r, parts)
	case resPolicies:
		h.servePoliciesResource(w, r, parts)
	case resLibraries:
		h.serveLibraries(w, r, action)
	case resPermissions:
		h.servePermissions(w, r, parts)
	default:
		dpError(w, http.StatusNotFound, "ENDPOINT_NOT_FOUND", "unknown resource: "+resource)
	}
}

// serveJobsResource routes /jobs/{action} and the nested /jobs/runs/{action}.
func (h *DataPlaneHandler) serveJobsResource(w http.ResponseWriter, r *http.Request, parts []string) {
	if parts[3] == subRuns && len(parts) >= minSubResourceSegs {
		h.serveRuns(w, r, parts[4])

		return
	}

	h.serveJobs(w, r, parts[3])
}

// servePoliciesResource routes the nested /policies/clusters/{action}.
func (h *DataPlaneHandler) servePoliciesResource(w http.ResponseWriter, r *http.Request, parts []string) {
	if parts[3] == subClusters && len(parts) >= minSubResourceSegs {
		h.servePolicies(w, r, parts[4])

		return
	}

	dpError(w, http.StatusNotFound, "ENDPOINT_NOT_FOUND", "unknown policies path")
}

// dpSplit strips the leading "/api/" and returns [ver, resource, action, ...].
// Path /api/2.1/clusters/create → ["2.1","clusters","create"]; the leading
// "api" is kept at index 0 for the Matches/ServeHTTP guards.
func dpSplit(p string) []string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}

	return strings.Split(trimmed, "/")
}

func dpDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, dpMaxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		dpError(w, http.StatusBadRequest, "MALFORMED_REQUEST", "invalid JSON: "+err.Error())

		return false
	}

	return true
}

func dpJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

// dpErrorBody is the Databricks error envelope shape.
type dpErrorBody struct {
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

func dpError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(dpErrorBody{ErrorCode: code, Message: msg})
}

// dpWriteErr maps a cloudemu canonical error to a Databricks error response.
func dpWriteErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		dpError(w, http.StatusNotFound, "RESOURCE_DOES_NOT_EXIST", err.Error())
	case cerrors.IsAlreadyExists(err):
		dpError(w, http.StatusConflict, "RESOURCE_ALREADY_EXISTS", err.Error())
	case cerrors.IsInvalidArgument(err):
		dpError(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", err.Error())
	case cerrors.IsFailedPrecondition(err):
		dpError(w, http.StatusBadRequest, "INVALID_STATE", err.Error())
	default:
		dpError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
	}
}

func dpMethodNotAllowed(w http.ResponseWriter) {
	dpError(w, http.StatusMethodNotAllowed, "INVALID_PARAMETER_VALUE", "method not allowed")
}
