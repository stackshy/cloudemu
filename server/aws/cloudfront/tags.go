package cloudfront

import (
	"net/http"
	"sort"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

// serveTagging handles /2020-05-31/tagging: ListTagsForResource (GET) and the
// Tag/Untag operations (POST, distinguished by the Operation query parameter).
func (h *Handler) serveTagging(w http.ResponseWriter, r *http.Request) {
	arn := r.URL.Query().Get("Resource")

	switch {
	case r.Method == http.MethodGet:
		h.listTagsForResource(w, r, arn)
	case r.Method == http.MethodPost && r.URL.Query().Get("Operation") == "Tag":
		h.tagResource(w, r, arn)
	case r.Method == http.MethodPost && r.URL.Query().Get("Operation") == "Untag":
		h.untagResource(w, r, arn)
	default:
		writeError(w, http.StatusBadRequest, "InvalidArgument", "unsupported tagging operation")
	}
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request, arn string) {
	tags, err := h.cf.ListTagsForResource(r.Context(), arn)
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := tagsResponse{Xmlns: xmlns, Items: make([]tagXML, 0, len(tags))}

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		resp.Items = append(resp.Items, tagXML{Key: k, Value: tags[k]})
	}

	wire.WriteXML(w, http.StatusOK, resp)
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request, arn string) {
	var req tagsXML
	if !decodeXML(w, r, &req) {
		return
	}

	if err := h.cf.TagResource(r.Context(), arn, req.toMap()); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request, arn string) {
	var req tagKeysRequest
	if !decodeXML(w, r, &req) {
		return
	}

	if err := h.cf.UntagResource(r.Context(), arn, req.Items); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
