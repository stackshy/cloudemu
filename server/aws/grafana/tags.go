package grafana

import (
	"net/http"
	"strings"
)

// tagResourceRequest is the TagResource request body.
type tagResourceRequest struct {
	Tags map[string]string `json:"tags"`
}

// serveTags routes /tags/{resourceArn}. The ARN spans multiple path segments
// (it contains a slash), so rest is rejoined into the full ARN.
//
// TagResource is POST /tags/{resourceArn}, UntagResource is
// DELETE /tags/{resourceArn}?tagKeys=..., ListTagsForResource is GET.
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	resourceArn := strings.Join(rest, "/")

	switch r.Method {
	case http.MethodPost:
		h.tagResource(w, r, resourceArn)
	case http.MethodDelete:
		h.untagResource(w, r, resourceArn)
	case http.MethodGet:
		h.listTagsForResource(w, r, resourceArn)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request, resourceArn string) {
	var req tagResourceRequest
	if !decodeBody(w, r, &req) {
		return
	}

	if err := h.g.TagResource(r.Context(), resourceArn, req.Tags); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request, resourceArn string) {
	tagKeys := r.URL.Query()["tagKeys"]

	if err := h.g.UntagResource(r.Context(), resourceArn, tagKeys); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request, resourceArn string) {
	tags, err := h.g.ListTagsForResource(r.Context(), resourceArn)
	if err != nil {
		writeErr(w, err)

		return
	}

	if tags == nil {
		tags = map[string]string{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}
