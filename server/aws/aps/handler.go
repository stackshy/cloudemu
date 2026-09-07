// Package aps implements the Amazon Managed Service for Prometheus (APS)
// control-plane API (REST-JSON, awsRestjson1) as a server.Handler. Point the
// real aws-sdk-go-v2/service/amp client (or the `aws amp` CLI, or the
// aws_prometheus_workspace / aws_prometheus_rule_group_namespace /
// aws_prometheus_alert_manager_definition Terraform resources) at a Server
// registered with this handler and the workspace, rule-groups-namespace,
// alert-manager-definition, logging and tagging operations work end-to-end
// against an in-memory driver.
//
// APS routes by HTTP verb + path (e.g. POST /workspaces, GET
// /workspaces/{workspaceId}, PUT /workspaces/{workspaceId}/rulegroupsnamespaces/
// {name}); there is no X-Amz-Target header and no version prefix. Matches claims
// the /workspaces tree and the /tags paths carrying an APS (:aps:) ARN, so it
// runs before the S3 catch-all and never shadows a sibling service's tag
// operations.
package aps

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/aps/driver"
)

// Path roots at the service root.
const (
	rootWorkspaces = "workspaces"
	rootTags       = "tags"
)

// Sub-resource segments under a workspace.
const (
	subAlias                = "alias"
	subLogging              = "logging"
	subRuleGroupsNamespaces = "rulegroupsnamespaces"
	subAlertManager         = "alertmanager"
	subDefinition           = "definition"
)

// arnMarker scopes the shared /tags root to APS ARNs.
const arnMarker = ":aps:"

// Handler serves Amazon APS requests against a driver.
type Handler struct {
	aps driver.APS
}

// New returns an APS handler backed by d.
func New(d driver.APS) *Handler {
	return &Handler{aps: d}
}

// Matches claims the APS path shapes. The /workspaces tree is unique to APS
// among the registered handlers. The /tags root is shared with other restJson1
// services, so it is claimed only for APS ARNs; a non-APS ARN falls through.
func (*Handler) Matches(r *http.Request) bool {
	segs := splitPath(escapedPath(r))
	if len(segs) == 0 {
		return false
	}

	switch segs[0] {
	case rootWorkspaces:
		return matchesWorkspace(segs[1:])
	case rootTags:
		return len(segs) >= 2 && strings.Contains(strings.Join(segs[1:], "/"), arnMarker)
	default:
		return false
	}
}

// matchesWorkspace reports whether rest (the segments below /workspaces) is a
// known APS operation shape. A deeper or unknown shape falls through to the S3
// catch-all.
func matchesWorkspace(rest []string) bool {
	switch len(rest) {
	case 0, 1:
		// /workspaces (collection) and /workspaces/{id} (item).
		return true
	case 2: //nolint:mnd // /workspaces/{id}/{sub}.
		switch rest[1] {
		case subAlias, subLogging, subRuleGroupsNamespaces:
			return true
		default:
			return false
		}
	case 3: //nolint:mnd // /workspaces/{id}/{sub}/{leaf}.
		switch {
		case rest[1] == subRuleGroupsNamespaces:
			return true
		case rest[1] == subAlertManager && rest[2] == subDefinition:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

// ServeHTTP dispatches an APS request on its path shape.
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
	case 1:
		h.serveWorkspaceItem(w, r, rest[0])
	case 2: //nolint:mnd // /workspaces/{id}/{sub}.
		h.serveWorkspaceSub(w, r, rest[0], rest[1])
	case 3: //nolint:mnd // /workspaces/{id}/{sub}/{leaf}.
		h.serveWorkspaceSubLeaf(w, r, rest[0], rest[1], rest[2])
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

// serveWorkspaceItem routes the verb-keyed operations on /workspaces/{id}.
func (h *Handler) serveWorkspaceItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.describeWorkspace(w, r, id)
	case http.MethodDelete:
		h.deleteWorkspace(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

// serveWorkspaceSub routes /workspaces/{id}/{sub}.
func (h *Handler) serveWorkspaceSub(w http.ResponseWriter, r *http.Request, id, sub string) {
	switch sub {
	case subAlias:
		h.serveAlias(w, r, id)
	case subLogging:
		h.serveLogging(w, r, id)
	case subRuleGroupsNamespaces:
		h.serveRuleGroupsCollection(w, r, id)
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveWorkspaceSubLeaf routes /workspaces/{id}/{sub}/{leaf}.
func (h *Handler) serveWorkspaceSubLeaf(w http.ResponseWriter, r *http.Request, id, sub, leaf string) {
	switch {
	case sub == subRuleGroupsNamespaces:
		h.serveRuleGroupsItem(w, r, id, leaf)
	case sub == subAlertManager && leaf == subDefinition:
		h.serveAlertManager(w, r, id)
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

// splitPath splits a URL path into its non-empty segments, percent-decoding each
// segment so an ARN or name label is delivered whole to a handler.
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
