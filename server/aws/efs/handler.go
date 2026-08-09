// Package efs implements the AWS EFS (Elastic File System) control-plane API
// (REST-JSON, awsRestjson1) as a server.Handler. Point the real
// aws-sdk-go-v2/service/efs client (or the `aws efs` CLI) at a Server
// registered with this handler and the operations work end-to-end against an
// in-memory driver.
//
// EFS uses path + HTTP-method routing under a fixed API-version prefix
// (/2015-02-01/...). Matches claims only that prefix, so it never shadows the
// S3 catch-all; ServeHTTP dispatches on the path shape below the prefix.
package efs

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

// apiPrefix is the EFS API-version path prefix every operation lives under.
const apiPrefix = "/2015-02-01/"

// Path roots below the version prefix.
const (
	rootFileSystems  = "file-systems"
	rootMountTargets = "mount-targets"
	rootAccessPoints = "access-points"
	rootAccountPrefs = "account-preferences"
	rootResourceTag  = "resource-tags"
	rootCreateTags   = "create-tags"
	rootDeleteTags   = "delete-tags"
	rootTags         = "tags"
)

// Handler serves EFS requests against a driver.
type Handler struct {
	efs driver.EFS
}

// New returns an EFS handler backed by d.
func New(d driver.EFS) *Handler {
	return &Handler{efs: d}
}

// Matches claims requests under the EFS API-version prefix whose first segment
// is a known EFS collection root. The version prefix makes this unambiguous
// with the S3 catch-all (no real bucket path begins with /2015-02-01/).
func (*Handler) Matches(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, apiPrefix) {
		return false
	}

	segs := splitPath(strings.TrimPrefix(r.URL.Path, apiPrefix))
	if len(segs) == 0 {
		return false
	}

	switch segs[0] {
	case rootFileSystems, rootMountTargets, rootAccessPoints, rootAccountPrefs,
		rootResourceTag, rootCreateTags, rootDeleteTags, rootTags:
		return true
	default:
		return false
	}
}

// ServeHTTP dispatches an EFS request on its path shape below the version prefix.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(strings.TrimPrefix(r.URL.Path, apiPrefix))
	if len(segs) == 0 {
		notFound(w, r.URL.Path)

		return
	}

	switch segs[0] {
	case rootFileSystems:
		h.serveFileSystems(w, r, segs[1:])
	case rootMountTargets:
		h.serveMountTargets(w, r, segs[1:])
	case rootAccessPoints:
		h.serveAccessPoints(w, r, segs[1:])
	case rootAccountPrefs:
		h.serveAccountPreferences(w, r)
	case rootResourceTag:
		h.serveResourceTags(w, r, segs[1:])
	case rootCreateTags, rootDeleteTags, rootTags:
		h.serveLegacyTags(w, r, segs[0], segs[1:])
	default:
		notFound(w, r.URL.Path)
	}
}

// splitPath splits a URL path into its non-empty segments.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}

	return strings.Split(p, "/")
}
