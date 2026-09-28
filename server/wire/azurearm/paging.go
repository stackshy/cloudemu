package azurearm

import (
	"net/http"
	"strconv"
)

// DefaultPageSize is how many items a paged ARM list returns before it emits a
// nextLink.
const DefaultPageSize = 100

// Query parameters a paged ARM list reads: $skip resumes a listing at an
// offset, $top caps how many items one page holds.
const (
	skipParam = "$skip"
	topParam  = "$top"
)

// Paginate returns the page of items a list request asks for and the nextLink
// that continues it. The page starts at the request's $skip offset and holds at
// most pageSize items (fewer when the request sets a smaller $top). When items
// remain, nextLink is an absolute URL that repeats the request (api-version,
// $top and any filter included) with $skip advanced; ARM SDK pagers GET it
// verbatim until it is empty, so it carries the scheme and host. A missing or
// malformed $skip/$top is ignored.
func Paginate[T any](r *http.Request, items []T, pageSize int) (page []T, nextLink string) {
	if top := queryInt(r, topParam); top > 0 && top < pageSize {
		pageSize = top
	}

	skip := queryInt(r, skipParam)
	if skip >= len(items) {
		return []T{}, ""
	}

	end := skip + pageSize
	if end >= len(items) {
		return items[skip:], ""
	}

	return items[skip:end], nextPageLink(r, end)
}

// queryInt reads a non-negative integer query parameter, 0 when it is missing
// or malformed.
func queryInt(r *http.Request, name string) int {
	n, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || n < 0 {
		return 0
	}

	return n
}

// nextPageLink builds the absolute URL that continues a listing at offset skip.
func nextPageLink(r *http.Request, skip int) string {
	next := *r.URL
	next.Host = r.Host

	next.Scheme = "http"
	if r.TLS != nil {
		next.Scheme = "https"
	}

	q := next.Query()
	q.Set(skipParam, strconv.Itoa(skip))
	next.RawQuery = q.Encode()

	return next.String()
}
