package resourcegroupstaggingapi

import (
	"encoding/base64"
	"strconv"
)

// page resolves the [from,to) window and the next PaginationToken for a slice of
// the given length, resuming from token and chunking in size-element pages. A
// size <= 0 means "no client-imposed page size" — the rest of the slice is
// returned in one page. next is "" when no further page remains.
func page(total int, token string, size int) (from, to int, next string) {
	start := decodeOffsetToken(token)
	if start > total {
		start = total
	}

	if size <= 0 {
		size = total - start
	}

	end := start + size
	if end >= total {
		return start, total, ""
	}

	return start, end, encodeOffsetToken(end)
}

// decodeOffsetToken parses a PaginationToken produced by encodeOffsetToken back
// into a slice offset. An empty or malformed token yields offset 0 so a stray
// token never wedges pagination.
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

// encodeOffsetToken renders a slice offset as an opaque PaginationToken.
func encodeOffsetToken(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}
