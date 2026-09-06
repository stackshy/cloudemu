// Package appflow implements the AWS AppFlow control-plane API (REST-JSON,
// awsRestjson1) as a server.Handler. Point the real
// aws-sdk-go-v2/service/appflow client (or the `aws appflow` CLI, or the
// aws_appflow_flow / aws_appflow_connector_profile Terraform resources) at a
// Server registered with this handler and the flow and connector-profile
// operations work end-to-end against an in-memory driver.
//
// AppFlow routes by HTTP verb + a fixed per-operation path at the root (e.g.
// POST /create-flow, POST /describe-flow, POST /tags/{resourceArn}); there is no
// X-Amz-Target header and no version prefix. Matches claims only the exact
// AppFlow operation paths and /tags paths carrying an AppFlow ARN, so it never
// shadows the S3 catch-all.
package appflow

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/appflow/driver"
)

// Operation path roots. Every flow/connector operation is a single-segment path
// served by POST.
const (
	opCreateFlow       = "create-flow"
	opDescribeFlow     = "describe-flow"
	opUpdateFlow       = "update-flow"
	opDeleteFlow       = "delete-flow"
	opListFlows        = "list-flows"
	opStartFlow        = "start-flow"
	opStopFlow         = "stop-flow"
	opCreateProfile    = "create-connector-profile"
	opUpdateProfile    = "update-connector-profile"
	opDeleteProfile    = "delete-connector-profile"
	opDescribeProfiles = "describe-connector-profiles"
	rootTags           = "tags"
)

// arnMarker scopes the shared /tags root to AppFlow ARNs.
const arnMarker = ":appflow:"

// Handler serves AWS AppFlow requests against a driver.
type Handler struct {
	af driver.AppFlow
}

// New returns an AppFlow handler backed by d.
func New(d driver.AppFlow) *Handler {
	return &Handler{af: d}
}

// isOperationRoot reports whether seg is one of the fixed single-segment
// AppFlow operation paths.
func isOperationRoot(seg string) bool {
	switch seg {
	case opCreateFlow, opDescribeFlow, opUpdateFlow, opDeleteFlow, opListFlows,
		opStartFlow, opStopFlow, opCreateProfile, opUpdateProfile, opDeleteProfile,
		opDescribeProfiles:
		return true
	default:
		return false
	}
}

// Matches claims the exact AppFlow operation paths and /tags paths whose ARN is
// an AppFlow ARN. Operation paths are POST-only, so a GET/PUT/DELETE on an S3
// bucket that happens to share an operation's name falls through to the S3
// catch-all. The /tags root is shared with other restJson1 services, so it is
// claimed only for AppFlow ARNs; a non-AppFlow ARN falls through.
func (*Handler) Matches(r *http.Request) bool {
	segs := splitPath(escapedPath(r))
	if len(segs) == 0 {
		return false
	}

	if isOperationRoot(segs[0]) {
		return len(segs) == 1 && r.Method == http.MethodPost
	}

	if segs[0] == rootTags {
		return len(segs) >= 2 && strings.Contains(strings.Join(segs[1:], "/"), arnMarker)
	}

	return false
}

// ServeHTTP dispatches an AppFlow request on its operation path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(escapedPath(r))
	if len(segs) == 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	if segs[0] == rootTags {
		h.serveTags(w, r, segs[1:])

		return
	}

	h.serveOperation(w, r, segs[0])
}

// serveOperation dispatches the POST-only flow and connector-profile operations.
//
//nolint:gocyclo // one arm per operation path; the surface is flat by design.
func (h *Handler) serveOperation(w http.ResponseWriter, r *http.Request, op string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	switch op {
	case opCreateFlow:
		h.createFlow(w, r)
	case opDescribeFlow:
		h.describeFlow(w, r)
	case opUpdateFlow:
		h.updateFlow(w, r)
	case opDeleteFlow:
		h.deleteFlow(w, r)
	case opListFlows:
		h.listFlows(w, r)
	case opStartFlow:
		h.startFlow(w, r)
	case opStopFlow:
		h.stopFlow(w, r)
	case opCreateProfile:
		h.createConnectorProfile(w, r)
	case opUpdateProfile:
		h.updateConnectorProfile(w, r)
	case opDeleteProfile:
		h.deleteConnectorProfile(w, r)
	case opDescribeProfiles:
		h.describeConnectorProfiles(w, r)
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
// each segment so an ARN label is delivered whole to a handler.
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
