package efs

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
)

// defaultPageSize is the page size EFS applies when MaxItems/MaxResults is
// absent or non-positive on a Describe request.
const defaultPageSize = 100

// parseMax reads a positive page-size query value (MaxItems/MaxResults),
// falling back to def when the value is missing or not a positive integer.
func parseMax(raw string, def int) int {
	if raw == "" {
		return def
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}

	return n
}

// respondPaged renders a paginated EFS Describe response. When listErr is nil it
// stable-sorts items by less, slices the page named by token, maps each element
// to its wire shape with toWire, and writes the JSON built by build. A driver
// error is surfaced via writeErr; a malformed token is an EFS BadRequest. In
// either error case no success response is written.
func respondPaged[T, W any](
	w http.ResponseWriter, items []T, listErr error, less func(a, b *T) bool, token string, maxItems int,
	toWire func(*T) W, build func(out []W, next string) any,
) {
	if listErr != nil {
		writeErr(w, listErr)
		return
	}

	// Stable-sort (via element pointers, so large records are not copied per
	// comparison) before offset-paging: an unsorted slice would let the same
	// offset duplicate or skip a record across page boundaries.
	sort.SliceStable(items, func(i, j int) bool { return less(&items[i], &items[j]) })

	page, err := pagination.Paginate(items, token, maxItems)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid pagination token")
		return
	}

	out := make([]W, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, toWire(&page.Items[i]))
	}

	writeJSON(w, build(out, page.NextPageToken))
}
