// Package pagination provides generic pagination utilities for cloudemu services.
package pagination

import (
	"encoding/base64"
	"encoding/json"
)

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
	return t, nil
}
