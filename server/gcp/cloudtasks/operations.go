package cloudtasks

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	ctdriver "github.com/stackshy/cloudemu/v2/services/cloudtasks/driver"
)

const (
	defaultPageSize = 1000
	maxPageSize     = 1000
)

// decodeQueue reads a queue request body, normalizing integer enums to their
// canonical names first. Returns false (and writes a 400) on a decode error.
func decodeQueue(w http.ResponseWriter, r *http.Request, q *queueJSON) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, gcprest.MaxBodyBytes))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "read body: "+err.Error())
		return false
	}

	if len(body) == 0 {
		return true // empty body ({} equivalent) — leaves queue zero-valued
	}

	if err := json.Unmarshal(normalizeEnumNumbers(body), q); err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid JSON: "+err.Error())
		return false
	}

	return true
}

func (h *Handler) createQueue(w http.ResponseWriter, r *http.Request, rt route) {
	var body queueJSON
	if !decodeQueue(w, r, &body) {
		return
	}

	// The queue id is the trailing segment of the name supplied in the body
	// (Cloud Tasks create takes the full resource name in the body — there is no
	// queueId query param).
	id := lastSegment(body.Name)
	if id == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", "queue name is required")
		return
	}

	rt.queue = id

	q, err := h.queues.CreateQueue(r.Context(), body.toDriverConfig(rt.fullName()))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toQueueJSON(q))
}

func (h *Handler) getQueue(w http.ResponseWriter, r *http.Request, rt route) {
	q, err := h.queues.GetQueue(r.Context(), rt.fullName())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toQueueJSON(q))
}

func (h *Handler) listQueues(w http.ResponseWriter, r *http.Request, rt route) {
	queues, err := h.queues.ListQueues(r.Context(), rt.parent())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, err := pagination.PaginateSorted(queues,
		func(a, b ctdriver.Queue) bool { return a.Name < b.Name },
		pageToken(r), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	out := make([]queueJSON, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, toQueueJSON(&page.Items[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listQueuesResponse{Queues: out, NextPageToken: page.NextPageToken})
}

func (h *Handler) patchQueue(w http.ResponseWriter, r *http.Request, rt route) {
	var body queueJSON
	if !decodeQueue(w, r, &body) {
		return
	}

	mask := parseMask(r.URL.Query().Get("updateMask"))

	q, err := h.queues.PatchQueue(r.Context(), body.toDriverConfig(rt.fullName()), mask)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toQueueJSON(q))
}

func (h *Handler) deleteQueue(w http.ResponseWriter, r *http.Request, rt route) {
	if err := h.queues.DeleteQueue(r.Context(), rt.fullName()); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// google.protobuf.Empty.
	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}

func (h *Handler) pauseQueue(w http.ResponseWriter, r *http.Request, rt route) {
	q, err := h.queues.PauseQueue(r.Context(), rt.fullName())
	writeQueueOrErr(w, q, err)
}

func (h *Handler) resumeQueue(w http.ResponseWriter, r *http.Request, rt route) {
	q, err := h.queues.ResumeQueue(r.Context(), rt.fullName())
	writeQueueOrErr(w, q, err)
}

func (h *Handler) purgeQueue(w http.ResponseWriter, r *http.Request, rt route) {
	q, err := h.queues.PurgeQueue(r.Context(), rt.fullName())
	writeQueueOrErr(w, q, err)
}

func (h *Handler) getIamPolicy(w http.ResponseWriter, r *http.Request, rt route) {
	pol, err := h.queues.GetIamPolicy(r.Context(), rt.fullName())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toPolicyJSON(pol))
}

func (h *Handler) setIamPolicy(w http.ResponseWriter, r *http.Request, rt route) {
	var req setIamPolicyRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	pol, err := h.queues.SetIamPolicy(r.Context(), rt.fullName(), fromPolicyJSON(req.Policy))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toPolicyJSON(pol))
}

func (h *Handler) testIamPermissions(w http.ResponseWriter, r *http.Request, rt route) {
	var req testIamPermissionsRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	granted, err := h.queues.TestIamPermissions(r.Context(), rt.fullName(), req.Permissions)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, testIamPermissionsResponse{Permissions: granted})
}

// writeQueueOrErr writes a queue on success or maps the error, shared by the
// pause/resume/purge verbs.
func writeQueueOrErr(w http.ResponseWriter, q *ctdriver.Queue, err error) {
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toQueueJSON(q))
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
