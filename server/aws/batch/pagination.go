package batch

import (
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
)

// Batch Describe* ops accept maxResults between 1 and 100. When it is omitted
// they still return at most 100 results plus a nextToken.
const (
	minDescribeResults     = 1
	maxDescribeResults     = 100
	defaultDescribeResults = 100
)

// paginate slices one page out of an already stably ordered result set. The
// bounds check runs first because pagination.Paginate treats 0 as the default.
// It writes a ClientException and returns ok=false on a bad maxResults or
// nextToken. The returned items are never nil so JSON renders an empty array.
func paginate[T any](w http.ResponseWriter, items []T, maxResults *int32, nextToken string) (page []T, next string, ok bool) {
	limit := defaultDescribeResults

	if maxResults != nil {
		if *maxResults < minDescribeResults || *maxResults > maxDescribeResults {
			writeError(w, http.StatusBadRequest, exceptionClient,
				"maxResults must be between 1 and 100, got "+strconv.Itoa(int(*maxResults)))

			return nil, "", false
		}

		limit = int(*maxResults)
	}

	p, err := pagination.Paginate(items, nextToken, limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, exceptionClient, "invalid nextToken")

		return nil, "", false
	}

	if p.Items == nil {
		p.Items = []T{}
	}

	return p.Items, p.NextPageToken, true
}
