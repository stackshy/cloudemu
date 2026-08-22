// Package cloudrun implements the GCP Cloud Run Admin API v2 Jobs REST surface
// as a server.Handler. Real cloud.google.com/go/run/apiv2 clients (and the
// gcloud CLI) configured with a custom endpoint hit this handler the same way
// they hit run.googleapis.com.
//
// Scope is Jobs (run-to-completion), not Services (HTTP ingress / revisions).
//
// Coverage:
//
//	POST   /v2/projects/{p}/locations/{l}/jobs?jobId={id}                   — Create (LRO, response=Job)
//	GET    /v2/projects/{p}/locations/{l}/jobs/{job}                        — Get
//	GET    /v2/projects/{p}/locations/{l}/jobs                              — List
//	DELETE /v2/projects/{p}/locations/{l}/jobs/{job}                        — Delete (LRO)
//	POST   /v2/projects/{p}/locations/{l}/jobs/{job}:run                    — Run   (LRO, response=Execution)
//	GET    /v2/projects/{p}/locations/{l}/jobs/{job}/executions/{exec}      — Executions.Get
//	GET    /v2/projects/{p}/locations/{l}/operations/{op}                   — Poll an LRO
//
// All mutating endpoints return google.longrunning.Operation envelopes with
// done=true so SDK pollers terminate on the first response.
package cloudrun

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
)

const (
	pathPrefix      = "/v2/projects/"
	locationsSeg    = "locations"
	jobsSeg         = "jobs"
	executionsSeg   = "executions"
	operationsSeg   = "operations"
	actionRun       = "run"
	contentTypeJSON = "application/json"
	maxBodyBytes    = 1 << 20
	jobTypeURL      = "type.googleapis.com/google.cloud.run.v2.Job"
	execTypeURL     = "type.googleapis.com/google.cloud.run.v2.Execution"
)

// Handler serves Cloud Run Admin API v2 Jobs requests against a CloudRun driver.
type Handler struct {
	cr driver.CloudRun
}

// New returns a Cloud Run Jobs handler backed by cr.
func New(cr driver.CloudRun) *Handler {
	return &Handler{cr: cr}
}

// Matches claims Cloud Run v2 job and location-operation paths:
// /v2/projects/{p}/locations/{l}/{jobs|operations}[/...]. The locations+jobs
// guard keeps it disjoint from Cloud Logging's /v2/projects/{p}/logs paths.
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

	switch {
	case p.operation != "":
		h.serveOperation(w, r, &p)
	case p.action == actionRun && p.job != "":
		h.run(w, r, &p)
	case p.execution != "":
		h.getExecution(w, r, &p)
	case p.job != "":
		h.serveJob(w, r, &p)
	default:
		h.serveCollection(w, r, &p)
	}
}

func (h *Handler) serveJob(w http.ResponseWriter, r *http.Request, p *crPath) {
	switch r.Method {
	case http.MethodGet:
		h.getJob(w, r, p)
	case http.MethodDelete:
		h.deleteJob(w, r, p)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, p *crPath) {
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

	cfg := driver.JobConfig{
		Name:        name,
		TaskCount:   body.Template.TaskCount,
		Containers:  toDriverContainers(body.Template.Template.Containers),
		Labels:      body.Labels,
		Annotations: body.Annotations,
	}

	job, err := h.cr.CreateJob(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	resource := toJobResource(job, p)
	writeJSON(w, http.StatusOK, operation{
		Name:     opName(p, "create-"+name),
		Done:     true,
		Response: asResponse(resource, jobTypeURL),
	})
}

func (h *Handler) getJob(w http.ResponseWriter, r *http.Request, p *crPath) {
	job, err := h.cr.GetJob(r.Context(), p.job)
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

	out := listJobsResponse{Jobs: make([]jobResource, 0, len(jobs))}
	for i := range jobs {
		out.Jobs = append(out.Jobs, toJobResource(&jobs[i], p))
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) deleteJob(w http.ResponseWriter, r *http.Request, p *crPath) {
	if err := h.cr.DeleteJob(r.Context(), p.job); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, operation{Name: opName(p, "delete-"+p.job), Done: true})
}

func (h *Handler) run(w http.ResponseWriter, r *http.Request, p *crPath) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	exec, err := h.cr.RunJob(r.Context(), p.job)
	if err != nil {
		writeErr(w, err)
		return
	}

	resource := toExecutionResource(exec, p)
	writeJSON(w, http.StatusOK, operation{
		Name:     opName(p, "run-"+p.job),
		Done:     true,
		Response: asResponse(resource, execTypeURL),
	})
}

func (h *Handler) getExecution(w http.ResponseWriter, r *http.Request, p *crPath) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	exec, err := h.cr.GetExecution(r.Context(), p.execution)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toExecutionResource(exec, p))
}

// serveOperation answers GET /v2/…/operations/{op}. Mutations are synchronous
// in the emulator, so a poll is just a done echo.
func (*Handler) serveOperation(w http.ResponseWriter, r *http.Request, p *crPath) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	writeJSON(w, http.StatusOK, operation{
		Name: "projects/" + p.project + "/locations/" + p.location + "/operations/" + p.operation,
		Done: true,
	})
}

// crPath holds the parsed components of a Cloud Run v2 URL.
type crPath struct {
	project   string
	location  string
	job       string
	execution string
	action    string // "run"
	operation string
}

// parsePath extracts components from a Cloud Run v2 URL, returning ok=false for
// any path this handler does not serve.
//
//	/v2/projects/{p}/locations/{l}/jobs
//	/v2/projects/{p}/locations/{l}/jobs/{job}
//	/v2/projects/{p}/locations/{l}/jobs/{job}:run
//	/v2/projects/{p}/locations/{l}/jobs/{job}/executions/{exec}
//	/v2/projects/{p}/locations/{l}/operations/{op}
func parsePath(path string) (crPath, bool) {
	if !strings.HasPrefix(path, pathPrefix) {
		return crPath{}, false
	}

	parts := strings.Split(strings.TrimPrefix(path, pathPrefix), "/")

	const (
		minParts = 4 // {project}/locations/{location}/{type}
		idxScope = 1
		idxType  = 3
		idxName  = 4
	)

	if len(parts) < minParts || parts[idxScope] != locationsSeg {
		return crPath{}, false
	}

	p := crPath{project: parts[0], location: parts[2]}

	switch parts[idxType] {
	case operationsSeg:
		if len(parts) <= idxName || parts[idxName] == "" {
			return crPath{}, false
		}

		p.operation = parts[idxName]

		return p, true
	case jobsSeg:
		parseJobTail(parts, idxName, &p)

		return p, true
	default:
		return crPath{}, false
	}
}

// parseJobTail reads the job / :run / executions segments after ".../jobs".
func parseJobTail(parts []string, idxName int, p *crPath) {
	if len(parts) <= idxName {
		return // collection
	}

	if base, action, ok := splitColon(parts[idxName]); ok {
		p.job = base
		p.action = action

		return
	}

	p.job = parts[idxName]

	const (
		idxExecType = 5
		idxExecName = 6
	)

	if len(parts) > idxExecName && parts[idxExecType] == executionsSeg {
		p.execution = parts[idxExecName]
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

func opName(p *crPath, suffix string) string {
	return "projects/" + p.project + "/locations/" + p.location + "/operations/" +
		suffix + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

func toDriverContainers(in []container) []driver.Container {
	out := make([]driver.Container, 0, len(in))
	for i := range in {
		out = append(out, driver.Container{
			Name:    in[i].Name,
			Image:   in[i].Image,
			Command: in[i].Command,
			Args:    in[i].Args,
			Env:     envToMap(in[i].Env),
		})
	}

	return out
}

func envToMap(in []envVar) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for _, e := range in {
		out[e.Name] = e.Value
	}

	return out
}

func envToList(in map[string]string) []envVar {
	if len(in) == 0 {
		return nil
	}

	out := make([]envVar, 0, len(in))
	for k, v := range in {
		out = append(out, envVar{Name: k, Value: v})
	}

	return out
}

func toContainers(in []driver.Container) []container {
	out := make([]container, 0, len(in))
	for i := range in {
		out = append(out, container{
			Name:    in[i].Name,
			Image:   in[i].Image,
			Command: in[i].Command,
			Args:    in[i].Args,
			Env:     envToList(in[i].Env),
		})
	}

	return out
}

func toConditions(in []driver.Condition) []condition {
	if len(in) == 0 {
		return nil
	}

	out := make([]condition, 0, len(in))
	for _, c := range in {
		out = append(out, condition{Type: c.Type, State: c.State, Message: c.Message, Reason: c.Reason})
	}

	return out
}

func toJobResource(j *driver.Job, p *crPath) jobResource {
	return jobResource{
		Name:           p.jobName(j.Name),
		UID:            j.UID,
		Generation:     strconv.FormatInt(j.Generation, 10),
		CreateTime:     formatTime(j.CreateTime),
		UpdateTime:     formatTime(j.UpdateTime),
		LaunchStage:    j.LaunchStage,
		ExecutionCount: j.ExecutionCount,
		Labels:         j.Labels,
		Annotations:    j.Annotations,
		Template: execTemplate{
			TaskCount: j.TaskCount,
			Template:  taskTemplate{Containers: toContainers(j.Containers)},
		},
		Conditions: toConditions(j.Conditions),
	}
}

func toExecutionResource(e *driver.Execution, p *crPath) executionResource {
	return executionResource{
		Name:           p.jobName(e.JobName) + "/executions/" + e.Name,
		UID:            e.UID,
		Generation:     strconv.FormatInt(e.Generation, 10),
		Job:            p.jobName(e.JobName),
		CreateTime:     formatTime(e.CreateTime),
		StartTime:      formatTime(e.StartTime),
		CompletionTime: formatTime(e.CompletionTime),
		TaskCount:      e.TaskCount,
		SucceededCount: e.SucceededCount,
		FailedCount:    e.FailedCount,
		RunningCount:   e.RunningCount,
		CancelledCount: e.CancelledCount,
		LogURI:         e.LogURI,
		Template:       taskTemplate{Containers: toContainers(e.Containers)},
		Conditions:     toConditions(e.Conditions),
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339Nano)
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
