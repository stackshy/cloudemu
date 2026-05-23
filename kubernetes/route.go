package kubernetes

import (
	"strings"
)

// namespacesSegment is the URL segment that introduces a namespaced
// resource in Kubernetes REST paths — e.g. /api/v1/namespaces/{ns}/pods.
const namespacesSegment = "namespaces"

// apiVersionV1 is the only API version Wave 2 Phase 1 supports — both for
// core (/api/v1) and apps (/apis/apps/v1).
const apiVersionV1 = "v1"

// watchQuery is the ?watch=true value clients pass to upgrade a list
// request into a stream. Centralized so dispatchers don't all hold a
// "true" literal.
const watchQueryValue = "true"

// Route holds the four pieces every Kubernetes REST URL decomposes into.
// One Route value is the dispatch key for the per-resource handlers.
type Route struct {
	APIGroup   string // "" for core (/api/v1), "apps" for /apis/apps/v1, etc.
	APIVersion string // always "v1" for Wave 2 Phase 1
	Namespace  string // "" for cluster-scoped resources
	Resource   string // "namespaces", "configmaps", "pods", …
	Name       string // "" for collection requests (LIST / CREATE)
}

// parseRoute splits a Kubernetes REST path into its semantic pieces. The
// path is what r.URL.Path looks like after the /k8s/<uid> prefix has been
// stripped by APIServer.ServeHTTP. Returns nil if the path doesn't match a
// known Kubernetes URL shape.
//
// Supported shapes:
//
//	/api/v1/{resource}                          cluster-scoped collection
//	/api/v1/{resource}/{name}                   cluster-scoped item
//	/api/v1/namespaces/{ns}/{resource}          namespaced collection
//	/api/v1/namespaces/{ns}/{resource}/{name}   namespaced item
//	/apis/{group}/{version}/...                 same shapes under a group
func parseRoute(path string) *Route {
	parts := splitPath(path)

	switch {
	case len(parts) >= 2 && parts[0] == "api":
		return parseCoreRoute(parts[1:])
	case len(parts) >= 3 && parts[0] == "apis":
		return parseGroupRoute(parts[1:])
	default:
		return nil
	}
}

// parseCoreRoute handles /api/v1/... after "api" has been stripped.
//
// parts[0] is the version, the rest is the resource path.
func parseCoreRoute(parts []string) *Route {
	if len(parts) < pathSegsCoreCollection {
		return nil
	}

	r := &Route{APIGroup: "", APIVersion: parts[0]}

	return fillResourceRoute(r, parts[1:])
}

// parseGroupRoute handles /apis/{group}/{version}/... after "apis" has been
// stripped.
func parseGroupRoute(parts []string) *Route {
	if len(parts) < pathSegsGroupCollection {
		return nil
	}

	r := &Route{APIGroup: parts[0], APIVersion: parts[1]}

	return fillResourceRoute(r, parts[2:])
}

// fillResourceRoute fills in the Namespace/Resource/Name fields from the
// path tail.
func fillResourceRoute(r *Route, tail []string) *Route {
	switch len(tail) {
	case pathSegsResourceOnly:
		r.Resource = tail[0]

		return r
	case pathSegsResourceAndName:
		r.Resource = tail[0]
		r.Name = tail[1]

		return r
	case pathSegsNamespacedCollection:
		// namespaces/{ns}/{resource}
		if tail[0] != namespacesSegment {
			return nil
		}

		r.Namespace = tail[1]
		r.Resource = tail[2]

		return r
	case pathSegsNamespacedItem:
		if tail[0] != namespacesSegment {
			return nil
		}

		r.Namespace = tail[1]
		r.Resource = tail[2]
		r.Name = tail[3]

		return r
	default:
		return nil
	}
}

const (
	pathSegsCoreCollection       = 2 // version + resource
	pathSegsGroupCollection      = 3 // group + version + resource
	pathSegsResourceOnly         = 1 // resource
	pathSegsResourceAndName      = 2 // resource + name
	pathSegsNamespacedCollection = 3 // "namespaces" + ns + resource
	pathSegsNamespacedItem       = 4 // "namespaces" + ns + resource + name
)

// splitPath returns the non-empty path segments of p.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}

	return strings.Split(p, "/")
}
