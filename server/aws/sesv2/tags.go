package sesv2

import (
	"net/http"
)

// serveTags routes /tags (TagResource/UntagResource/ListTagsForResource). The
// resource ARN and (for untag) tag keys are carried as query parameters.
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 0 {
		notFound(w, r.URL.Path)

		return
	}

	switch r.Method {
	case http.MethodPost:
		h.tagResource(w, r)
	case http.MethodDelete:
		h.untagResource(w, r)
	case http.MethodGet:
		h.listTags(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	var req tagResourceRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.ses.TagResource(r.Context(), req.ResourceArn, tagsToMap(req.Tags)); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	arn := q.Get("ResourceArn")
	keys := q["TagKeys"]

	if err := h.ses.UntagResource(r.Context(), arn, keys); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request) {
	arn := r.URL.Query().Get("ResourceArn")

	tags, err := h.ses.ListTagsForResource(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, listTagsForResourceResponse{Tags: mapToTags(tags)})
}
