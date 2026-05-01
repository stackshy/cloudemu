// Package cloudfunctions implements the GCP Cloud Functions v1 REST API as
// a server.Handler. Real cloud.google.com/go/functions/apiv1 clients
// configured with a custom endpoint hit this handler the same way they hit
// cloudfunctions.googleapis.com.
//
// MVP coverage:
//
//	POST   /v1/projects/{p}/locations/{l}/functions             — Create (LRO)
//	GET    /v1/projects/{p}/locations/{l}/functions/{name}      — Get
//	GET    /v1/projects/{p}/locations/{l}/functions             — List
//	DELETE /v1/projects/{p}/locations/{l}/functions/{name}      — Delete (LRO)
//	POST   /v1/projects/{p}/locations/{l}/functions/{name}:call — Synchronous invoke
//	GET    /v1/operations/{op}                                  — Poll an LRO
//
// All mutating endpoints return Operation envelopes with done=true so SDK
// pollers terminate on the first response.
package cloudfunctions

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/errors"
	sdrv "github.com/stackshy/cloudemu/serverless/driver"
)

const (
	pathPrefix      = "/v1/projects/"
	functionsSeg    = "functions"
	locationsSeg    = "locations"
	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20
)

// Handler serves GCP Cloud Functions v1 REST requests against a serverless
// driver.
type Handler struct {
	fn sdrv.Serverless
}

// New returns a Cloud Functions handler backed by fn.
func New(fn sdrv.Serverless) *Handler {
	return &Handler{fn: fn}
}

// Matches accepts paths that look like Cloud Functions v1: either an LRO poll
// (/v1/operations/...) or a /v1/projects/{p}/locations/{l}/functions[/...]
// path. The locations+functions segment guards us from shadowing Firestore's
// /v1/projects/{p}/databases/... URLs.
func (*Handler) Matches(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/v1/operations/") {
		return true
	}

	if !strings.HasPrefix(r.URL.Path, pathPrefix) {
		return false
	}

	rest := strings.TrimPrefix(r.URL.Path, pathPrefix)

	// rest is "{project}/locations/{location}/functions[/...]"
	parts := strings.Split(rest, "/")

	const (
		idxScope = 1 // locations
		idxType  = 3 // functions
	)

	if len(parts) <= idxType {
		return false
	}

	if parts[idxScope] != locationsSeg {
		return false
	}

	// Strip ":action" suffix from the last segment for the type-equality check.
	typePart := parts[idxType]
	if i := strings.Index(typePart, ":"); i >= 0 {
		typePart = typePart[:i]
	}

	return typePart == functionsSeg
}

// ServeHTTP routes requests by URL shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/v1/operations/") {
		h.serveOperation(w, r)
		return
	}

	parts, ok := parseFunctionsPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unsupported path")
		return
	}

	if parts.action == "call" && parts.name != "" {
		h.serveCall(w, r, parts)
		return
	}

	if parts.name != "" {
		h.serveResource(w, r, parts)
		return
	}

	h.serveCollection(w, r, parts)
}

func (h *Handler) serveResource(w http.ResponseWriter, r *http.Request, p functionPath) {
	switch r.Method {
	case http.MethodGet:
		h.get(w, r, p)
	case http.MethodPatch:
		h.update(w, r, p)
	case http.MethodDelete:
		h.delete(w, r, p)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, p functionPath) {
	switch r.Method {
	case http.MethodPost:
		h.create(w, r, p)
	case http.MethodGet:
		h.list(w, r, p)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

// serveOperation answers GET /v1/operations/{name}. We always return done=true
// because mutations are synchronous in the mock; a poll is just an echo.
func (*Handler) serveOperation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	opName := strings.TrimPrefix(r.URL.Path, "/v1/")
	writeJSON(w, http.StatusOK, operation{Name: opName, Done: true})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request, p functionPath) {
	// Real Cloud Functions accepts the function name in either the body or as a
	// "?functionId=" query parameter. SDKs use the body.
	var body cloudFunction
	if !decodeJSON(w, r, &body) {
		return
	}

	name := lastSegment(body.Name)
	if name == "" {
		name = r.URL.Query().Get("functionId")
	}

	if name == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "function name required")
		return
	}

	cfg := sdrv.FunctionConfig{
		Name:        name,
		Runtime:     body.Runtime,
		Handler:     body.EntryPoint,
		Memory:      body.AvailableMemory,
		Tags:        body.Labels,
		Environment: body.EnvVariables,
		Timeout:     parseTimeoutSeconds(body.Timeout),
	}

	info, err := h.fn.CreateFunction(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	resource := toCloudFunction(info, p)

	writeJSON(w, http.StatusOK, operation{
		Name:     "operations/create-" + name + "-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Done:     true,
		Response: resourceAsResponse(resource, "CloudFunction"),
	})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, p functionPath) {
	info, err := h.fn.GetFunction(r.Context(), p.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toCloudFunction(info, p))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, p functionPath) {
	infos, err := h.fn.ListFunctions(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := listFunctionsResponse{Functions: make([]cloudFunction, 0, len(infos))}
	for i := range infos {
		out.Functions = append(out.Functions, toCloudFunction(&infos[i], p))
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request, p functionPath) {
	var body cloudFunction
	if !decodeJSON(w, r, &body) {
		return
	}

	cfg := sdrv.FunctionConfig{
		Name:        p.name,
		Runtime:     body.Runtime,
		Handler:     body.EntryPoint,
		Memory:      body.AvailableMemory,
		Tags:        body.Labels,
		Environment: body.EnvVariables,
		Timeout:     parseTimeoutSeconds(body.Timeout),
	}

	info, err := h.fn.UpdateFunction(r.Context(), p.name, cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	resource := toCloudFunction(info, p)
	writeJSON(w, http.StatusOK, operation{
		Name:     "operations/update-" + p.name,
		Done:     true,
		Response: resourceAsResponse(resource, "CloudFunction"),
	})
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request, p functionPath) {
	if err := h.fn.DeleteFunction(r.Context(), p.name); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, operation{
		Name: "operations/delete-" + p.name,
		Done: true,
	})
}

func (h *Handler) serveCall(w http.ResponseWriter, r *http.Request, p functionPath) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	var req callRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	out, err := h.fn.Invoke(r.Context(), sdrv.InvokeInput{
		FunctionName: p.name,
		Payload:      []byte(req.Data),
		InvokeType:   "RequestResponse",
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := callResponse{
		ExecutionID: strconv.FormatInt(time.Now().UnixNano(), 10),
	}

	if out.Error != "" {
		resp.Error = out.Error
	} else {
		resp.Result = string(out.Payload)
	}

	writeJSON(w, http.StatusOK, resp)
}

// functionPath holds the components of a Cloud Functions URL.
type functionPath struct {
	project  string
	location string
	name     string
	action   string // "call", etc.
}

// fullName returns the canonical resource name "projects/{p}/locations/{l}/functions/{n}".
func (p functionPath) fullName() string {
	return "projects/" + p.project + "/locations/" + p.location + "/functions/" + p.name
}

// parseFunctionsPath extracts components from a Cloud Functions v1 URL.
//
//	/v1/projects/{p}/locations/{l}/functions
//	/v1/projects/{p}/locations/{l}/functions/{name}
//	/v1/projects/{p}/locations/{l}/functions/{name}:{action}
func parseFunctionsPath(path string) (functionPath, bool) {
	rest := strings.TrimPrefix(path, pathPrefix)

	parts := strings.Split(rest, "/")

	const (
		minParts    = 4 // {project}/locations/{location}/functions
		idxProject  = 0
		idxScope    = 1
		idxLocation = 2
		idxType     = 3
		idxName     = 4
	)

	if len(parts) < minParts || parts[idxScope] != locationsSeg {
		return functionPath{}, false
	}

	typePart := parts[idxType]
	if typePart != functionsSeg {
		// Could be "functions:action" with no name on the collection.
		base, action, hasAction := splitColon(typePart)
		if !hasAction || base != functionsSeg {
			return functionPath{}, false
		}

		return functionPath{
			project: parts[idxProject], location: parts[idxLocation], action: action,
		}, true
	}

	out := functionPath{
		project:  parts[idxProject],
		location: parts[idxLocation],
	}

	if len(parts) > idxName {
		nameWithAction := strings.Join(parts[idxName:], "/")
		if base, action, ok := splitColon(nameWithAction); ok {
			out.name = base
			out.action = action
		} else {
			out.name = nameWithAction
		}
	}

	return out, true
}

func splitColon(s string) (base, action string, ok bool) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s, "", false
	}

	return s[:i], s[i+1:], true
}

func lastSegment(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}

	return name
}

func parseTimeoutSeconds(t string) int {
	t = strings.TrimSuffix(t, "s")

	n, err := strconv.Atoi(t)
	if err != nil {
		return 0
	}

	return n
}

func toCloudFunction(info *sdrv.FunctionInfo, p functionPath) cloudFunction {
	scope := p
	scope.name = info.Name

	cf := cloudFunction{
		Name:            scope.fullName(),
		Status:          "ACTIVE",
		Runtime:         info.Runtime,
		EntryPoint:      info.Handler,
		AvailableMemory: info.Memory,
		Labels:          info.Tags,
		EnvVariables:    info.Environment,
		UpdateTime:      info.LastModified,
		VersionID:       "1",
	}

	if info.Timeout > 0 {
		cf.Timeout = strconv.Itoa(info.Timeout) + "s"
	}

	return cf
}

//nolint:gocritic // cf is the response body shape; one copy per LRO response is fine.
func resourceAsResponse(cf cloudFunction, kind string) map[string]any {
	b, err := json.Marshal(cf)
	if err != nil {
		return nil
	}

	out := map[string]any{
		"@type": "type.googleapis.com/google.cloud.functions.v1." + kind,
	}

	var fields map[string]any
	_ = json.Unmarshal(b, &fields)

	for k, v := range fields {
		out[k] = v
	}

	return out
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON: "+err.Error())
		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, reason, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    status,
			"message": msg,
			"status":  reason,
		},
	})
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}
