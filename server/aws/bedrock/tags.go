package bedrock

import (
	"net/http"

	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// tagResource handles POST /tagResource, associating tags with a resource ARN.
func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	var in tagResourceRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	if err := h.bedrock.TagResource(r.Context(), in.ResourceARN, toDriverTags(in.Tags)); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

// untagResource handles POST /untagResource, removing tag keys from a resource.
func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	var in untagResourceRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	if err := h.bedrock.UntagResource(r.Context(), in.ResourceARN, in.TagKeys); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

// listTagsForResource handles POST /listTagsForResource, returning a resource's
// tags.
func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	var in listTagsRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	tags, err := h.bedrock.ListTagsForResource(r.Context(), in.ResourceARN)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, listTagsResponse{Tags: toWireTags(tags)})
}

// toDriverTags converts wire tag pairs to driver tags.
func toDriverTags(pairs []tagPair) []bedrockdriver.Tag {
	out := make([]bedrockdriver.Tag, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, bedrockdriver.Tag{Key: p.Key, Value: p.Value})
	}

	return out
}

// toWireTags converts driver tags to wire tag pairs (always non-nil).
func toWireTags(tags []bedrockdriver.Tag) []tagPair {
	out := make([]tagPair, 0, len(tags))
	for _, t := range tags {
		out = append(out, tagPair{Key: t.Key, Value: t.Value})
	}

	return out
}
