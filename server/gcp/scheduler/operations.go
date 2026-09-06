package scheduler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	scheddriver "github.com/stackshy/cloudemu/v2/services/scheduler/driver"
)

const (
	defaultPageSize = 500
	maxPageSize     = 500
)

// decodeJob reads a job request body, normalizing integer enums to their
// canonical names first. Returns false (and writes a 400) on a decode error.
func decodeJob(w http.ResponseWriter, r *http.Request, job *jobJSON) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, gcprest.MaxBodyBytes))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "read body: "+err.Error())
		return false
	}

	if len(body) == 0 {
		return true // empty body ({} equivalent) — leaves job zero-valued
	}

	if err := json.Unmarshal(normalizeEnumNumbers(body), job); err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid JSON: "+err.Error())
		return false
	}

	return true
}

// validateJob enforces the target oneof (exactly one target) and a recognized
// HTTP method. Returns false (and writes a 400) on a violation.
func validateJob(w http.ResponseWriter, job *jobJSON) bool {
	targets := boolToInt(job.HTTPTarget != nil) +
		boolToInt(job.PubsubTarget != nil) +
		boolToInt(job.AppEngineHTTPTarget != nil)

	if targets != 1 {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument",
			"exactly one of httpTarget, pubsubTarget, appEngineHttpTarget must be set")
		return false
	}

	if job.HTTPTarget != nil && !methodOK(job.HTTPTarget.HTTPMethod) {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", "invalid httpTarget.httpMethod")
		return false
	}

	if job.AppEngineHTTPTarget != nil && !methodOK(job.AppEngineHTTPTarget.HTTPMethod) {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", "invalid appEngineHttpTarget.httpMethod")
		return false
	}

	return true
}

// methodOK reports whether m is empty (defaults to POST) or a valid HttpMethod.
func methodOK(m string) bool {
	return m == "" || validMethods[m]
}

// boolToInt returns 1 for true, 0 for false — used to count the set targets in
// the oneof.
func boolToInt(b bool) int {
	if b {
		return 1
	}

	return 0
}

func (h *Handler) createJob(w http.ResponseWriter, r *http.Request, rt route) {
	var body jobJSON
	if !decodeJob(w, r, &body) {
		return
	}

	if !validateJob(w, &body) {
		return
	}

	// The job id comes from the ?jobId query param, or the trailing segment of a
	// name supplied in the body.
	id := r.URL.Query().Get("jobId")
	if id == "" {
		id = lastSegment(body.Name)
	}

	if id == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", "jobId is required")
		return
	}

	rt.job = id

	job, err := h.jobs.CreateJob(r.Context(), body.toDriverConfig(rt.fullName()))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toJobJSON(job))
}

func (h *Handler) getJob(w http.ResponseWriter, r *http.Request, rt route) {
	job, err := h.jobs.GetJob(r.Context(), rt.fullName())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toJobJSON(job))
}

func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request, rt route) {
	jobs, err := h.jobs.ListJobs(r.Context(), rt.parent())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, err := pagination.PaginateSorted(jobs,
		func(a, b scheddriver.Job) bool { return a.Name < b.Name },
		pageToken(r), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	out := make([]jobJSON, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, toJobJSON(&page.Items[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listJobsResponse{Jobs: out, NextPageToken: page.NextPageToken})
}

func (h *Handler) patchJob(w http.ResponseWriter, r *http.Request, rt route) {
	var body jobJSON
	if !decodeJob(w, r, &body) {
		return
	}

	// Validate methods only when a target carrying one is being written.
	if body.HTTPTarget != nil && !methodOK(body.HTTPTarget.HTTPMethod) {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", "invalid httpTarget.httpMethod")
		return
	}

	if body.AppEngineHTTPTarget != nil && !methodOK(body.AppEngineHTTPTarget.HTTPMethod) {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", "invalid appEngineHttpTarget.httpMethod")
		return
	}

	mask := parseMask(r.URL.Query().Get("updateMask"))

	job, err := h.jobs.PatchJob(r.Context(), body.toDriverConfig(rt.fullName()), mask)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toJobJSON(job))
}

func (h *Handler) deleteJob(w http.ResponseWriter, r *http.Request, rt route) {
	if err := h.jobs.DeleteJob(r.Context(), rt.fullName()); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// google.protobuf.Empty.
	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}

func (h *Handler) pauseJob(w http.ResponseWriter, r *http.Request, rt route) {
	job, err := h.jobs.PauseJob(r.Context(), rt.fullName())
	writeJobOrErr(w, job, err)
}

func (h *Handler) resumeJob(w http.ResponseWriter, r *http.Request, rt route) {
	job, err := h.jobs.ResumeJob(r.Context(), rt.fullName())
	writeJobOrErr(w, job, err)
}

func (h *Handler) runJob(w http.ResponseWriter, r *http.Request, rt route) {
	job, err := h.jobs.RunJob(r.Context(), rt.fullName())
	writeJobOrErr(w, job, err)
}

// writeJobOrErr writes a job on success or maps the error, shared by the three
// custom verbs.
func writeJobOrErr(w http.ResponseWriter, job *scheddriver.Job, err error) {
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toJobJSON(job))
}

// parseMask splits a comma-separated updateMask query param into field paths.
func parseMask(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}

// lastSegment returns the trailing path segment of a resource name.
func lastSegment(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}

	return name
}

// pageSize reads ?pageSize, clamping to a sane default and ceiling.
func pageSize(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if err != nil || n <= 0 {
		return defaultPageSize
	}

	if n > maxPageSize {
		return maxPageSize
	}

	return n
}

// pageToken reads the opaque ?pageToken continuation cursor.
func pageToken(r *http.Request) string {
	return r.URL.Query().Get("pageToken")
}
