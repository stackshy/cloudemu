package cloudrun

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
)

// serviceHostMarker identifies an inbound request as an invoke through a
// deployed service's generated URL rather than an Admin API call: real Cloud
// Run services are addressed by a host of the form
// https://{service}-{hash}.{region}.run.app (see services.go's serviceURI),
// not by the /v2/projects/... Admin API path every other operation in this
// package uses, so invoke traffic routes on the Host header instead — the
// same approach the AWS Lambda Function URL handler uses for its own
// generated *.lambda-url.* hosts.
const serviceHostMarker = ".run.app"

// isServiceHost reports whether host (an incoming request's Host header,
// optionally with a port) addresses a generated Cloud Run service URL.
func isServiceHost(host string) bool {
	return strings.HasSuffix(requestHost(host), serviceHostMarker)
}

// requestHost strips an optional port from an HTTP Host header and
// lower-cases the remainder.
func requestHost(host string) string {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}

	return strings.ToLower(h)
}

// serveServiceInvoke handles a request addressed to a deployed service's
// generated host, resolving the service and running it through the CloudRun
// driver's Invoke choke point (a registered Go handler, or the echo stub —
// see providers/gcp/cloudrun/invoke.go).
//
// ctx carries the re-entrant delivery depth (internal/recursionguard): a
// service whose handler calls back into its own URL would otherwise recurse
// unbounded, so once the depth carried on recursionguard.DepthHeader reaches
// recursionguard.MaxDepth this invoke is refused with 508 Loop Detected
// instead of running the handler again. A handler that itself calls another
// (or its own) service URL is responsible for forwarding the incremented
// depth on that outbound request via recursionguard.DepthHeader, the same
// bridge Event Grid webhook delivery and the GCS/Pub/Sub trigger paths use to
// carry the depth across an HTTP hop.
func (h *Handler) serveServiceInvoke(w http.ResponseWriter, r *http.Request) {
	host := requestHost(r.Host)

	svc, err := h.resolveServiceByHost(r, host)
	if err != nil {
		writeErr(w, err)
		return
	}

	depth := inboundDepth(r)
	if depth >= recursionguard.MaxDepth {
		writeError(w, http.StatusLoopDetected, "LOOP_DETECTED",
			"recursive Cloud Run invocation limit reached for service "+svc.Name)

		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "INVALID_ARGUMENT", err.Error())
		return
	}

	ctx := recursionguard.WithDepth(r.Context(), depth+1)

	resp, err := h.cr.Invoke(ctx, svc.Name, driver.InvokeRequest{
		Method:  r.Method,
		Path:    r.URL.EscapedPath(),
		Query:   r.URL.RawQuery,
		Headers: map[string][]string(r.Header),
		Body:    body,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeInvokeResponse(w, resp)
}

// inboundDepth reads the re-entrant delivery depth carried on
// recursionguard.DepthHeader, defaulting to 0 for the start of a new chain.
func inboundDepth(r *http.Request) int {
	d, err := strconv.Atoi(r.Header.Get(recursionguard.DepthHeader))
	if err != nil || d < 0 {
		return 0
	}

	return d
}

// resolveServiceByHost finds the stored service whose generated URL host
// matches host, or a NotFound error when none does — the shape a real Cloud
// Run host that addresses no deployed service would 404 as.
func (h *Handler) resolveServiceByHost(r *http.Request, host string) (*driver.Service, error) {
	svcs, err := h.cr.ListServices(r.Context())
	if err != nil {
		return nil, err
	}

	for i := range svcs {
		if uriHost(svcs[i].URI) == host {
			return &svcs[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "no Cloud Run service found for host %q", host)
}

// uriHost extracts and lower-cases the host of a service's serving URL.
func uriHost(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return ""
	}

	return strings.ToLower(u.Host)
}

// writeInvokeResponse writes a driver.InvokeResponse to w verbatim: status
// (defaulting to 200), headers, and body — a real HTTP passthrough rather
// than the JSON envelope every other Cloud Run Admin API response uses.
func writeInvokeResponse(w http.ResponseWriter, resp *driver.InvokeResponse) {
	for k, vals := range resp.Headers {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}

	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}

	w.WriteHeader(status)
	_, _ = w.Write(resp.Body)
}
