// Package cloudrun implements the GCP Cloud Run Admin API v2 REST surface as a
// server.Handler. Real cloud.google.com/go/run/apiv2 clients (and the gcloud
// CLI) configured with a custom endpoint hit this handler the same way they hit
// run.googleapis.com.
//
// Scope covers both surfaces: Jobs (run-to-completion) and Services (HTTP
// ingress / revisions / traffic).
//
// Coverage:
//
//	POST   /v2/projects/{p}/locations/{l}/jobs?jobId={id}                — Jobs.Create (LRO)
//	GET    /v2/projects/{p}/locations/{l}/jobs/{job}                     — Jobs.Get
//	GET    /v2/projects/{p}/locations/{l}/jobs                           — Jobs.List
//	PATCH  /v2/projects/{p}/locations/{l}/jobs/{job}                     — Jobs.Patch (LRO)
//	DELETE /v2/projects/{p}/locations/{l}/jobs/{job}                     — Jobs.Delete (LRO)
//	POST   /v2/projects/{p}/locations/{l}/jobs/{job}:run                 — Jobs.Run (LRO)
//	GET    /v2/projects/{p}/locations/{l}/jobs/{job}/executions          — Executions.List
//	GET    /v2/projects/{p}/locations/{l}/jobs/{job}/executions/{exec}   — Executions.Get
//	POST   /v2/projects/{p}/locations/{l}/jobs/{job}:{get,set}IamPolicy  — Jobs IAM
//	POST   /v2/projects/{p}/locations/{l}/services?serviceId={id}        — Services.Create (LRO)
//	GET    /v2/projects/{p}/locations/{l}/services/{svc}                 — Services.Get
//	GET    /v2/projects/{p}/locations/{l}/services                       — Services.List
//	PATCH  /v2/projects/{p}/locations/{l}/services/{svc}                 — Services.Patch (LRO)
//	DELETE /v2/projects/{p}/locations/{l}/services/{svc}                 — Services.Delete (LRO)
//	GET    /v2/projects/{p}/locations/{l}/services/{svc}/revisions[/{r}] — Revisions.{List,Get}
//	DELETE /v2/projects/{p}/locations/{l}/services/{svc}/revisions/{r}   — Revisions.Delete (LRO)
//	POST   /v2/projects/{p}/locations/{l}/services/{svc}:{get,set}IamPolicy — Services IAM
//	GET    /v2/projects/{p}/locations/{l}/operations/{op}                — Poll an LRO
//
// All mutating endpoints return google.longrunning.Operation envelopes with
// done=true so SDK pollers terminate on the first response.
package cloudrun

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
)

const (
	pathPrefix         = "/v2/projects/"
	locationsSeg       = "locations"
	jobsSeg            = "jobs"
	servicesSeg        = "services"
	executionsSeg      = "executions"
	revisionsSeg       = "revisions"
	operationsSeg      = "operations"
	actionRun          = "run"
	actionGetIamPolicy = "getIamPolicy"
	actionSetIamPolicy = "setIamPolicy"
	actionTestIamPerms = "testIamPermissions"
	contentTypeJSON    = "application/json"
	maxBodyBytes       = 1 << 20
	jobTypeURL         = "type.googleapis.com/google.cloud.run.v2.Job"
	execTypeURL        = "type.googleapis.com/google.cloud.run.v2.Execution"
	serviceTypeURL     = "type.googleapis.com/google.cloud.run.v2.Service"
	defaultPageSize    = 100
)

// Handler serves Cloud Run Admin API v2 requests against a CloudRun driver.
type Handler struct {
	cr driver.CloudRun
	mu sync.RWMutex
	// policies stores the IAM policy set via setIamPolicy, keyed by a resource's
	// canonical name. CloudEmu does not enforce IAM; the policy is stored so a
	// set/get (and Terraform's *_iam_member read-back) round-trips.
	policies map[string]*iamPolicy
}

// New returns a Cloud Run handler backed by cr.
func New(cr driver.CloudRun) *Handler {
	return &Handler{cr: cr, policies: make(map[string]*iamPolicy)}
}

// Matches claims Cloud Run v2 job, service and location-operation paths. The
// locations+{jobs,services,operations} guard keeps it disjoint from Cloud
// Logging's /v2/projects/{p}/logs paths.
func (*Handler) Matches(r *http.Request) bool {
	_, ok := parsePath(r.URL.Path)

	return ok
}

// ServeHTTP routes by URL shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := parsePath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unsupported path")
		return
	}

	switch p.kind {
	case operationsSeg:
		h.serveOperation(w, r, &p)
	case servicesSeg:
		h.serveServices(w, r, &p)
	default:
		h.serveJobs(w, r, &p)
	}
}

// serveJobs routes the jobs surface.
func (h *Handler) serveJobs(w http.ResponseWriter, r *http.Request, p *crPath) {
	switch {
	case p.action != "":
		h.serveJobAction(w, r, p)
	case p.sub == executionsSeg && p.subName != "":
		h.getExecution(w, r, p)
	case p.sub == executionsSeg:
		h.listExecutions(w, r, p)
	case p.name != "":
		h.serveJobItem(w, r, p)
	default:
		h.serveJobCollection(w, r, p)
	}
}

func (h *Handler) serveJobAction(w http.ResponseWriter, r *http.Request, p *crPath) {
	switch p.action {
	case actionRun:
		h.run(w, r, p)
	case actionGetIamPolicy, actionSetIamPolicy, actionTestIamPerms:
		h.serveIam(w, r, p, p.jobName(p.name), h.jobExists)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown method: "+p.action)
	}
}

func (h *Handler) serveJobItem(w http.ResponseWriter, r *http.Request, p *crPath) {
	switch r.Method {
	case http.MethodGet:
		h.getJob(w, r, p)
	case http.MethodPatch:
		h.updateJob(w, r, p)
	case http.MethodDelete:
		h.deleteJob(w, r, p)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *Handler) serveJobCollection(w http.ResponseWriter, r *http.Request, p *crPath) {
	switch r.Method {
	case http.MethodPost:
		h.create(w, r, p)
	case http.MethodGet:
		h.list(w, r, p)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request, p *crPath) {
	var body jobResource
	if !decodeJSON(w, r, &body) {
		return
	}

	name := r.URL.Query().Get("jobId")
	if name == "" {
		name = lastSegment(body.Name)
	}

	if name == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "jobId is required")
		return
	}

	job, err := h.cr.CreateJob(r.Context(), jobConfigFromWire(name, &body))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, operation{
		Name:     opName(p, "create-"+name),
		Done:     true,
		Response: asResponse(toJobResource(job, p), jobTypeURL),
	})
}

func (h *Handler) updateJob(w http.ResponseWriter, r *http.Request, p *crPath) {
	var body jobResource
	if !decodeJSON(w, r, &body) {
		return
	}

	job, err := h.cr.UpdateJob(r.Context(), jobConfigFromWire(p.name, &body))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, operation{
		Name:     opName(p, "update-"+p.name),
		Done:     true,
		Response: asResponse(toJobResource(job, p), jobTypeURL),
	})
}

func (h *Handler) getJob(w http.ResponseWriter, r *http.Request, p *crPath) {
	job, err := h.cr.GetJob(r.Context(), p.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toJobResource(job, p))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, p *crPath) {
	jobs, err := h.cr.ListJobs(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	items, next := pageConvert(r, jobs, func(j *driver.Job) jobResource { return toJobResource(j, p) })

	writeJSON(w, http.StatusOK, listJobsResponse{Jobs: items, NextPageToken: next})
}

func (h *Handler) deleteJob(w http.ResponseWriter, r *http.Request, p *crPath) {
	if err := h.cr.DeleteJob(r.Context(), p.name); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, operation{Name: opName(p, "delete-"+p.name), Done: true})
}

func (h *Handler) run(w http.ResponseWriter, r *http.Request, p *crPath) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	exec, err := h.cr.RunJob(r.Context(), p.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, operation{
		Name:     opName(p, "run-"+p.name),
		Done:     true,
		Response: asResponse(toExecutionResource(exec, p), execTypeURL),
	})
}

func (h *Handler) getExecution(w http.ResponseWriter, r *http.Request, p *crPath) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	exec, err := h.cr.GetExecution(r.Context(), p.subName)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toExecutionResource(exec, p))
}

func (h *Handler) listExecutions(w http.ResponseWriter, r *http.Request, p *crPath) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	execs, err := h.cr.ListExecutions(r.Context(), p.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	items, next := pageConvert(r, execs, func(e *driver.Execution) executionResource {
		return toExecutionResource(e, p)
	})

	writeJSON(w, http.StatusOK, listExecutionsResponse{Executions: items, NextPageToken: next})
}

// serveOperation answers GET /v2/…/operations/{op}. Mutations are synchronous
// in the emulator, so a poll is just a done echo.
func (*Handler) serveOperation(w http.ResponseWriter, r *http.Request, p *crPath) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	writeJSON(w, http.StatusOK, operation{
		Name: "projects/" + p.project + "/locations/" + p.location + "/operations/" + p.name,
		Done: true,
	})
}

// crPath holds the parsed components of a Cloud Run v2 URL.
type crPath struct {
	project  string
	location string
	kind     string // jobs | services | operations
	name     string // job / service / operation id
	sub      string // executions | revisions
	subName  string // execution / revision id
	action   string // run | getIamPolicy | setIamPolicy | testIamPermissions
}

// parsePath extracts components from a Cloud Run v2 URL, returning ok=false for
// any path this handler does not serve.
func parsePath(path string) (crPath, bool) {
	if !strings.HasPrefix(path, pathPrefix) {
		return crPath{}, false
	}

	parts := strings.Split(strings.TrimPrefix(path, pathPrefix), "/")

	const (
		minParts = 4 // {project}/locations/{location}/{kind}
		idxScope = 1
		idxKind  = 3
		idxName  = 4
	)

	if len(parts) < minParts || parts[idxScope] != locationsSeg {
		return crPath{}, false
	}

	p := crPath{project: parts[0], location: parts[2], kind: parts[idxKind]}

	switch p.kind {
	case operationsSeg:
		if len(parts) <= idxName || parts[idxName] == "" {
			return crPath{}, false
		}

		p.name = parts[idxName]

		return p, true
	case jobsSeg, servicesSeg:
		parseTail(parts, idxName, &p)

		return p, true
	default:
		return crPath{}, false
	}
}

// parseTail reads the {name}[:action] and sub-collection segments after
// .../{jobs|services}.
func parseTail(parts []string, idxName int, p *crPath) {
	if len(parts) <= idxName {
		return // collection
	}

	if base, action, ok := splitColon(parts[idxName]); ok {
		p.name = base
		p.action = action

		return
	}

	p.name = parts[idxName]

	const (
		idxSub     = 5
		idxSubName = 6
	)

	if len(parts) > idxSub {
		p.sub = parts[idxSub]
	}

	if len(parts) > idxSubName {
		p.subName = parts[idxSubName]
	}
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

func (p *crPath) jobName(id string) string {
	return "projects/" + p.project + "/locations/" + p.location + "/jobs/" + id
}

func (p *crPath) serviceName(id string) string {
	return "projects/" + p.project + "/locations/" + p.location + "/services/" + id
}

func opName(p *crPath, suffix string) string {
	return "projects/" + p.project + "/locations/" + p.location + "/operations/" +
		suffix + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

// paginate resolves pageSize / pageToken query params into the indices of the
// requested page over a total-length slice, plus the next page token (empty on
// the last page). Tokens are 1-based start offsets rendered as decimal strings.
func paginate(total int, r *http.Request) (indices []int, next string) {
	size := defaultPageSize
	if v, err := strconv.Atoi(r.URL.Query().Get("pageSize")); err == nil && v > 0 {
		size = v
	}

	start := 0
	if v, err := strconv.Atoi(r.URL.Query().Get("pageToken")); err == nil && v > 0 {
		start = v
	}

	if start > total {
		start = total
	}

	end := start + size
	if end > total {
		end = total
	}

	indices = make([]int, 0, end-start)
	for i := start; i < end; i++ {
		indices = append(indices, i)
	}

	if end < total {
		next = strconv.Itoa(end)
	}

	return indices, next
}

// pageConvert selects the requested page over items and maps each element to
// its wire shape, returning the converted page and the next page token.
func pageConvert[S, W any](r *http.Request, items []S, conv func(*S) W) (page []W, next string) {
	idx, next := paginate(len(items), r)

	page = make([]W, 0, len(idx))
	for _, i := range idx {
		page = append(page, conv(&items[i]))
	}

	return page, next
}

// asResponse marshals a wire resource into the {@type, ...fields} map an LRO
// response carries.
func asResponse(v any, typeURL string) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}

	out := map[string]any{"@type": typeURL}

	var fields map[string]any
	if err := json.Unmarshal(b, &fields); err != nil {
		return out
	}

	for k, val := range fields {
		out[k] = val
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
		"error": map[string]any{"code": status, "message": msg, "status": reason},
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
