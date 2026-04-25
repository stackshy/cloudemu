// Package azurearm provides shared HTTP wire-format helpers for Azure
// Resource Manager (ARM) JSON REST handlers.
//
// ARM URLs share a common shape:
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/{provider}/{type}/{name}
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/{provider}/{type}
//	/subscriptions/{sub}/providers/{provider}/{type}
//
// and an `api-version` query parameter is always present. Real ARM responses
// are JSON; errors carry an `error.code` / `error.message` envelope.
package azurearm

import (
	"encoding/json"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/errors"
)

// ContentType is the JSON content type used by all ARM responses.
const ContentType = "application/json"

// MaxBodyBytes caps incoming JSON request bodies. Real ARM PUT requests for a
// VM are ~3 KB; 1 MiB is plenty of headroom.
const MaxBodyBytes = 1 << 20

// indexPostSubscription is the part index just past /subscriptions/{sub}.
const indexPostSubscription = 2

// pairLen is the number of path segments consumed by a {key}/{value} pair
// such as resourceGroups/{rg}.
const pairLen = 2

// ResourcePath is a parsed ARM URL path. Fields are empty when not present in
// the path (e.g., a subscription-scoped list has no ResourceGroup).
type ResourcePath struct {
	Subscription  string
	ResourceGroup string
	Provider      string // e.g. "Microsoft.Compute"
	ResourceType  string // e.g. "virtualMachines"
	ResourceName  string // empty for collection paths
	SubResource   string // e.g. "start", "powerOff" — empty for resource ops
}

// ParsePath extracts the ARM path components from urlPath. Returns ok=false
// when the path doesn't look like an ARM resource URL.
//
// Recognized shapes:
//
//	/subscriptions/{sub}
//	/subscriptions/{sub}/resourceGroups/{rg}
//	/subscriptions/{sub}/providers/{prov}/{type}
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/{prov}/{type}
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/{prov}/{type}/{name}
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/{prov}/{type}/{name}/{subResource}
func ParsePath(urlPath string) (ResourcePath, bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) < 2 || parts[0] != "subscriptions" {
		return ResourcePath{}, false
	}

	rp := ResourcePath{Subscription: parts[1]}

	i := parseResourceGroup(parts, indexPostSubscription, &rp)
	if i >= len(parts) || parts[i] != "providers" {
		return rp, true
	}

	if i+2 >= len(parts) {
		return ResourcePath{}, false
	}

	rp.Provider = parts[i+1]
	rp.ResourceType = parts[i+2]
	i += 3

	parseTrailing(parts, i, &rp)

	return rp, true
}

// parseResourceGroup advances past an optional resourceGroups/{rg} pair and
// records the group on rp. Returns the next index to inspect.
func parseResourceGroup(parts []string, i int, rp *ResourcePath) int {
	if i >= len(parts) || parts[i] != "resourceGroups" {
		return i
	}

	if i+1 >= len(parts) {
		return i
	}

	rp.ResourceGroup = parts[i+1]

	return i + pairLen
}

// parseTrailing records {name} and {subResource} segments if present.
func parseTrailing(parts []string, i int, rp *ResourcePath) {
	if i < len(parts) {
		rp.ResourceName = parts[i]
		i++
	}

	if i < len(parts) {
		rp.SubResource = parts[i]
	}
}

// WriteJSON writes v as a JSON response with status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck // best-effort response
}

// errorEnvelope is the ARM error response shape.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteError writes an ARM-style JSON error response.
func WriteError(w http.ResponseWriter, status int, code, msg string) {
	WriteJSON(w, status, errorEnvelope{Error: errorBody{Code: code, Message: msg}})
}

// WriteCErr maps a CloudEmu canonical error to the matching ARM HTTP status
// and code. Used by handlers so error mapping is consistent across services.
func WriteCErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		WriteError(w, http.StatusNotFound, "ResourceNotFound", err.Error())
	case cerrors.IsAlreadyExists(err):
		WriteError(w, http.StatusConflict, "Conflict", err.Error())
	case cerrors.IsInvalidArgument(err):
		WriteError(w, http.StatusBadRequest, "InvalidParameter", err.Error())
	case cerrors.IsFailedPrecondition(err):
		WriteError(w, http.StatusConflict, "PreconditionFailed", err.Error())
	default:
		WriteError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}

// DecodeJSON reads a JSON request body into v. Returns false (and writes a
// 400) on decode error.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return false
	}

	return true
}

// BuildResourceID constructs an ARM resource ID for the given path components.
// Used in response bodies so SDKs receive properly-shaped IDs.
func BuildResourceID(subscription, resourceGroup, provider, resourceType, name string) string {
	return "/subscriptions/" + subscription +
		"/resourceGroups/" + resourceGroup +
		"/providers/" + provider +
		"/" + resourceType +
		"/" + name
}
