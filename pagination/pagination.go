package pagination

// Page represents a page of results.
type Page[T any] struct {
	Items         []T
	NextPageToken string
	HasMore       bool
}

// Paginate paginates a slice of items given a page token and max results per page.
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
