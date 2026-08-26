// Package pagination provides generic pagination utilities for cloudemu services.
package pagination

import (
	"encoding/base64"
	"encoding/json"
	"errors"
)

// ErrInvalidToken indicates a page token that decoded successfully as
// base64+JSON but carries a semantically invalid value (e.g. a negative
// offset). Callers can map it to their API's invalid-token response
// (InvalidParameterValue / InvalidNextTokenException / etc.) instead of
// panicking on an out-of-range slice bound.
var ErrInvalidToken = errors.New("pagination: invalid page token")

// PageToken holds pagination state.
type PageToken struct {
	Offset int `json:"offset"`
}

// EncodeToken encodes pagination state into a base64 string.
func EncodeToken(offset int) string {
	t := PageToken{Offset: offset}
	data, _ := json.Marshal(t)

	return base64.StdEncoding.EncodeToString(data)
}

// DecodeToken decodes a page token string into pagination state.
// Returns offset 0 for empty tokens.
func DecodeToken(token string) (PageToken, error) {
	if token == "" {
		return PageToken{Offset: 0}, nil
	}

	data, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return PageToken{}, err
	}

	var t PageToken
	if err := json.Unmarshal(data, &t); err != nil {
		return PageToken{}, err
	}

	// A negative offset is never a token we produced; treat it as invalid
	// rather than letting it flow through to a slice bound (which panics).
	if t.Offset < 0 {
		return PageToken{}, ErrInvalidToken
	}

	return t, nil
}
