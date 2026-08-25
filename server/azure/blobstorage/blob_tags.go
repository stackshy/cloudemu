package blobstorage

import (
	"bytes"
	"encoding/xml"
	"net/http"
	"sort"
)

// setBlobTags handles PUT /{container}/{blob}?comp=tags (Set Blob Tags). It
// parses the <Tags> XML body and stores the tags per blob WITHOUT touching the
// blob's content, ETag, or last-modified time (per the Azure spec). An empty
// <TagSet> clears all tags. Returns 204 No Content on success.
func (h *Handler) setBlobTags(w http.ResponseWriter, r *http.Request, container, blob string) {
	if h.checkLease(w, r, container, blob) {
		return
	}

	body, ok := readLimitedBody(w, r)
	if !ok {
		return
	}

	tags, err := parseTagsXML(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidXmlDocument", "could not parse Tags body")
		return
	}

	if err := h.bucket.PutObjectTagging(r.Context(), container, blob, tags); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// getBlobTags handles GET /{container}/{blob}?comp=tags (Get Blob Tags),
// returning the blob's stored tags as a <Tags> XML document with 200 OK.
func (h *Handler) getBlobTags(w http.ResponseWriter, r *http.Request, container, blob string) {
	tags, err := h.bucket.GetObjectTagging(r.Context(), container, blob)
	if err != nil {
		writeErr(w, err)
		return
	}

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := blobTagsXML{}
	for _, k := range keys {
		out.TagSet = append(out.TagSet, tagXML{Key: k, Value: tags[k]})
	}

	writeXML(w, out)
}

// parseTagsXML extracts the key/value tags from a Set Blob Tags request body.
// An empty or whitespace-only body (or an empty <TagSet>) yields an empty map,
// which clears the blob's tags.
func parseTagsXML(body []byte) (map[string]string, error) {
	tags := make(map[string]string)

	if len(bytes.TrimSpace(body)) == 0 {
		return tags, nil
	}

	var doc blobTagsXML
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, err
	}

	for _, t := range doc.TagSet {
		tags[t.Key] = t.Value
	}

	return tags, nil
}
