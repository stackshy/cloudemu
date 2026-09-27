package apigatewayv2

import (
	"strconv"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

// listPage reads one of an API's sub-collections under its read lock and
// returns the page that in selects.
func listPage[V, T any](
	m *Mock, apiID string, pick func(*apiData) map[string]*V, render func(*V) T,
	less func(a, b T) bool, in *driver.PageInput,
) (items []T, next string, err error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, "", err
	}

	ad.mu.RLock()
	src := pick(ad)
	all := make([]T, 0, len(src))

	for _, v := range src {
		all = append(all, render(v))
	}
	ad.mu.RUnlock()

	return pageOf(all, less, in)
}

// pageOf sorts items by less and returns the page that in selects plus the
// next token. An omitted MaxResults returns every remaining item.
func pageOf[T any](items []T, less func(a, b T) bool, in *driver.PageInput) (page []T, next string, err error) {
	var maxResults, token string
	if in != nil {
		maxResults, token = in.MaxResults, in.NextToken
	}

	size := len(items)

	if maxResults != "" {
		n, convErr := strconv.Atoi(maxResults)
		if convErr != nil || n < 1 {
			return nil, "", badRequest("MaxResults must be a positive integer")
		}

		size = n
	}

	pg, err := pagination.PaginateSorted(items, less, token, max(size, 1))
	if err != nil {
		return nil, "", badRequest("Invalid NextToken specified")
	}

	page = pg.Items
	if page == nil {
		page = []T{}
	}

	return page, pg.NextPageToken, nil
}
