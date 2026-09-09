// Package grafana implements the Amazon Managed Grafana control-plane API
// (restJson1) as a server.Handler. Point the real aws-sdk-go-v2/service/grafana
// client (or the `aws grafana` CLI, or the aws_grafana_workspace Terraform
// resource) at a Server registered with this handler and the workspace,
// configuration, authentication and tagging operations work end-to-end against
// an in-memory driver.
//
// Grafana routes by HTTP verb + path at the root (e.g. POST /workspaces,
// GET /workspaces/{id}, PUT /workspaces/{id}/configuration, POST /tags/{arn});
// there is no X-Amz-Target header and no version prefix. Matches claims the
// /workspaces tree and the /tags paths carrying a Grafana ARN (scoped by the
// ":grafana:" marker), so it runs before the S3 catch-all and never shadows a
// sibling service's tag operations.
package grafana

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/sigv4"
	"github.com/stackshy/cloudemu/v2/services/grafana/driver"
)

// sigV4ServiceAPS is the SigV4 credential-scope service the Amazon Managed
// Prometheus (amp) SDK signs under. Grafana and APS both root workspace CRUD at
// /workspaces, so Grafana yields that tree when the request is signed for APS.
const sigV4ServiceAPS = "aps"

// Path roots at the service root.
const (
	rootWorkspaces = "workspaces"
	rootTags       = "tags"
)

// Sub-resource path segments under /workspaces/{id}.
const (
	subAuthentication = "authentication"
	subConfiguration  = "configuration"
)

// arnMarker scopes the shared /tags root to Grafana ARNs.
const arnMarker = ":grafana:"

// Path-tail lengths under /workspaces: a single {id} segment, or {id}/{sub}.
const (
	depthItem        = 1
	depthSubResource = 2
)

// Handler serves Amazon Managed Grafana requests against a driver.
type Handler struct {
	g driver.Grafana
}

// New returns a Grafana handler backed by d.
func New(d driver.Grafana) *Handler {
	return &Handler{g: d}
}

// Matches claims the Grafana path shapes. The /workspaces tree is unique to
// Grafana. The /tags root is shared with other restJson1 services, so it is
// claimed only for Grafana ARNs; a non-Grafana ARN falls through.
func (*Handler) Matches(r *http.Request) bool {
	segs := splitPath(escapedPath(r))
	if len(segs) == 0 {
		return false
	}

	switch segs[0] {
	case rootWorkspaces:
		// The /workspaces tree is shared with APS. When the request is SigV4-signed
		// for the amp (aps) service it belongs to APS, so Grafana yields; an
		// unsigned or grafana-signed request stays with Grafana (registered first).
		if sigv4.Service(r) == sigV4ServiceAPS {
			return false
		}

		return matchesWorkspaces(r.Method, segs[1:])
	case rootTags:
		return len(segs) >= 2 && strings.Contains(strings.Join(segs[1:], "/"), arnMarker)
	default:
		return false
	}
}

// matchesWorkspaces reports whether the /workspaces path tail is a Grafana
// operation. A deeper or unknown sub-resource falls through to the S3 catch-all.
func matchesWorkspaces(method string, rest []string) bool {
	switch len(rest) {
	case 0:
		return method == http.MethodGet || method == http.MethodPost
	case depthItem:
		return true
	case depthSubResource:
		return rest[1] == subAuthentication || rest[1] == subConfiguration
	default:
		return false
	}
}

// ServeHTTP dispatches a Grafana request on its path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(escapedPath(r))
	if len(segs) == 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch segs[0] {
	case rootWorkspaces:
		h.serveWorkspaces(w, r, segs[1:])
	case rootTags:
		h.serveTags(w, r, segs[1:])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveWorkspaces routes the /workspaces tree.
func (h *Handler) serveWorkspaces(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		h.serveWorkspaceCollection(w, r)
	case depthItem:
		h.serveWorkspaceItem(w, r, rest[0])
	case depthSubResource:
		h.serveWorkspaceSubResource(w, r, rest[0], rest[1])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveWorkspaceCollection routes GET /workspaces (list) and POST /workspaces
// (create).
func (h *Handler) serveWorkspaceCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listWorkspaces(w, r)
	case http.MethodPost:
		h.createWorkspace(w, r)
	default:
		methodNotAllowed(w)
	}
}

// serveWorkspaceItem routes the verb-keyed operations on a single workspace.
func (h *Handler) serveWorkspaceItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.describeWorkspace(w, r, id)
	case http.MethodPut:
		h.updateWorkspace(w, r, id)
	case http.MethodDelete:
		h.deleteWorkspace(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

// serveWorkspaceSubResource routes the /authentication and /configuration
// sub-resources of a single workspace.
func (h *Handler) serveWorkspaceSubResource(w http.ResponseWriter, r *http.Request, id, sub string) {
	switch sub {
	case subConfiguration:
		h.serveConfiguration(w, r, id)
	case subAuthentication:
		h.serveAuthentication(w, r, id)
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// escapedPath returns the request path preserving percent-encoding so a path
// label that contains a slash (an ARN) survives as one segment.
func escapedPath(r *http.Request) string {
	if r.URL.RawPath != "" {
		return r.URL.RawPath
	}

	return r.URL.EscapedPath()
}

// splitPath splits a URL path into its non-empty segments, percent-decoding
// each segment so an ARN or id label is delivered whole to a handler.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}

	raw := strings.Split(p, "/")
	out := make([]string, 0, len(raw))

	for _, seg := range raw {
		if dec, err := url.PathUnescape(seg); err == nil {
			out = append(out, dec)
		} else {
			out = append(out, seg)
		}
	}

	return out
}
