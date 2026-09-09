package codeartifact

import (
	"encoding/json"
	"net/http"
)

// serveTag handles POST /v1/tag?resourceArn= (TagResource).
func (h *Handler) serveTag(w http.ResponseWriter, r *http.Request, rest []string) {
	if !okTagShape(w, r, rest) {
		return
	}

	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	if err := h.ca.TagResource(r.Context(), r.URL.Query().Get("resourceArn"), tagsFromBody(raw)); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

// serveUntag handles POST /v1/untag?resourceArn= (UntagResource).
func (h *Handler) serveUntag(w http.ResponseWriter, r *http.Request, rest []string) {
	if !okTagShape(w, r, rest) {
		return
	}

	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	if err := h.ca.UntagResource(r.Context(), r.URL.Query().Get("resourceArn"), tagKeysFromBody(raw)); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

// serveListTags handles POST /v1/tags?resourceArn= (ListTagsForResource).
func (h *Handler) serveListTags(w http.ResponseWriter, r *http.Request, rest []string) {
	if !okTagShape(w, r, rest) {
		return
	}

	tags, err := h.ca.ListTagsForResource(r.Context(), r.URL.Query().Get("resourceArn"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"tags": tagsToWire(tags)})
}

// okTagShape validates a tag request's path and verb, writing the appropriate
// error and returning false when it is malformed: an unknown sub-path is a 404,
// a wrong verb on the exact path is a method error.
func okTagShape(w http.ResponseWriter, r *http.Request, rest []string) bool {
	switch {
	case len(rest) != 0:
		notFoundPath(w, r.URL.Path)

		return false
	case r.Method != http.MethodPost:
		methodNotAllowed(w)

		return false
	default:
		return true
	}
}

// tagKeysFromBody extracts the tagKeys list from a raw request body.
func tagKeysFromBody(raw map[string]json.RawMessage) []string {
	v, ok := raw["tagKeys"]
	if !ok {
		return nil
	}

	var keys []string
	if json.Unmarshal(v, &keys) != nil {
		return nil
	}

	return keys
}
