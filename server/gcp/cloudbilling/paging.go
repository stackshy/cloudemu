package cloudbilling

import (
	"encoding/base64"
	"net/http"
	"strconv"
)

// paginate applies GCP-style pageSize/pageToken offset pagination to items,
// returning the requested page and the nextPageToken (empty when the page is
// the last one). A missing or non-positive pageSize returns everything.
func paginate[T any](items []T, r *http.Request) (page []T, nextPageToken string) {
	start := decodePageToken(r.URL.Query().Get("pageToken"))
	if start > len(items) {
		start = len(items)
	}

	rest := items[start:]

	size := pageSize(r)
	if size > 0 && size < len(rest) {
		return rest[:size], encodePageToken(start + size)
	}

	return rest, ""
}

// pageSize parses the pageSize query param, returning 0 when absent or invalid.
func pageSize(r *http.Request) int {
	raw := r.URL.Query().Get("pageSize")
	if raw == "" {
		return 0
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}

	return n
}

// encodePageToken/decodePageToken carry an offset as an opaque base64 token.
func encodePageToken(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodePageToken(tok string) int {
	if tok == "" {
		return 0
	}

	b, err := base64.StdEncoding.DecodeString(tok)
	if err != nil {
		return 0
	}

	n, err := strconv.Atoi(string(b))
	if err != nil || n < 0 {
		return 0
	}

	return n
}
