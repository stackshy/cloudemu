package compute

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcpfilter"
	"github.com/stackshy/cloudemu/v2/server/wire/gcplist"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// listQuery parses the filter, maxResults and pageToken parameters every
// compute list accepts. On a bad value it writes 400 and returns false.
func listQuery(w http.ResponseWriter, r *http.Request) (*gcpfilter.Filter, gcplist.Params, bool) {
	q := r.URL.Query()

	f, err := gcpfilter.Compile(q.Get("filter"))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid",
			"Invalid value for field 'filter': '"+q.Get("filter")+"'. Invalid list filter expression.")

		return nil, gcplist.Params{}, false
	}

	p, err := gcplist.Compute(q)
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return nil, gcplist.Params{}, false
	}

	return f, p, true
}

// filterPage applies the request's filter to items and returns the requested
// page ordered by name, plus the next page token.
func filterPage[T any](
	w http.ResponseWriter, r *http.Request, items []T, name func(T) string,
) (page []T, next string, ok bool) {
	f, p, ok := listQuery(w, r)
	if !ok {
		return nil, "", false
	}

	kept := make([]T, 0, len(items))

	for _, it := range items {
		if f.Match(it) {
			kept = append(kept, it)
		}
	}

	page, next, err := gcplist.Page(kept, name, p)
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return nil, "", false
	}

	return page, next, true
}

// scopedItem is one aggregated-list entry with its "zones/{zone}" or
// "regions/{region}" key.
type scopedItem[T any] struct {
	scope string
	item  T
}

// aggregatedPage filters and pages an aggregated list across all scopes, in
// scope then name order, and regroups the page by scope.
func aggregatedPage[T any](
	w http.ResponseWriter, r *http.Request, items []scopedItem[T], name func(T) string,
) (grouped map[string][]T, next string, ok bool) {
	f, p, ok := listQuery(w, r)
	if !ok {
		return nil, "", false
	}

	kept := make([]scopedItem[T], 0, len(items))

	for _, it := range items {
		if f.Match(it.item) {
			kept = append(kept, it)
		}
	}

	page, next, err := gcplist.Page(kept, func(s scopedItem[T]) string { return s.scope + "\x00" + name(s.item) }, p)
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return nil, "", false
	}

	grouped = make(map[string][]T)
	for _, s := range page {
		grouped[s.scope] = append(grouped[s.scope], s.item)
	}

	return grouped, next, true
}
