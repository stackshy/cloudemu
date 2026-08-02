package kubernetes

import (
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
)

// listPage slices items for a `?limit=&continue=` list request (kubectl's
// chunked listing / client-go pager). When limit is absent or non-positive the
// full slice is returned with an empty token, preserving the unpaginated
// default. The returned string is the value for list metadata.continue — "" on
// the final (or only) page. Items MUST already be in a stable sort order; every
// list path here sorts by key before calling this.
//
// The continue token is an opaque base64 offset; because the store's offsets
// never expire, a malformed token falls back to the full list rather than the
// real apiserver's 410 Gone — a documented emulation simplification.
func listPage[T any](items []T, r *http.Request) (page []T, cont string) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		return items, ""
	}

	p, err := pagination.Paginate(items, r.URL.Query().Get("continue"), limit)
	if err != nil {
		return items, ""
	}

	return p.Items, p.NextPageToken
}
