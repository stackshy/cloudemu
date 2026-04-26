// Package gcprest provides shared HTTP wire-format helpers for GCP Compute
// (and other GCP REST APIs) JSON handlers.
//
// GCP REST URLs share a common shape:
//
//	/compute/v1/projects/{project}/zones/{zone}/{type}
//	/compute/v1/projects/{project}/zones/{zone}/{type}/{name}
//	/compute/v1/projects/{project}/zones/{zone}/{type}/{name}/{action}
//	/compute/v1/projects/{project}/global/{type}
//	/compute/v1/projects/{project}/regions/{region}/{type}
//
// Mutating operations return Operation envelopes that real GCP SDKs poll on
// `selfLink` until status=DONE. Our mock returns DONE immediately.
package gcprest

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/errors"
)

// ContentType is the JSON content type used by all REST responses.
const ContentType = "application/json"

// MaxBodyBytes caps incoming request bodies. GCP Compute Insert bodies for an
// instance are typically a few KB; 1 MiB is plenty of headroom.
const MaxBodyBytes = 1 << 20

// BasePrefix is the URL prefix that identifies a GCP Compute API request.
const BasePrefix = "/compute/v1/"

// Scope values used in GCP REST URL paths.
const (
	ScopeZones   = "zones"
	ScopeRegions = "regions"
	ScopeGlobal  = "global"
)

// scopePairLen is the number of path segments consumed by a scope/{name} pair
// like zones/us-central1-a.
const scopePairLen = 2

// ResourcePath is a parsed GCP REST URL path.
type ResourcePath struct {
	Project      string
	Scope        string // "zones", "regions", "global"
	ScopeName    string // zone/region name; empty when Scope=="global"
	ResourceType string // e.g. "instances", "operations"
	ResourceName string // empty for collection paths
	Action       string // e.g. "start", "stop", "reset" — empty for resource ops
}

// ParsePath extracts GCP REST path components from urlPath. Returns ok=false
// when the path doesn't match the /compute/v1/projects/... shape.
func ParsePath(urlPath string) (ResourcePath, bool) {
	if !strings.HasPrefix(urlPath, BasePrefix) {
		return ResourcePath{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, BasePrefix), "/")
	if len(parts) < 2 || parts[0] != "projects" {
		return ResourcePath{}, false
	}

	rp := ResourcePath{Project: parts[1]}

	i := 2
	if i >= len(parts) {
		return rp, true
	}

	next, ok := parseScope(parts, i, &rp)
	if !ok {
		return ResourcePath{}, false
	}

	parseTrailing(parts, next, &rp)

	return rp, true
}

// parseScope reads the scope segment ("zones/{z}", "regions/{r}", or
// "global") into rp. Returns the next index to inspect and ok=false on a
// malformed scope.
func parseScope(parts []string, i int, rp *ResourcePath) (int, bool) {
	switch parts[i] {
	case ScopeZones, ScopeRegions:
		if i+1 >= len(parts) {
			return i, false
		}

		rp.Scope = parts[i]
		rp.ScopeName = parts[i+1]

		return i + scopePairLen, true
	case ScopeGlobal:
		rp.Scope = ScopeGlobal

		return i + 1, true
	default:
		return i, false
	}
}

// parseTrailing records {type} / {name} / {action} segments if present.
func parseTrailing(parts []string, i int, rp *ResourcePath) {
	if i < len(parts) {
		rp.ResourceType = parts[i]
		i++
	}

	if i < len(parts) {
		rp.ResourceName = parts[i]
		i++
	}

	if i < len(parts) {
		rp.Action = parts[i]
	}
}

// WriteJSON writes v as a JSON response with status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck // best-effort response
}

// errorEnvelope is the GCP error response shape.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    int           `json:"code"`
	Message string        `json:"message"`
	Errors  []errorDetail `json:"errors,omitempty"`
	Status  string        `json:"status,omitempty"`
}

type errorDetail struct {
	Message string `json:"message"`
	Reason  string `json:"reason"`
}

// WriteError writes a GCP-style JSON error response.
func WriteError(w http.ResponseWriter, status int, reason, msg string) {
	WriteJSON(w, status, errorEnvelope{
		Error: errorBody{
			Code:    status,
			Message: msg,
			Status:  reason,
			Errors:  []errorDetail{{Message: msg, Reason: reason}},
		},
	})
}

// WriteCErr maps a CloudEmu canonical error to the matching GCP HTTP status
// and reason.
func WriteCErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		WriteError(w, http.StatusNotFound, "notFound", err.Error())
	case cerrors.IsAlreadyExists(err):
		WriteError(w, http.StatusConflict, "alreadyExists", err.Error())
	case cerrors.IsInvalidArgument(err):
		WriteError(w, http.StatusBadRequest, "invalid", err.Error())
	case cerrors.IsFailedPrecondition(err):
		WriteError(w, http.StatusConflict, "conditionNotMet", err.Error())
	default:
		WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
	}
}

// DecodeJSON reads a JSON request body into v. Returns false (and writes a
// 400) on decode error.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return false
	}

	return true
}

// SelfLink returns the canonical self-link for a GCP resource at the given
// scope. SDKs use this for polling and resource navigation.
func SelfLink(host, project, scope, scopeName, resourceType, name string) string {
	host = strings.TrimSuffix(host, "/")
	if scope == ScopeGlobal {
		return host + "/compute/v1/projects/" + project + "/global/" + resourceType + "/" + name
	}

	return host + "/compute/v1/projects/" + project + "/" + scope + "/" + scopeName + "/" + resourceType + "/" + name
}

// Operation models the subset of GCP's compute#operation we need. Real ops
// are async; our mock returns DONE immediately so SDK clients that poll see
// completion on the first GET.
type Operation struct {
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	OperationType string `json:"operationType"`
	TargetID      string `json:"targetId,omitempty"`
	TargetLink    string `json:"targetLink,omitempty"`
	Status        string `json:"status"`
	Progress      int    `json:"progress"`
	InsertTime    string `json:"insertTime"`
	StartTime     string `json:"startTime"`
	EndTime       string `json:"endTime"`
	SelfLink      string `json:"selfLink"`
	Zone          string `json:"zone,omitempty"`
}

// NewDoneOperation builds an Operation in DONE state for opType targeting the
// resource at scope/scopeName/resourceType/name. host is the test server URL
// so selfLink/targetLink are absolute and SDKs can navigate them.
//
// Operation.ID must be a numeric string (uint64) — GCP's protobuf JSON
// unmarshaling rejects anything else. The human-readable identifier goes in
// Name instead.
func NewDoneOperation(host, project, scope, scopeName, resourceType, name, opType string) Operation {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	opName := "operation-" + name + "-" + opType
	op := Operation{
		Kind:          "compute#operation",
		ID:            strconv.FormatInt(time.Now().UnixNano(), 10),
		Name:          opName,
		OperationType: opType,
		TargetLink:    SelfLink(host, project, scope, scopeName, resourceType, name),
		Status:        "DONE",
		Progress:      100,
		InsertTime:    now,
		StartTime:     now,
		EndTime:       now,
		SelfLink:      SelfLink(host, project, scope, scopeName, "operations", opName),
	}

	if scope == ScopeZones {
		op.Zone = strings.TrimSuffix(host, "/") + "/compute/v1/projects/" + project + "/zones/" + scopeName
	}

	return op
}
