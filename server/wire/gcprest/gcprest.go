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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// OperationRegistry stores the compute#operation records the compute-family
// handlers (compute, networks/vpc, load balancing) mint, so a later
// zone/region/global operations get, wait, list or delete reads back the
// operation as it was issued (same id, operationType and targetLink), matching
// real GCP, and 404s a name that was never issued. Like GCP, which keeps
// operations only for a limited time, it retains the most recent
// MaxOperationsPerScope operations per project and scope and drops the oldest.
// A nil *OperationRegistry stores nothing.
type OperationRegistry struct {
	mu      sync.RWMutex
	ops     map[string]Operation
	buckets map[string]*opBucket
}

// MaxOperationsPerScope caps the operations retained per project and scope.
const MaxOperationsPerScope = 1000

// opBucket is the creation-ordered operation names of one project and scope.
// names[head:] may hold names already deleted; live counts the stored ones.
type opBucket struct {
	project, scope, scopeName string
	names                     []string
	head, live                int
}

// NewOperationRegistry returns an empty operation registry.
func NewOperationRegistry() *OperationRegistry {
	return &OperationRegistry{ops: map[string]Operation{}, buckets: map[string]*opBucket{}}
}

// opKey scopes an operation name by the project and URL scope it is polled
// under, so operations of different projects or scopes stay distinct.
func opKey(project, scope, scopeName, name string) string {
	return project + "\x00" + scope + "\x00" + scopeName + "\x00" + name
}

// Get returns the operation stored under name in project at scope/scopeName.
func (reg *OperationRegistry) Get(project, scope, scopeName, name string) (Operation, bool) {
	if reg == nil {
		return Operation{}, false
	}

	reg.mu.RLock()
	defer reg.mu.RUnlock()

	op, ok := reg.ops[opKey(project, scope, scopeName, name)]

	return op, ok
}

// List returns the operations stored in project in creation order. An empty
// scope returns every scope (the aggregated list); otherwise only
// scope/scopeName.
func (reg *OperationRegistry) List(project, scope, scopeName string) []Operation {
	if reg == nil {
		return nil
	}

	reg.mu.RLock()
	defer reg.mu.RUnlock()

	var out []Operation

	for _, b := range reg.buckets {
		if b.project != project || (scope != "" && (b.scope != scope || b.scopeName != scopeName)) {
			continue
		}

		for _, name := range b.names[b.head:] {
			if op, ok := reg.ops[opKey(b.project, b.scope, b.scopeName, name)]; ok {
				out = append(out, op)
			}
		}
	}

	return out
}

// Delete removes the operation stored under name, reporting whether it existed.
func (reg *OperationRegistry) Delete(project, scope, scopeName, name string) bool {
	if reg == nil {
		return false
	}

	key := opKey(project, scope, scopeName, name)

	reg.mu.Lock()
	defer reg.mu.Unlock()

	if _, ok := reg.ops[key]; !ok {
		return false
	}

	delete(reg.ops, key)

	bkey := opKey(project, scope, scopeName, "")
	b := reg.buckets[bkey]
	b.live--

	if b.live == 0 {
		delete(reg.buckets, bkey)
	} else {
		reg.compact(b)
	}

	return true
}

// compact rebuilds b.names without its consumed prefix and deleted names once
// those dominate, so a create and delete loop cannot grow the slice without
// bound. The caller holds reg.mu.
func (reg *OperationRegistry) compact(b *opBucket) {
	const slack = 16

	if len(b.names)-b.head <= 2*b.live+slack {
		return
	}

	kept := make([]string, 0, b.live)

	for _, name := range b.names[b.head:] {
		if _, ok := reg.ops[opKey(b.project, b.scope, b.scopeName, name)]; ok {
			kept = append(kept, name)
		}
	}

	b.names, b.head = kept, 0
}

// store records op and evicts the scope's oldest operations beyond
// MaxOperationsPerScope. The caller holds reg.mu.
func (reg *OperationRegistry) store(project, scope, scopeName string, op *Operation) {
	bkey := opKey(project, scope, scopeName, "")

	b := reg.buckets[bkey]
	if b == nil {
		b = &opBucket{project: project, scope: scope, scopeName: scopeName}
		reg.buckets[bkey] = b
	}

	reg.ops[opKey(project, scope, scopeName, op.Name)] = *op
	b.names = append(b.names, op.Name)
	b.live++

	for b.live > MaxOperationsPerScope {
		key := opKey(project, scope, scopeName, b.names[b.head])
		b.head++

		if _, ok := reg.ops[key]; ok {
			delete(reg.ops, key)

			b.live--
		}
	}

	// Drop the consumed prefix once it is half the slice, so the slice stays
	// bounded and each insert is amortized O(1).
	if b.head > len(b.names)/2 {
		b.names = append([]string(nil), b.names[b.head:]...)
		b.head = 0
	}

	reg.compact(b)
}

// RecordDone builds a DONE operation for a mutation (via NewDoneOperation) and
// stores it so a later poll reads it back unchanged. A nil registry still
// returns the operation but stores nothing.
func (reg *OperationRegistry) RecordDone(
	host, project, scope, scopeName, resourceType, name, opType string,
) Operation {
	return reg.RecordDoneTarget(host, project, scope, scopeName, resourceType, name, "", opType)
}

// RecordDoneTarget is RecordDone with the target resource's numeric id, which
// real GCP reports as the operation's targetId.
func (reg *OperationRegistry) RecordDoneTarget(
	host, project, scope, scopeName, resourceType, name, targetID, opType string,
) Operation {
	op := NewDoneOperation(host, project, scope, scopeName, resourceType, name, opType)
	op.TargetID = targetID

	if reg == nil {
		return op
	}

	reg.mu.Lock()
	reg.store(project, scope, scopeName, &op)
	reg.mu.Unlock()

	return op
}

// ContentType is the JSON content type used by all REST responses.
const ContentType = "application/json"

// MaxBodyBytes caps incoming request bodies. GCP Compute Insert bodies for an
// instance are typically a few KB; 1 MiB is plenty of headroom.
const MaxBodyBytes = 1 << 20

// BasePrefix is the URL prefix that identifies a GCP Compute API request.
const BasePrefix = "/compute/v1/"

// Scope values used in GCP REST URL paths.
const (
	ScopeZones      = "zones"
	ScopeRegions    = "regions"
	ScopeGlobal     = "global"
	ScopeAggregated = "aggregated"
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
	Action       string // e.g. "start", "stop", "reset"; empty for resource ops
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
	case ScopeAggregated:
		rp.Scope = ScopeAggregated

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

// google.rpc.Code enum NAMEs used by the mapping below. Kept as named
// constants because they are referenced from both canonicalStatus (as the
// mapped result) and isCanonicalCode (as the already-canonical passthrough set).
const (
	codeInvalidArgument    = "INVALID_ARGUMENT"
	codeNotFound           = "NOT_FOUND"
	codeAlreadyExists      = "ALREADY_EXISTS"
	codeFailedPrecondition = "FAILED_PRECONDITION"
	codeAborted            = "ABORTED"
	codePermissionDenied   = "PERMISSION_DENIED"
	codeResourceExhausted  = "RESOURCE_EXHAUSTED"
	codeUnimplemented      = "UNIMPLEMENTED"
	codeUnavailable        = "UNAVAILABLE"
	codeInternal           = "INTERNAL"
)

// canonicalStatus maps a Google JSON-API error `reason` (the camelCase token
// carried in errors[].reason, e.g. "notFound") to the canonical
// google.rpc.Code enum NAME real GCP returns in the top-level `status` field
// (all-caps SNAKE_CASE, e.g. "NOT_FOUND"). See
// https://cloud.google.com/apis/design/errors and the google.rpc.Code enum.
// Reasons without a canonical code (e.g. HTTP 405 "methodNotAllowed", which has
// no google.rpc.Code) return "" so the omitempty `status` field is dropped,
// matching real GCP, which omits `status` for those responses.
func canonicalStatus(reason string) string {
	// Some callers (e.g. eventarc, fcm, vertexai) already pass a canonical
	// google.rpc.Code NAME as the reason. Return it unchanged so a canonical
	// input is never silently dropped by the camelCase mapping below.
	if isCanonicalCode(reason) {
		return reason
	}

	return camelReasonToCode(reason)
}

// camelReasonToCode maps a camelCase JSON-API reason token to its canonical
// google.rpc.Code NAME, or "" when the reason has no canonical code.
func camelReasonToCode(reason string) string {
	switch reason {
	case "notFound":
		return codeNotFound
	case "alreadyExists":
		return codeAlreadyExists
	case "invalid", "invalidArgument", "badRequest", "required":
		return codeInvalidArgument
	case "conditionNotMet", "failedPrecondition", "resourceInUseByAnotherResource",
		"containerNotEmpty", "cnameResourceRecordSetConflict":
		return codeFailedPrecondition
	case "aborted":
		return codeAborted
	case "forbidden":
		return codePermissionDenied
	case "rateLimitExceeded":
		return codeResourceExhausted
	case "notImplemented":
		return codeUnimplemented
	case "backendError":
		return codeUnavailable
	case "internalError":
		return codeInternal
	default:
		return ""
	}
}

// isCanonicalCode reports whether s is already one of the google.rpc.Code enum
// NAMEs (all-caps SNAKE_CASE). The set is explicit so a typo'd or garbage
// reason cannot pass through as if it were canonical. See
// https://github.com/googleapis/googleapis/blob/master/google/rpc/code.proto.
func isCanonicalCode(s string) bool {
	switch s {
	//nolint:misspell // google.rpc.Code enum name is CANCELLED (two Ls)
	case "OK", "CANCELLED", "UNKNOWN", codeInvalidArgument, "DEADLINE_EXCEEDED",
		codeNotFound, codeAlreadyExists, codePermissionDenied, codeResourceExhausted,
		codeFailedPrecondition, codeAborted, "OUT_OF_RANGE", codeUnimplemented,

		codeInternal, codeUnavailable, "DATA_LOSS", "UNAUTHENTICATED":
		return true
	default:
		return false
	}
}

// WriteError writes a GCP-style JSON error response. reason is the Google
// JSON-API camelCase token surfaced in errors[].reason; the top-level `status`
// field carries the canonical google.rpc.Code NAME derived from it.
func WriteError(w http.ResponseWriter, status int, reason, msg string) {
	WriteJSON(w, status, errorEnvelope{
		Error: errorBody{
			Code:    status,
			Message: msg,
			Status:  canonicalStatus(reason),
			Errors:  []errorDetail{{Message: msg, Reason: reason}},
		},
	})
}

// WriteCErr maps a CloudEmu canonical error to the matching GCP HTTP status
// and reason. The wire message is cerrors.Message(err): the error's
// human-readable text without the canonical code prefix (e.g. "instance x not
// found", not "NotFound: instance x not found"), matching every other cloud's
// wire handlers, which never leak the internal error-taxonomy name into the
// message an SDK surfaces to the caller.
func WriteCErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	switch {
	case cerrors.IsNotFound(err):
		WriteError(w, http.StatusNotFound, "notFound", msg)
	case cerrors.IsAlreadyExists(err):
		WriteError(w, http.StatusConflict, "alreadyExists", msg)
	case cerrors.IsInvalidArgument(err):
		WriteError(w, http.StatusBadRequest, "invalid", msg)
	case cerrors.IsFailedPrecondition(err):
		WriteError(w, http.StatusConflict, "conditionNotMet", msg)
	default:
		WriteError(w, http.StatusInternalServerError, "internalError", msg)
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

// OperationUser is the principal reported as an operation's user. The
// emulator does not authenticate callers, so every operation carries this one.
const OperationUser = "cloudemu@example.com"

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
	User          string `json:"user,omitempty"`
	Status        string `json:"status"`
	Progress      int    `json:"progress"`
	InsertTime    string `json:"insertTime"`
	StartTime     string `json:"startTime"`
	EndTime       string `json:"endTime"`
	SelfLink      string `json:"selfLink"`
	Zone          string `json:"zone,omitempty"`
	Region        string `json:"region,omitempty"`
}

// NewDoneOperation builds an Operation in DONE state for opType targeting the
// resource at scope/scopeName/resourceType/name. host is the test server URL
// so selfLink/targetLink are absolute and SDKs can navigate them.
//
// Operation.ID must be a numeric string (uint64): GCP's protobuf JSON
// unmarshaling rejects anything else. The human-readable identifier goes in
// Name instead.
func NewDoneOperation(host, project, scope, scopeName, resourceType, name, opType string) Operation {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	opName := newOpName()
	op := Operation{
		Kind:          "compute#operation",
		ID:            strconv.FormatInt(time.Now().UnixNano(), 10),
		Name:          opName,
		OperationType: opType,
		TargetLink:    SelfLink(host, project, scope, scopeName, resourceType, name),
		User:          OperationUser,
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

	if scope == ScopeRegions {
		op.Region = strings.TrimSuffix(host, "/") + "/compute/v1/projects/" + project + "/regions/" + scopeName
	}

	return op
}

// newOpName returns an operation name in real GCE's shape,
// "operation-<unixMilli>-<8 hex>", so two operations never share a name.
func newOpName() string {
	b := make([]byte, 4) //nolint:mnd // 8 hex digits
	if _, err := rand.Read(b); err != nil {
		return "operation-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}

	return "operation-" + strconv.FormatInt(time.Now().UnixMilli(), 10) + "-" + hex.EncodeToString(b)
}

// DefaultListMax is GCP's default list page size when maxResults is absent.
const DefaultListMax = 500

// NameMatches reports whether name satisfies a GCP list filter. Only the common
// single-clause "name (=|!=|eq|ne) value" form is supported; any other filter
// (or none) matches everything, matching real GCP's lenient behavior for the
// filter shapes the emulator does not model.
func NameMatches(filter, name string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}

	for _, cand := range []string{"!=", "=", " ne ", " eq "} {
		idx := strings.Index(filter, cand)
		if idx < 0 {
			continue
		}

		field := strings.TrimSpace(filter[:idx])
		if field != "name" {
			return true
		}

		value := strings.Trim(strings.TrimSpace(filter[idx+len(cand):]), `"'`)
		op := strings.TrimSpace(cand)
		negate := op == "!=" || op == "ne"

		return (name == value) != negate
	}

	return true
}

// MaxResults parses the maxResults query param, defaulting to DefaultListMax
// when absent, non-numeric, or non-positive.
func MaxResults(raw string) int {
	if raw == "" {
		return DefaultListMax
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return DefaultListMax
	}

	return n
}
