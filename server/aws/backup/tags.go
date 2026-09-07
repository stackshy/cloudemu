package backup

import (
	"net/http"
	"strings"
)

// serveTags handles the shared /tags/{resourceArn} operations: POST
// (TagResource) and GET (ListTags). The ARN is the whole path below /tags/.
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request, rest []string) {
	arn := strings.Join(rest, "/")
	if arn == "" {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch r.Method {
	case http.MethodPost:
		h.tagResource(w, r, arn)
	case http.MethodGet:
		h.listTags(w, r, arn)
	default:
		methodNotAllowed(w)
	}
}

// serveUntag handles POST /untag/{resourceArn} (UntagResource).
func (h *Handler) serveUntag(w http.ResponseWriter, r *http.Request, rest []string) {
	arn := strings.Join(rest, "/")
	if arn == "" {
		notFoundPath(w, r.URL.Path)

		return
	}

	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	var req struct {
		TagKeyList []string `json:"TagKeyList"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	if err := h.backup.UntagResource(r.Context(), arn, req.TagKeyList); err != nil {
		writeErr(w, err)

		return
	}

	writeEmpty(w)
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request, arn string) {
	var req struct {
		Tags map[string]string `json:"Tags"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	if err := h.backup.TagResource(r.Context(), arn, req.Tags); err != nil {
		writeErr(w, err)

		return
	}

	writeEmpty(w)
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request, arn string) {
	tags, err := h.backup.ListTags(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	body := map[string]any{}
	if len(tags) > 0 {
		body["Tags"] = tags
	}

	writeJSON(w, body)
}
