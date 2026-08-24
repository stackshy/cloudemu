package wire

import (
	"encoding/base64"
	"errors"
	"strconv"
)

// ErrInvalidOffsetToken reports that a pagination token/marker could not be
// decoded into a non-negative offset. Callers map it to their protocol-specific
// error (e.g. "invalid Marker" / "invalid NextToken"), or ignore it to reset to
// the start of the list.
var ErrInvalidOffsetToken = errors.New("invalid pagination token")

// EncodeOffset turns a list offset into an opaque base64 pagination token — the
// codec shared by the AWS list handlers whose Marker/NextToken is an offset into
// a deterministically sorted result set.
func EncodeOffset(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

// DecodeOffset parses a token produced by EncodeOffset back into an offset. An
// empty token decodes to 0; a malformed or negative token returns
// ErrInvalidOffsetToken.
func DecodeOffset(token string) (int, error) {
	if token == "" {
		return 0, nil
	}

	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0, ErrInvalidOffsetToken
	}

	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0, ErrInvalidOffsetToken
	}

	return n, nil
}
