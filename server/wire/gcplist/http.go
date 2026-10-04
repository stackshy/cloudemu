package gcplist

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcpfilter"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// listQuery parses the filter, maxResults and pageToken parameters every
// Compute Engine list accepts. On a bad value it writes 400 and returns false.
func listQuery(w http.ResponseWriter, r *http.Request) (*gcpfilter.Filter, Params, bool) {
	q := r.URL.Query()

	f, err := gcpfilter.Compile(q.Get("filter"))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid",
			"Invalid value for field 'filter': '"+q.Get("filter")+"'. Invalid list filter expression.")

		return nil, Params{}, false
	}

	p, err := Compute(q)
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return nil, Params{}, false
	}

	return f, p, true
}

// FilterPage applies the request's filter to items and returns the requested
// page ordered by name, plus the next page token.
func FilterPage[T any](
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

	page, next, err := Page(kept, name, p)
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return nil, "", false
	}

	return page, next, true
}

// Scoped is one aggregated-list entry with its "zones/{zone}" or
// "regions/{region}" key.
type Scoped[T any] struct {
	Scope string
	Item  T
}

// AggregatedPage filters and pages an aggregated list across all scopes, in
// scope then name order, and regroups the page by scope.
func AggregatedPage[T any](
	w http.ResponseWriter, r *http.Request, items []Scoped[T], name func(T) string,
) (grouped map[string][]T, next string, ok bool) {
	f, p, ok := listQuery(w, r)
	if !ok {
		return nil, "", false
	}

	kept := make([]Scoped[T], 0, len(items))

	for _, it := range items {
		if f.Match(it.Item) {
			kept = append(kept, it)
		}
	}

	page, next, err := Page(kept, func(s Scoped[T]) string { return s.Scope + "\x00" + name(s.Item) }, p)
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return nil, "", false
	}

	grouped = make(map[string][]T)
	for _, s := range page {
		grouped[s.Scope] = append(grouped[s.Scope], s.Item)
	}

	return grouped, next, true
}
