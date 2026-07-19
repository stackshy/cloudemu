package pagination

import "sort"

// Page represents a page of results.
type Page[T any] struct {
	Items         []T
	NextPageToken string
	HasMore       bool
}

// Paginate slices one page out of items using an offset-based token.
//
// CONTRACT: offset tokens are only meaningful over a STABLE ordering — the
// caller must present items in the same order on every call (Go map
// iteration is not stable). Callers that cannot guarantee ordering should
// use PaginateSorted, which enforces it.
func Paginate[T any](items []T, pageToken string, maxResults int) (Page[T], error) {
	if maxResults <= 0 {
		maxResults = 100
	}

	token, err := DecodeToken(pageToken)
	if err != nil {
		return Page[T]{}, err
	}

	offset := token.Offset
	if offset >= len(items) {
		return Page[T]{Items: nil, HasMore: false}, nil
	}

	end := offset + maxResults
	hasMore := false

	if end >= len(items) {
		end = len(items)
	} else {
		hasMore = true
	}

	page := Page[T]{
		Items:   items[offset:end],
		HasMore: hasMore,
	}

	if hasMore {
		page.NextPageToken = EncodeToken(end)
	}

	return page, nil
}

// PaginateSorted stable-sorts items with less, then paginates. This is the
// misuse-proof entry point for offset tokens: the required ordering
// invariant is enforced here rather than trusted to the caller.
func PaginateSorted[T any](items []T, less func(a, b T) bool, pageToken string, maxResults int) (Page[T], error) {
	sort.SliceStable(items, func(i, j int) bool { return less(items[i], items[j]) })
	return Paginate(items, pageToken, maxResults)
}
