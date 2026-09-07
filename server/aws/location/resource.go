package location

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

// serveList decodes the shared {MaxResults, NextToken} list body, calls the
// driver list operation and renders each item with toEntry into the standard
// {Entries, NextToken} response. It centralizes the list mechanics all five
// resource types share.
func serveList[T any](
	w http.ResponseWriter,
	r *http.Request,
	list func(context.Context, driver.Page) ([]T, string, error),
	toEntry func(*T) map[string]any,
) {
	var req struct {
		MaxResults int32  `json:"MaxResults"`
		NextToken  string `json:"NextToken"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	items, next, err := list(r.Context(), driver.Page{NextToken: req.NextToken, MaxResults: req.MaxResults})
	if err != nil {
		writeErr(w, err)

		return
	}

	entries := make([]map[string]any, 0, len(items))
	for i := range items {
		entries = append(entries, toEntry(&items[i]))
	}

	writeList(w, entries, next)
}

// resource carries the routing shape and driver glue for one Location resource
// type. All five resource types share the same URL shape — a create/list
// collection and verb-keyed item operations — so the routing lives here once and
// each resource file supplies only the typed handler closures.
type resource struct {
	coll     string // collection segment, e.g. "maps"
	listPath string // list sub-path segment, e.g. "list-maps"

	create   http.HandlerFunc
	list     http.HandlerFunc
	describe func(w http.ResponseWriter, r *http.Request, name string)
	update   func(w http.ResponseWriter, r *http.Request, name string)
	del      func(w http.ResponseWriter, r *http.Request, name string)
}

// serve routes the path segments below /{root}/v0/ for this resource.
func (res *resource) serve(w http.ResponseWriter, r *http.Request, rest []string) {
	switch {
	case len(rest) == 1 && rest[0] == res.coll:
		res.serveCollection(w, r)
	case len(rest) == 1 && rest[0] == res.listPath:
		res.serveList(w, r)
	case len(rest) == 2 && rest[0] == res.coll:
		res.serveItem(w, r, rest[1])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveCollection handles POST /{root}/v0/{coll} (create).
func (res *resource) serveCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	res.create(w, r)
}

// serveList handles POST /{root}/v0/{list-path} (list).
func (res *resource) serveList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	res.list(w, r)
}

// serveItem handles the verb-keyed operations on a single resource.
func (res *resource) serveItem(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		res.describe(w, r, name)
	case http.MethodPatch:
		res.update(w, r, name)
	case http.MethodDelete:
		res.del(w, r, name)
	default:
		methodNotAllowed(w)
	}
}
