package vpclattice

import (
	"net/http"
	"strings"
)

// serveTags routes /tags/{resourceArn}: POST=Tag, GET=List, DELETE=Untag. The
// ARN is the whole remainder (it contains slashes).
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		notFound(w, r.URL.Path)

		return
	}

	arn := strings.Join(rest, "/")

	switch r.Method {
	case http.MethodPost:
		h.tagResource(w, r, arn)
	case http.MethodGet:
		h.listTagsForResource(w, r, arn)
	case http.MethodDelete:
		h.untagResource(w, r, arn)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request, arn string) {
	var req struct {
		Tags map[string]string `json:"tags"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.lattice.TagResource(r.Context(), arn, req.Tags); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request, arn string) {
	tags, err := h.lattice.ListTagsForResource(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"tags": tags})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request, arn string) {
	keys := r.URL.Query()["tagKeys"]

	if err := h.lattice.UntagResource(r.Context(), arn, keys); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}
