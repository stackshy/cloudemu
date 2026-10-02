package wire

import (
	"bytes"
	"io"
	"net/http"
)

// PeekBody reads up to limit bytes of r's body for a Matches predicate to
// inspect, then puts them back in front of the unread rest. The handler that
// serves r, and the auth gate that authorizes it, therefore read the whole
// body byte for byte, however large it is. Closing the body closes the
// original.
func PeekBody(r *http.Request, limit int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}

	peeked, err := io.ReadAll(io.LimitReader(r.Body, limit))
	r.Body = peekedBody{Reader: io.MultiReader(bytes.NewReader(peeked), r.Body), Closer: r.Body}

	return peeked, err
}

// peekedBody is a request body whose first bytes were already read.
type peekedBody struct {
	io.Reader
	io.Closer
}
