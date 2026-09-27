package apigatewayv2

import (
	"net/http"
	"net/url"
	"strings"
)

// serveTags handles /v2/tags/{resource-arn}: GET=GetTags, POST=TagResource,
// DELETE=UntagResource (tagKeys in the query string). The ARN is one
// path-escaped label, so it is read from the escaped path.
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request) {
	arn, err := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), tagsPrefix))
	if err != nil {
		writeError(w, http.StatusBadRequest, "BadRequestException", "Invalid resource ARN specified")
		return
	}

	switch r.Method {
	case http.MethodGet:
		tags, err := h.ag.GetTags(r.Context(), arn)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, tagsBody{Tags: tags})
	case http.MethodPost:
		var req tagsBody
		if !decodeJSON(w, r, &req) {
			return
		}

		if err := h.ag.TagResource(r.Context(), arn, req.Tags); err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, struct{}{})
	case http.MethodDelete:
		if err := h.ag.UntagResource(r.Context(), arn, r.URL.Query()["tagKeys"]); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		writeMethodNotAllowed(w)
	}
}
