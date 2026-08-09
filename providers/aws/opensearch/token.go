package opensearch

import "strconv"

// encodeToken encodes a numeric list offset as an opaque pagination token.
func encodeToken(offset int) string {
	return strconv.Itoa(offset)
}

// decodeToken decodes an opaque pagination token to a numeric offset. An empty
// token decodes to 0 (start from the beginning). A non-empty token that is not a
// non-negative integer is reported as invalid so the caller can raise
// InvalidPaginationTokenException rather than silently restart at page one.
func decodeToken(token string) (offset int, err error) {
	if token == "" {
		return 0, nil
	}

	n, convErr := strconv.Atoi(token)
	if convErr != nil || n < 0 {
		return 0, invalidToken("Invalid pagination token: %q", token)
	}

	return n, nil
}
