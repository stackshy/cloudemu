package batch

import (
	"net/http"
	"strings"
)

// tagsPathPrefix is the path prefix before the greedy {arn+} label of the
// tag operations (GET/POST/DELETE /v1/tags/{arn+}).
const tagsPathPrefix = apiPrefix + opTags + "/"

// serveTags routes the tag operations, which carry the resource ARN in the path
// (already percent-decoded by net/http).
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request) {
	arn := strings.TrimPrefix(r.URL.Path, tagsPathPrefix)
	if arn == "" || !strings.HasPrefix(r.URL.Path, tagsPathPrefix) {
		writeError(w, http.StatusBadRequest, exceptionClient, "resource ARN required")

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.listTagsForResource(w, r, arn)
	case http.MethodPost:
		h.tagResource(w, r, arn)
	case http.MethodDelete:
		h.untagResource(w, r, arn)
	default:
		writeError(w, http.StatusBadRequest, exceptionClient, "method not allowed: "+r.Method)
	}
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request, arn string) {
	tags, err := h.batch.ListTagsForResource(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, listTagsForResourceResponse{Tags: tags})
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request, arn string) {
	var req tagResourceRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.batch.TagResource(r.Context(), arn, req.Tags); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, emptyResponse{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request, arn string) {
	keys := r.URL.Query()["tagKeys"]

	if err := h.batch.UntagResource(r.Context(), arn, keys); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, emptyResponse{})
}
