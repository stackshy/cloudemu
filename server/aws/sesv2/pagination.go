package sesv2

import (
	"encoding/base64"
	"net/url"
	"strconv"
)

// pageWindow computes the [start,end) slice bounds for a SESv2 list request from
// its NextToken/PageSize query parameters and the total number of (already
// sorted) items. NextToken is an opaque base64-encoded offset into the list; an
// empty returned token means the last page was reached.
func pageWindow(total int, q url.Values) (start, end int, next string) {
	start = decodePageToken(q.Get("NextToken"))
	if start > total {
		start = total
	}

	end = total

	if n, err := strconv.Atoi(q.Get("PageSize")); err == nil && n > 0 && start+n < total {
		end = start + n
		next = encodePageToken(end)
	}

	return start, end, next
}

// decodePageToken parses an opaque NextToken back into an offset. A missing or
// malformed token resets to the start of the list.
func decodePageToken(token string) int {
	if token == "" {
		return 0
	}

	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0
	}

	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0
	}

	return n
}

// encodePageToken turns an offset into an opaque NextToken.
func encodePageToken(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}
