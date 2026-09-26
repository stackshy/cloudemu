package bedrock

import (
	"encoding/base64"
	"net/http"
	"strconv"
)

// Bedrock List operations accept maxResults in 1..1000.
const (
	minPageSize = 1
	maxPageSize = 1000
)

// paginate applies the maxResults and nextToken query parameters to items. The
// token is an opaque base64 offset. It writes a ValidationException and
// returns ok=false for an out-of-range maxResults or a malformed token.
func paginate[T any](w http.ResponseWriter, r *http.Request, items []T) (page []T, next string, ok bool) {
	q := r.URL.Query()

	start, ok := decodePageToken(q.Get("nextToken"), len(items))
	if !ok {
		writeError(w, http.StatusBadRequest, "ValidationException", "The provided pagination token is invalid.")

		return nil, "", false
	}

	size := len(items)

	if raw := q.Get("maxResults"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < minPageSize || n > maxPageSize {
			writeError(w, http.StatusBadRequest, "ValidationException",
				"1 validation error detected: Value '"+raw+"' at 'maxResults' failed to satisfy constraint: "+
					"Member must have value between 1 and 1000")

			return nil, "", false
		}

		size = n
	}

	items = items[start:]
	if size >= len(items) {
		return items, "", true
	}

	return items[:size], encodePageToken(start + size), true
}

func encodePageToken(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

// decodePageToken returns the start offset for token. An empty token starts
// at 0. A token past the end yields an empty page.
func decodePageToken(token string, total int) (int, bool) {
	if token == "" {
		return 0, true
	}

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, false
	}

	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0, false
	}

	return min(n, total), true
}
