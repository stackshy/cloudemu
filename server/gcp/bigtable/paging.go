package bigtable

import (
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

const (
	defaultPageSize = 100
	maxPageSize     = 1000
)

// pageParams reads the ?pageSize / ?pageToken list query params. An absent,
// non-numeric, or non-positive pageSize falls back to the default; larger
// values are capped. token is the opaque offset cursor (empty for the first
// page).
func pageParams(r *http.Request) (size int, token string) {
	size = defaultPageSize

	if n, err := strconv.Atoi(r.URL.Query().Get("pageSize")); err == nil && n > 0 {
		size = n
	}

	if size > maxPageSize {
		size = maxPageSize
	}

	return size, r.URL.Query().Get("pageToken")
}

// paginate slices one page out of items (which the driver already returns in a
// stable name order) using the request's pageSize/pageToken. It returns the
// page items and the nextPageToken (empty on the last page). On a malformed
// token it writes a 400 and returns ok=false.
func paginate[T any](w http.ResponseWriter, r *http.Request, items []T) (page []T, nextPageToken string, ok bool) {
	size, token := pageParams(r)

	p, err := pagination.Paginate(items, token, size)
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return nil, "", false
	}

	return p.Items, p.NextPageToken, true
}
