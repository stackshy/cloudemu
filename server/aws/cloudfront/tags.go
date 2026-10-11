package cloudfront

import (
	"net/http"
	"sort"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

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
