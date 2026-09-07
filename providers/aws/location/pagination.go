package location

import (
	"strconv"

	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

// paginate returns the [start,end) window and next token for a slice of length
// n, honoring an opaque numeric offset token in page.NextToken.
func paginate(n int, page driver.Page) (start, end int, next string) {
	start = decodeToken(page.NextToken)
	if start > n {
		start = n
	}

	limit := int(page.MaxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	end = start + limit
	if end >= n {
		return start, n, ""
	}

	return start, end, strconv.Itoa(end)
}

// decodeToken decodes an opaque pagination token to a numeric offset. An empty
// or malformed token decodes to 0 (start from the beginning).
func decodeToken(token string) int {
	if token == "" {
		return 0
	}

	n, err := strconv.Atoi(token)
	if err != nil || n < 0 {
		return 0
	}

	return n
}
