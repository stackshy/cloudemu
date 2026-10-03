// Package gcplist reads GCP list paging parameters and slices a stable page
// out of a result set. Page tokens are opaque offsets into the list sorted by
// a caller-supplied key, so following nextPageToken visits every item once.
package gcplist

import (
	"errors"
	"net/url"
	"strconv"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
)

// ComputeMaxResults is the Compute Engine default and maximum for maxResults
// (https://cloud.google.com/compute/docs/reference/rest/v1/instances/list).
const ComputeMaxResults = 500

var (
	// ErrInvalidMaxResults reports a maxResults that is not an integer in 0..500.
	ErrInvalidMaxResults = errors.New("invalid value for field 'maxResults': must be between 0 and 500")
	// ErrInvalidPageToken reports a pageToken this server did not issue.
	ErrInvalidPageToken = errors.New("invalid value for field 'pageToken': invalid page token")
)

// Params is one parsed page request.
type Params struct {
	Size  int
	Token string
}

// Compute reads the Compute Engine maxResults / pageToken query parameters.
// An absent or zero maxResults means the default of 500.
func Compute(q url.Values) (Params, error) {
	p := Params{Size: ComputeMaxResults, Token: q.Get("pageToken")}

	raw := q.Get("maxResults")
	if raw == "" {
		return p, nil
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > ComputeMaxResults {
		return Params{}, ErrInvalidMaxResults
	}

	if n > 0 {
		p.Size = n
	}

	return p, nil
}

// Page sorts items by key and returns the page p selects plus the token for
// the next page, which is empty on the last page. A token that does not decode,
// or that points past the end of the list, is rejected.
func Page[T any](items []T, key func(T) string, p Params) (page []T, next string, err error) {
	if p.Token != "" {
		tok, decErr := pagination.DecodeToken(p.Token)
		if decErr != nil || tok.Offset >= len(items) {
			return nil, "", ErrInvalidPageToken
		}
	}

	sorted, err := pagination.PaginateSorted(items, func(a, b T) bool { return key(a) < key(b) }, p.Token, p.Size)
	if err != nil {
		return nil, "", ErrInvalidPageToken
	}

	return sorted.Items, sorted.NextPageToken, nil
}
