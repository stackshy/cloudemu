// Package mwaa implements the Amazon Managed Workflows for Apache Airflow (MWAA)
// control-plane API (REST-JSON, awsRestjson1) as a server.Handler. Point the
// real aws-sdk-go-v2/service/mwaa client (or the `aws mwaa` CLI, or the
// aws_mwaa_environment Terraform resource) at a Server registered with this
// handler and the environment, tagging and token operations work end-to-end
// against an in-memory driver.
//
// MWAA routes by HTTP verb + path at the root (e.g. PUT /environments/{Name},
// GET /environments, POST /tags/{resourceArn}); there is no X-Amz-Target header
// and no version prefix. Matches claims the /environments tree, the /tags paths
// carrying an MWAA ARN, and the /clitoken and /webtoken token paths, so it runs
// before the S3 catch-all and never shadows a sibling service's tag operations.
package mwaa

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/mwaa/driver"
)

// Path roots at the service root.
const (
	rootEnvironments = "environments"
	rootTags         = "tags"
	rootCliToken     = "clitoken"
	rootWebToken     = "webtoken"
)

// arnMarker scopes the shared /tags root to MWAA ARNs.
const arnMarker = ":airflow:"

// Token kinds distinguish the two token operations.
const (
	tokenKindCli = "cli"
	tokenKindWeb = "web"
)

// Handler serves Amazon MWAA requests against a driver.
type Handler struct {
	mw driver.MWAA
}

// New returns an MWAA handler backed by d.
func New(d driver.MWAA) *Handler {
	return &Handler{mw: d}
}

// Matches claims the MWAA path shapes. The /environments tree is unique to
// MWAA. The /tags root is shared with other restJson1 services, so it is
// claimed only for MWAA ARNs; a non-MWAA ARN falls through. The token roots are
// POST-only with exactly one name segment.
func (*Handler) Matches(r *http.Request) bool {
	segs := splitPath(escapedPath(r))
	if len(segs) == 0 {
		return false
	}

	switch segs[0] {
	case rootEnvironments:
		// GET /environments (collection) and any verb on
		// /environments/{Name}; a deeper path is not an MWAA operation and
		// falls through to the S3 catch-all.
		rest := segs[1:]
		switch len(rest) {
		case 0:
			return r.Method == http.MethodGet
		case 1:
			return true
		default:
			return false
		}
	case rootTags:
		return len(segs) >= 2 && strings.Contains(strings.Join(segs[1:], "/"), arnMarker)
	case rootCliToken, rootWebToken:
		return len(segs) == 2 && r.Method == http.MethodPost
	default:
		return false
	}
}

// ServeHTTP dispatches an MWAA request on its path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(escapedPath(r))
	if len(segs) == 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch segs[0] {
	case rootEnvironments:
		h.serveEnvironments(w, r, segs[1:])
	case rootTags:
		h.serveTags(w, r, segs[1:])
	case rootCliToken:
		h.serveToken(w, r, segs[1:], tokenKindCli)
	case rootWebToken:
		h.serveToken(w, r, segs[1:], tokenKindWeb)
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveEnvironments routes /environments (collection) and /environments/{Name}.
func (h *Handler) serveEnvironments(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		if r.Method == http.MethodGet {
			h.listEnvironments(w, r)

			return
		}

		methodNotAllowed(w)

		return
	}

	if len(rest) != 1 {
		notFoundPath(w, r.URL.Path)

		return
	}

	h.serveEnvironmentItem(w, r, rest[0])
}

// serveEnvironmentItem routes the verb-keyed operations on a single environment.
func (h *Handler) serveEnvironmentItem(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPut:
		h.createEnvironment(w, r, name)
	case http.MethodGet:
		h.getEnvironment(w, r, name)
	case http.MethodPatch:
		h.updateEnvironment(w, r, name)
	case http.MethodDelete:
		h.deleteEnvironment(w, r, name)
	default:
		methodNotAllowed(w)
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
// each segment so an ARN or name label is delivered whole to a handler.
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
