package opensearch

import "strconv"

// encodeToken encodes a numeric list offset as an opaque pagination token.
func encodeToken(offset int) string {
	return strconv.Itoa(offset)
}

// decodeToken decodes an opaque pagination token to a numeric offset. An empty
// or unparseable token decodes to 0 (start from the beginning).
func decodeToken(token string) int {
	if token == "" {
		return 0
	}

	n, err := strconv.Atoi(token)
	if err != nil {
		return 0
	}

	return n
}
