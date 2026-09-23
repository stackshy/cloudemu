package cloudwatch

import (
	"encoding/base64"
	"strconv"
)

// decodeOffsetToken parses a NextToken made by encodeOffsetToken. An empty
// token means the first page. ok is false for a token this handler did not
// issue, and the offset is then 0.
func decodeOffsetToken(tok string) (offset int, ok bool) {
	if tok == "" {
		return 0, true
	}

	raw, err := base64.StdEncoding.DecodeString(tok)
	if err != nil {
		return 0, false
	}

	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0, false
	}

	return n, true
}

// offsetFromToken decodes a NextToken. A bad token returns a wireError with
// the given code, since ops document different codes for it.
func offsetFromToken(tok, code string) (int, error) {
	n, ok := decodeOffsetToken(tok)
	if !ok {
		return 0, newWireError(code, "The value "+tok+" for parameter NextToken is invalid.")
	}

	return n, nil
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
