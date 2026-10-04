package aws

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/stackshy/cloudemu/v2/server"
)

// probeRoute finds the handler that dispatch will pick for r, by running the
// dispatcher's own first-match lookup (match) on a copy of r. Both the public
// exemption and the authorization plan are bound to that handler, so a
// request cannot borrow a marker (a Host, a path, a target) of one service
// while being served by another.
//
// The probe gets fresh form state and the same body bytes that dispatch will
// read, so the lookup and the real dispatch see identical input. A form body
// or query string that does not parse returns ok=false: handlers that parse
// forms could otherwise disagree about what the request is, so callers treat
// it as fail-closed.
//
// The probe's body is left exactly as the Matches calls left it, because the
// real request's body will be in that same state when the handler serves it.
// A Resolver reading the probe therefore sees the bytes dispatch sees. That
// holds only if every Matches that peeks at the body puts it back whole (see
// wire.PeekBody); TestMatchesLeaveBodyIntact checks it for every handler.
func probeRoute(
	r *http.Request, body []byte, match func(*http.Request) server.Handler,
) (probe *http.Request, h server.Handler, ok bool) {
	if _, err := url.ParseQuery(r.URL.RawQuery); err != nil {
		return nil, nil, false
	}

	if strings.HasPrefix(r.Header.Get("Content-Type"), urlEncodedForm) {
		if _, err := url.ParseQuery(string(body)); err != nil {
			return nil, nil, false
		}
	}

	probe = r.Clone(r.Context())
	probe.Form, probe.PostForm, probe.MultipartForm = nil, nil, nil
	probe.Body = io.NopCloser(bytes.NewReader(body))

	h = match(probe)

	return probe, h, true
}

// servedPublicly reports whether h serves the probed request as an operation
// AWS serves without credentials (a noAuth operation), which skips both
// authentication and authorization. A handler answers true only for the
// public routes it really serves, never for its private ones, and a request
// no handler would serve is not public.
func servedPublicly(probe *http.Request, h server.Handler) bool {
	pub, ok := h.(server.PublicRequester)

	return ok && pub.PublicRequest(probe)
}

const urlEncodedForm = "application/x-www-form-urlencoded"
