package blobstorage

import (
	"net/http"
	"sort"
	"strings"

	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// findBlobsByTags handles Find Blobs by Tags: GET /?comp=blobs&where=… at the
// account level (container == "") and GET
// /{container}?restype=container&comp=blobs&where=… scoped to one container. It
// parses the tag-query in ?where, matches live blobs by their index tags, and
// returns each match with its container, name, and tag set.
func (h *Handler) findBlobsByTags(w http.ResponseWriter, r *http.Request, container string) {
	page, ok := h.bucket.(storagedriver.AzureFindBlobsByTags)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "find blobs by tags is not supported")
		return
	}

	where := r.URL.Query().Get("where")

	whereContainer, match, parseOK := parseTagQuery(where)
	if !parseOK {
		writeError(w, http.StatusBadRequest, "InvalidQueryParameterValue",
			"the ?where tag query is malformed or uses an unsupported operator")
		return
	}

	// A container-scoped request wins; an account-scoped one may still be narrowed
	// by an @container term in the query.
	scope := container
	if scope == "" {
		scope = whereContainer
	}

	blobs, err := page.FindBlobsByTags(r.Context(), scope, match)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := filterBlobsXML{ServiceEndpoint: serviceEndpoint(r), Where: where}
	for _, b := range blobs {
		out.Blobs.Blobs = append(out.Blobs.Blobs, filterBlobXML{
			Name: b.Name, ContainerName: b.Container, Tags: tagSetXML(b.Tags),
		})
	}

	writeXML(w, out)
}

// tagSetXML renders a blob's tag map as the <Tags><TagSet>… document the Find
// Blobs by Tags response carries, with keys ordered for determinism.
func tagSetXML(tags map[string]string) blobTagsXML {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := blobTagsXML{}
	for _, k := range keys {
		out.TagSet = append(out.TagSet, tagXML{Key: k, Value: tags[k]})
	}

	return out
}

// serviceEndpoint reconstructs the account service endpoint for the
// EnumerationResults ServiceEndpoint attribute.
func serviceEndpoint(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}

	return scheme + "://" + r.Host + "/"
}

// parseTagQuery parses an Azure Find Blobs by Tags ?where expression into an
// optional container scope (from an @container term) and the set of tag equality
// conditions that must all hold. Conditions are joined by AND; each is
// "key"='value' (double quotes around the key optional) or @container='name'.
// Only the '=' operator is supported: a query using a range operator (<, >, <=,
// >=) yields ok=false so the caller can reject it rather than silently mismatch.
// An empty or whitespace-only query matches every tagged blob.
func parseTagQuery(where string) (container string, match map[string]string, ok bool) {
	match = make(map[string]string)

	if strings.TrimSpace(where) == "" {
		return "", match, true
	}

	for _, cond := range splitAnd(where) {
		if strings.ContainsAny(cond, "<>") {
			return "", nil, false
		}

		key, val, found := strings.Cut(cond, "=")
		if !found {
			return "", nil, false
		}

		key = strings.Trim(strings.TrimSpace(key), `"`)
		val = unquoteTagValue(strings.TrimSpace(val))

		if key == "" {
			return "", nil, false
		}

		if key == "@container" {
			container = val
			continue
		}

		match[key] = val
	}

	return container, match, true
}

// splitAnd splits a ?where expression on the AND conjunction, case-insensitively
// and only when AND stands as its own space-delimited token.
func splitAnd(where string) []string {
	fields := strings.Fields(where)

	var (
		parts []string
		cur   []string
	)

	for _, f := range fields {
		if strings.EqualFold(f, "AND") {
			parts = append(parts, strings.Join(cur, " "))
			cur = nil

			continue
		}

		cur = append(cur, f)
	}

	return append(parts, strings.Join(cur, " "))
}

// unquoteTagValue strips the single quotes around a tag value and unescapes a
// doubled single-quote (”) to a literal one, per the Azure query grammar.
func unquoteTagValue(v string) string {
	v = strings.TrimPrefix(v, "'")
	v = strings.TrimSuffix(v, "'")

	return strings.ReplaceAll(v, "''", "'")
}
