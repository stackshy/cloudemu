package keyspaces

import (
	"encoding/base64"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// paginate returns the slice of items beginning at the offset encoded in token
// and limited to maxResults, plus the token for the next page (nil when the
// page reaches the end). The token is an opaque base64 offset — stable because
// the driver returns results in a deterministic order. A malformed token yields
// a ValidationException.
func paginate[T any](items []T, maxResults *int32, token *string) (page []T, next *string, err error) {
	start, err := decodeToken(token)
	if err != nil {
		return nil, nil, err
	}

	if start > len(items) {
		start = len(items)
	}

	end := len(items)
	if maxResults != nil && int(*maxResults) > 0 && start+int(*maxResults) < end {
		end = start + int(*maxResults)
	}

	if end < len(items) {
		next = encodeToken(end)
	}

	return items[start:end], next, nil
}

func decodeToken(token *string) (int, error) {
	if token == nil || *token == "" {
		return 0, nil
	}

	raw, err := base64.StdEncoding.DecodeString(*token)
	if err != nil {
		return 0, cerrors.New(cerrors.InvalidArgument, "invalid nextToken")
	}

	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0, cerrors.New(cerrors.InvalidArgument, "invalid nextToken")
	}

	return n, nil
}

func encodeToken(offset int) *string {
	s := base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))

	return &s
}
