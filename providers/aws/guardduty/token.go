package guardduty

import (
	"strconv"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// paginateOrdered returns a page of an already-ordered slice, honoring an opaque
// numeric offset token, without re-sorting. Callers that establish a non-lexical
// order (e.g. sort by timestamp) use this instead of paginateIDs so the page
// respects their ordering. A corrupt or out-of-range token yields a
// BadRequestException.
func paginateOrdered(ids []string, page driver.Page) (out []string, next string, err error) {
	start, err := decodeToken(page.NextToken)
	if err != nil {
		return nil, "", err
	}

	if start > len(ids) {
		return nil, "", badRequest("invalid pagination token: %q", page.NextToken)
	}

	limit := int(page.MaxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	end := start + limit
	if end >= len(ids) {
		return ids[start:], "", nil
	}

	return ids[start:end], encodeToken(end), nil
}

// encodeToken encodes a numeric list offset as an opaque pagination token.
func encodeToken(offset int) string {
	return strconv.Itoa(offset)
}

// decodeToken decodes an opaque pagination token to a numeric offset. An empty
// token decodes to 0 (start from the beginning). A non-empty token that is not a
// non-negative integer is reported as a BadRequestException so the caller learns
// its token was bad rather than silently restarting at page one.
func decodeToken(token string) (offset int, err error) {
	if token == "" {
		return 0, nil
	}

	n, convErr := strconv.Atoi(token)
	if convErr != nil || n < 0 {
		return 0, badRequest("invalid pagination token: %q", token)
	}

	return n, nil
}
