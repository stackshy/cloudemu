package eventbridgescheduler

import (
	"net/http"
	"strings"
)

// tagPair is a single AWS Tag object ({Key,Value}), the tag shape EventBridge
// Scheduler uses on the wire (a list of pairs, not a map).
type tagPair struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// tagResourceRequest is the TagResource request body.
type tagResourceRequest struct {
	Tags []tagPair `json:"Tags"`
}

// tagListToMap converts an AWS tag list to the driver's tag map.
func tagListToMap(pairs []tagPair) map[string]string {
	if pairs == nil {
		return nil
	}

	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		out[p.Key] = p.Value
	}

	return out
}

// mapToTagList converts the driver's tag map to a deterministic AWS tag list.
func mapToTagList(tags map[string]string) []tagPair {
	out := make([]tagPair, 0, len(tags))
	for k, v := range tags {
		out = append(out, tagPair{Key: k, Value: v})
	}

	sortTagPairs(out)

	return out
}

// sortTagPairs orders tag pairs by key so ListTagsForResource is deterministic.
func sortTagPairs(pairs []tagPair) {
	for i := 1; i < len(pairs); i++ {
		for j := i; j > 0 && pairs[j-1].Key > pairs[j].Key; j-- {
			pairs[j-1], pairs[j] = pairs[j], pairs[j-1]
		}
	}
}

// serveTags routes /tags/{resourceArn}. The ARN spans multiple path segments (it
// contains a slash), so rest is rejoined into the full ARN.
//
// TagResource is POST /tags/{resourceArn}, UntagResource is
// DELETE /tags/{resourceArn}?TagKeys=..., ListTagsForResource is GET.
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

	if err := h.s.TagResource(r.Context(), resourceArn, tagListToMap(req.Tags)); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request, resourceArn string) {
	tagKeys := r.URL.Query()["TagKeys"]

	if err := h.s.UntagResource(r.Context(), resourceArn, tagKeys); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request, resourceArn string) {
	tags, err := h.s.ListTagsForResource(r.Context(), resourceArn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"Tags": mapToTagList(tags)})
}
