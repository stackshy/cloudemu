package codeartifact

import (
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

const (
	defaultMaxResults = 100
	maxMaxResults     = 1000
	tokenPrefix       = "ca-"
)

// paginate returns the window [start, end) and the next token for a result set
// of n items. The token is an opaque offset; one this mock did not issue, or a
// maxResults outside 1..1000, is a ValidationException (never a silent first
// page).
func paginate(n int, page driver.Page) (start, end int, next string, err error) {
	if page.MaxResults < 0 || page.MaxResults > maxMaxResults {
		return 0, 0, "", validation("maxResults must be between 1 and %d", maxMaxResults)
	}

	start, err = decodeToken(page.NextToken)
	if err != nil {
		return 0, 0, "", err
	}

	if start > n {
		start = n
	}

	limit := int(page.MaxResults)
	if limit == 0 {
		limit = defaultMaxResults
	}

	end = start + limit
	if end >= n {
		return start, n, "", nil
	}

	return start, end, tokenPrefix + strconv.Itoa(end), nil
}

func decodeToken(token string) (int, error) {
	if token == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(strings.TrimPrefix(token, tokenPrefix))
	if err != nil || n < 0 || !strings.HasPrefix(token, tokenPrefix) {
		return 0, validation("nextToken %q is not valid", token)
	}

	return n, nil
}

// pageOf returns the items of one page of a sorted result set.
func pageOf[T any](items []T, page driver.Page) (out []T, next string, err error) {
	start, end, next, err := paginate(len(items), page)
	if err != nil {
		return nil, "", err
	}

	return items[start:end], next, nil
}
