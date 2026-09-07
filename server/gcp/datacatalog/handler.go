// Package datacatalog implements the Google Cloud Data Catalog control plane
// (datacatalog.googleapis.com) as a server.Handler on the /v1/ version prefix.
// The google_data_catalog_{entry_group,entry,tag_template,tag} resources are in
// the stable terraform-provider-google (GA), whose default base path is
// datacatalog.googleapis.com/v1/; real google.golang.org/api/datacatalog/v1
// clients and gcloud use the same path.
//
// Coverage (metadata registration control plane only, synchronous REST — no
// LRO):
//
//	POST   /v1/…/entryGroups?entryGroupId=                       — CreateEntryGroup
//	GET    /v1/…/entryGroups                                     — ListEntryGroups
//	GET    /v1/…/entryGroups/{eg}                                — GetEntryGroup
//	PATCH  /v1/…/entryGroups/{eg}?updateMask=                    — PatchEntryGroup
//	DELETE /v1/…/entryGroups/{eg}                                — DeleteEntryGroup
//	POST   /v1/…/entryGroups/{eg}/entries?entryId=              — CreateEntry
//	GET    /v1/…/entryGroups/{eg}/entries                        — ListEntries
//	GET    /v1/…/entryGroups/{eg}/entries/{e}                    — GetEntry
//	PATCH  /v1/…/entryGroups/{eg}/entries/{e}?updateMask=        — PatchEntry
//	DELETE /v1/…/entryGroups/{eg}/entries/{e}                    — DeleteEntry
//	POST   /v1/…/entries/{e}/tags                                — CreateTag (server-named)
//	GET    /v1/…/entries/{e}/tags                                — ListTags
//	PATCH  /v1/…/entries/{e}/tags/{t}?updateMask=               — PatchTag
//	DELETE /v1/…/entries/{e}/tags/{t}                            — DeleteTag
//	POST   /v1/…/tagTemplates?tagTemplateId=                     — CreateTagTemplate
//	GET    /v1/…/tagTemplates/{tt}                               — GetTagTemplate
//	PATCH  /v1/…/tagTemplates/{tt}?updateMask=                   — PatchTagTemplate
//	DELETE /v1/…/tagTemplates/{tt}?force=                        — DeleteTagTemplate
//	POST   /v1/…/tagTemplates/{tt}/fields?tagTemplateFieldId=    — CreateTagTemplateField
//	PATCH  /v1/…/tagTemplates/{tt}/fields/{f}?updateMask=        — PatchTagTemplateField
//	DELETE /v1/…/tagTemplates/{tt}/fields/{f}?force=             — DeleteTagTemplateField
//
// Every RPC returns the resource (or an empty object for delete) directly with
// no google.longrunning.Operation wrapper. Deleting a parent cascades to its
// descendants in the driver. The entryGroups/tagTemplates resource-segment guard
// keeps this handler disjoint from every other /v1/projects/ handler.
package datacatalog

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	dcdriver "github.com/stackshy/cloudemu/v2/services/datacatalog/driver"
)

const (
	apiV1 = "v1"

	projectsSeg     = "projects"
	locationsSeg    = "locations"
	entryGroupsSeg  = "entryGroups"
	entriesSeg      = "entries"
	tagsSeg         = "tags"
	tagTemplatesSeg = "tagTemplates"
	fieldsSeg       = "fields"

	minParts = 5 // [projects, {p}, locations, {loc}, {entryGroups|tagTemplates}]
)

// levelKind identifies which resource a path addresses.
type levelKind int

const (
	levelEntryGroup levelKind = iota
	levelEntry
	levelTag
	levelTagTemplate
	levelTagTemplateField
)

// Handler serves datacatalog.googleapis.com v1 requests against a DataCatalog
// driver.
type Handler struct {
	db dcdriver.DataCatalog
}

// New returns a Data Catalog handler backed by db.
func New(db dcdriver.DataCatalog) *Handler { return &Handler{db: db} }

// route holds the parsed components of a Data Catalog path. eg/entry/tt/field
// hold the addressed ids; name is the id at the deepest level, empty for a
// collection request. For a tag, entry is empty when the tag attaches to the
// entry group itself.
type route struct {
	project  string
	location string
	eg       string
	entry    string
	tt       string
	field    string
	level    levelKind
	name     string
}

// parseRoute extracts the components of a Data Catalog path under the /v1/
// version prefix. It accepts the entryGroups → entries → tags hierarchy and the
// tagTemplates → fields hierarchy under a locations scope.
func parseRoute(urlPath string) (route, bool) {
	prefix := "/" + apiV1 + "/" + projectsSeg + "/"
	if !strings.HasPrefix(urlPath, prefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/"+apiV1+"/"), "/")
	if len(parts) < minParts || parts[0] != projectsSeg || parts[2] != locationsSeg {
		return route{}, false
	}

	rt := route{project: parts[1], location: parts[3]}

	switch parts[4] {
	case entryGroupsSeg:
		ok := parseEntryGroupTree(&rt, parts[5:])
		return rt, ok
	case tagTemplatesSeg:
		ok := parseTagTemplateTree(&rt, parts[5:])
		return rt, ok
	default:
		return route{}, false
	}
}

// parseEntryGroupTree folds the segments after "entryGroups" into rt, consuming
// [eg] optionally followed by ("entries", entry) and then the tag tail, or an
// entry-group-level ("tags"[, tag]) tail.
func parseEntryGroupTree(rt *route, rest []string) bool {
	rt.level = levelEntryGroup

	if len(rest) == 0 {
		return true // entryGroups collection
	}

	rt.eg, rt.name, rest = rest[0], rest[0], rest[1:]
	if len(rest) == 0 {
		return true // entry group item
	}

	switch rest[0] {
	case entriesSeg:
		return parseEntryTail(rt, rest[1:])
	case tagsSeg:
		return parseTagTail(rt, rest[1:])
	default:
		return false
	}
}

// parseEntryTail consumes ("entries"-stripped) [entry] and then an optional tag
// tail.
func parseEntryTail(rt *route, rest []string) bool {
	rt.level, rt.name = levelEntry, ""

	if len(rest) == 0 {
		return true // entries collection
	}

	rt.entry, rt.name, rest = rest[0], rest[0], rest[1:]
	if len(rest) == 0 {
		return true // entry item
	}

	if rest[0] != tagsSeg {
		return false
	}

	return parseTagTail(rt, rest[1:])
}

// parseTagTail consumes ("tags"-stripped) [tag].
func parseTagTail(rt *route, rest []string) bool {
	rt.level, rt.name = levelTag, ""

	if len(rest) == 0 {
		return true // tags collection
	}

	rt.name, rest = rest[0], rest[1:]

	return len(rest) == 0 // tag item, else trailing junk
}

// parseTagTemplateTree folds the segments after "tagTemplates" into rt,
// consuming [tt] optionally followed by ("fields"[, field]).
func parseTagTemplateTree(rt *route, rest []string) bool {
	rt.level = levelTagTemplate

	if len(rest) == 0 {
		return true // tagTemplates collection
	}

	rt.tt, rt.name, rest = rest[0], rest[0], rest[1:]
	if len(rest) == 0 {
		return true // tag template item
	}

	if rest[0] != fieldsSeg {
		return false
	}

	rt.level, rt.name, rest = levelTagTemplateField, "", rest[1:]
	if len(rest) == 0 {
		return true // fields collection
	}

	rt.field, rt.name, rest = rest[0], rest[0], rest[1:]

	return len(rest) == 0 // field item, else trailing junk
}

// Matches claims the Data Catalog entryGroups/tagTemplates hierarchies. The
// resource-segment guard keeps it disjoint from every other /v1/projects/
// handler.
func (*Handler) Matches(r *http.Request) bool {
	_, ok := parseRoute(r.URL.Path)

	return ok
}

// rpcSet bundles the collection/item handlers for one resource level so a single
// dispatch can route by method. A nil handler yields a 405 for that method,
// which models the levels that lack an RPC (a tag has no Get; a tag template and
// its fields have no List).
type rpcSet struct {
	create func(http.ResponseWriter, *http.Request, *route)
	list   func(http.ResponseWriter, *http.Request, *route)
	get    func(http.ResponseWriter, *http.Request, *route)
	patch  func(http.ResponseWriter, *http.Request, *route)
	del    func(http.ResponseWriter, *http.Request, *route)
}

// ServeHTTP routes on the parsed path level and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Data Catalog path")
		return
	}

	switch rt.level {
	case levelEntryGroup:
		dispatch(w, r, &rt, rpcSet{h.createEntryGroup, h.listEntryGroups, h.getEntryGroup, h.patchEntryGroup, h.deleteEntryGroup})
	case levelEntry:
		dispatch(w, r, &rt, rpcSet{h.createEntry, h.listEntries, h.getEntry, h.patchEntry, h.deleteEntry})
	case levelTag:
		dispatch(w, r, &rt, rpcSet{create: h.createTag, list: h.listTags, patch: h.patchTag, del: h.deleteTag})
	case levelTagTemplate:
		dispatch(w, r, &rt, rpcSet{create: h.createTagTemplate, get: h.getTagTemplate, patch: h.patchTagTemplate, del: h.deleteTagTemplate})
	case levelTagTemplateField:
		dispatch(w, r, &rt, rpcSet{create: h.createTagTemplateField, patch: h.patchTagTemplateField, del: h.deleteTagTemplateField})
	}
}

// dispatch selects a handler from s by whether the path addresses a collection
// (no id) or an item, then by request method; a nil or unmatched slot is a 405.
func dispatch(w http.ResponseWriter, r *http.Request, rt *route, s rpcSet) {
	var handler func(http.ResponseWriter, *http.Request, *route)

	if rt.name == "" {
		switch r.Method {
		case http.MethodPost:
			handler = s.create
		case http.MethodGet:
			handler = s.list
		}
	} else {
		switch r.Method {
		case http.MethodGet:
			handler = s.get
		case http.MethodPatch:
			handler = s.patch
		case http.MethodDelete:
			handler = s.del
		}
	}

	if handler == nil {
		writeMethodNotAllowed(w)
		return
	}

	handler(w, r, rt)
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
