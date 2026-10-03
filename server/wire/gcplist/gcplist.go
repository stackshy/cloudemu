// Package gcplist reads GCP list paging parameters and slices a stable page
// out of a result set. Page tokens are keyset cursors: each one carries the
// sort key of the last item returned, and the next page starts strictly after
// it. Items inserted or deleted between pages therefore never repeat an item
// or invalidate the token, as with real GCP cursors.
package gcplist

import (
	"encoding/base64"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// ComputeMaxResults is the Compute Engine default and maximum for maxResults
// (https://cloud.google.com/compute/docs/reference/rest/v1/instances/list).
const ComputeMaxResults = 500

const tokenPrefix = "after:"

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

// Page sorts items by key (keys must be unique) and returns the page p
// selects plus the token for the next page, which is empty on the last page.
// The page is never nil, so an empty list encodes as []. Only a token that
// does not decode is rejected.
func Page[T any](items []T, key func(T) string, p Params) (page []T, next string, err error) {
	sort.SliceStable(items, func(i, j int) bool { return key(items[i]) < key(items[j]) })

	start := 0

	if p.Token != "" {
		after, ok := decodeToken(p.Token)
		if !ok {
			return nil, "", ErrInvalidPageToken
		}

		start = sort.Search(len(items), func(i int) bool { return key(items[i]) > after })
	}

	end := min(start+p.Size, len(items))

	page = make([]T, 0, end-start)
	page = append(page, items[start:end]...)

	if end < len(items) && end > start {
		next = base64.RawURLEncoding.EncodeToString([]byte(tokenPrefix + key(items[end-1])))
	}

	return page, next, nil
}

func decodeToken(token string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", false
	}

	after, ok := strings.CutPrefix(string(raw), tokenPrefix)

	return after, ok
}
