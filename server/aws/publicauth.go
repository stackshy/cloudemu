package aws

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/stackshy/cloudemu/v2/server"
)

// exemptPublic reports whether r may skip the SigV4 gate because it is an
// operation AWS serves without credentials (a noAuth operation).
//
// The decision belongs to the handler that dispatch will pick for this exact
// request: the gate runs the dispatcher's own first-match lookup (match) on a
// probe copy of r and asks that handler, through server.PublicRequester,
// whether it serves r as a public operation. A handler answers true only for
// the public routes it really serves, never for its private ones, so a
// request cannot borrow a public marker (a Host, a path, a target) while being
// served as something else. When no handler would serve r, it is not exempt.
//
// The probe gets fresh form state and the same body bytes that dispatch will
// read, so the lookup and the real dispatch see identical input. A form body
// or query string that does not parse fails closed, since handlers that parse
// forms could otherwise disagree about what the request is.
func exemptPublic(r *http.Request, body []byte, match func(*http.Request) server.Handler) bool {
	if _, err := url.ParseQuery(r.URL.RawQuery); err != nil {
		return false
	}

	if strings.HasPrefix(r.Header.Get("Content-Type"), urlEncodedForm) {
		if _, err := url.ParseQuery(string(body)); err != nil {
			return false
		}
	}

	probe := r.Clone(r.Context())
	probe.Form, probe.PostForm, probe.MultipartForm = nil, nil, nil
	probe.Body = io.NopCloser(bytes.NewReader(body))

	pub, ok := match(probe).(server.PublicRequester)
	if !ok {
		return false
	}

	probe.Body = io.NopCloser(bytes.NewReader(body))

	return pub.PublicRequest(probe)
}

const urlEncodedForm = "application/x-www-form-urlencoded"
