package cloudwatch

import (
	"encoding/base64"
	"strconv"
)

// decodeOffsetToken parses a NextToken produced by encodeOffsetToken back into a
// slice offset. An empty token means "start from the beginning"; a malformed
// token is treated as offset 0 so a stray token never wedges pagination.
func decodeOffsetToken(tok string) int {
	if tok == "" {
		return 0
	}

	raw, err := base64.StdEncoding.DecodeString(tok)
	if err != nil {
		return 0
	}

	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0
	}

	return n
}

// encodeOffsetToken renders a slice offset as an opaque NextToken.
func encodeOffsetToken(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

// pageWindow resolves [from,to) and the next-page offset for a slice of the
// given length, paged in fixed-size chunks. next is 0 when no further page
// remains.
func pageWindow(total, start, size int) (from, to, next int) {
	if start > total {
		start = total
	}

	end := start + size
	if end >= total {
		return start, total, 0
	}

	return start, end, end
}
