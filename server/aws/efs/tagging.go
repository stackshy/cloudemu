package efs

import (
	"net/http"
)

// serveResourceTags routes /resource-tags/{resourceId} (current tagging API):
// POST=TagResource, GET=ListTagsForResource, DELETE=UntagResource.
func (h *Handler) serveResourceTags(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 1 {
		notFound(w, r.URL.Path)
		return
	}

	id := rest[0]

	switch r.Method {
	case http.MethodPost:
		h.tagResource(w, r, id)
	case http.MethodGet:
		h.listTagsForResource(w, r, id)
	case http.MethodDelete:
		h.untagResourceQuery(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request, id string) {
	var req tagResourceRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.efs.TagResource(r.Context(), id, tagsToMap(req.Tags)); err != nil {
		writeErr(w, err)
		return
	}

	writeStatus(w, http.StatusOK)
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request, id string) {
	tags, err := h.efs.ListTagsForResource(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, listTagsForResourceResponse{Tags: mapToTags(tags)})
}

// untagResourceQuery handles DELETE /resource-tags/{id}?tagKeys=a&tagKeys=b.
func (h *Handler) untagResourceQuery(w http.ResponseWriter, r *http.Request, id string) {
	keys := r.URL.Query()["tagKeys"]

	if err := h.efs.UntagResource(r.Context(), id, keys); err != nil {
		writeErr(w, err)
		return
	}

	writeStatus(w, http.StatusOK)
}

// serveLegacyTags routes the deprecated tag APIs:
//
//	POST /create-tags/{fsId}   (CreateTags — body {Tags:[...]})
//	POST /delete-tags/{fsId}   (DeleteTags — body {TagKeys:[...]})
//	GET  /tags/{fsId}          (DescribeTags)
func (h *Handler) serveLegacyTags(w http.ResponseWriter, r *http.Request, root string, rest []string) {
	if len(rest) != 1 {
		notFound(w, r.URL.Path)
		return
	}

	id := rest[0]

	switch {
	case root == rootCreateTags && r.Method == http.MethodPost:
		h.tagResource(w, r, id)
	case root == rootDeleteTags && r.Method == http.MethodPost:
		h.deleteTagsBody(w, r, id)
	case root == rootTags && r.Method == http.MethodGet:
		h.describeTags(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) deleteTagsBody(w http.ResponseWriter, r *http.Request, id string) {
	var req untagResourceRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.efs.UntagResource(r.Context(), id, req.TagKeys); err != nil {
		writeErr(w, err)
		return
	}

	writeStatus(w, http.StatusOK)
}

func (h *Handler) describeTags(w http.ResponseWriter, r *http.Request, id string) {
	tags, err := h.efs.ListTagsForResource(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, listTagsForResourceResponse{Tags: mapToTags(tags)})
}
