package dynamodb

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

type tagJSON struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// tableFromARN resolves a DynamoDB ResourceArn ("arn:aws:dynamodb:<region>:
// <account>:table/<name>") to the bare table name the driver keys on. A value
// that isn't an ARN is returned unchanged, so a plain name also works.
func tableFromARN(arn string) string {
	const marker = ":table/"

	if i := strings.LastIndex(arn, marker); i >= 0 {
		name := arn[i+len(marker):]
		// A table ARN may carry a sub-resource suffix (.../index/...); keep only
		// the table segment.
		if j := strings.IndexByte(name, '/'); j >= 0 {
			name = name[:j]
		}

		return name
	}

	return arn
}

func (h *Handler) routeTags(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "TagResource":
		h.tagResource(w, r)
	case "UntagResource":
		h.untagResource(w, r)
	case "ListTagsOfResource":
		h.listTagsOfResource(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string    `json:"ResourceArn"`
		Tags        []tagJSON `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tags := make(map[string]string, len(req.Tags))
	for _, t := range req.Tags {
		tags[t.Key] = t.Value
	}

	if err := h.db.TagResource(r.Context(), tableFromARN(req.ResourceArn), tags); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string   `json:"ResourceArn"`
		TagKeys     []string `json:"TagKeys"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.db.UntagResource(r.Context(), tableFromARN(req.ResourceArn), req.TagKeys); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) listTagsOfResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string `json:"ResourceArn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tags, err := h.db.ListTagsOfResource(r.Context(), tableFromARN(req.ResourceArn))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]tagJSON, 0, len(tags))
	for k, v := range tags {
		out = append(out, tagJSON{Key: k, Value: v})
	}

	wire.WriteJSON(w, map[string]any{"Tags": out})
}
