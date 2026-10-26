// Package codeartifact implements the AWS CodeArtifact API (restJson1) as a
// server.Handler. Point the real aws-sdk-go-v2/service/codeartifact client (or
// the `aws codeartifact` CLI, or the aws_codeartifact_* Terraform resources) at a
// Server registered with this handler and the 48 management operations work
// end-to-end against an in-memory driver: domains, repositories, external
// connections, permissions policies, packages and package versions (with real
// asset bytes), package origin controls and package groups.
//
// CodeArtifact routes by HTTP verb + a fixed path under the /v1/ prefix, with the
// resource identity carried in the QUERY STRING rather than the path, for
// example POST /v1/domain?domain=my-domain (CreateDomain),
// GET /v1/repository?domain=d&repository=r (DescribeRepository),
// POST /v1/repositories (ListRepositories). There is no X-Amz-Target header.
// Matches claims exactly the paths in the route table, and the shared /v1/tag,
// /v1/tags and /v1/untag paths only when their ?resourceArn= query names a
// CodeArtifact (:codeartifact:) ARN, so it runs before the S3 catch-all and never
// shadows a sibling service's tag operations. (CodeArtifact's /v1/tags carries no
// path segment and takes ?resourceArn=, unlike the /v1/tags/{arn} path style of
// MQ, Batch, AppSync and Kafka, so the two never collide.)
//
// The package-manager registry protocols (npm, pip, Maven, ...) are not served:
// GetRepositoryEndpoint reports an AWS-shaped URL that points nowhere in the
// emulator.
package codeartifact

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// apiPrefix is the version prefix every CodeArtifact operation path carries.
const apiPrefix = "/v1/"

// SigningName is the SigV4 credential-scope service name CodeArtifact requests
// are signed with.
const SigningName = "codeartifact"

// arnMarker scopes the shared tag paths to CodeArtifact ARNs.
const arnMarker = ":codeartifact:"

// routeKey identifies one operation by verb and path.
type routeKey struct {
	method string
	path   string
}

// Handler serves AWS CodeArtifact requests against a driver. The optional
// capabilities are discovered once, by type assertion; an operation whose
// capability the driver lacks is not routed (it falls through like any unknown
// path).
type Handler struct {
	ca       driver.CodeArtifact
	access   driver.Access
	adminLs  driver.RepositoryAdministratorLister
	policies driver.Policies
	packages driver.Packages
	versions driver.PackageVersions
	groups   driver.PackageGroups

	routes map[routeKey]http.HandlerFunc
	paths  map[string]bool
}

// New returns a CodeArtifact handler backed by d.
func New(d driver.CodeArtifact) *Handler {
	h := &Handler{ca: d, routes: map[routeKey]http.HandlerFunc{}, paths: map[string]bool{}}

	h.access, _ = d.(driver.Access)
	h.adminLs, _ = d.(driver.RepositoryAdministratorLister)
	h.policies, _ = d.(driver.Policies)
	h.packages, _ = d.(driver.Packages)
	h.versions, _ = d.(driver.PackageVersions)
	h.groups, _ = d.(driver.PackageGroups)

	h.buildRoutes()

	return h
}

// Matches claims the CodeArtifact route table. The shared tag paths are claimed
// only when ?resourceArn= names a CodeArtifact ARN; anything else falls through.
func (h *Handler) Matches(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, apiPrefix) || !h.paths[r.URL.Path] {
		return false
	}

	if isTagPath(r.URL.Path) {
		return strings.Contains(r.URL.Query().Get("resourceArn"), arnMarker)
	}

	return true
}

func isTagPath(path string) bool {
	return path == "/v1/tag" || path == "/v1/tags" || path == "/v1/untag"
}

// ServeHTTP dispatches a CodeArtifact request on its verb and path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if fn, ok := h.routes[routeKey{r.Method, r.URL.Path}]; ok {
		fn(w, r)

		return
	}

	if h.paths[r.URL.Path] {
		methodNotAllowed(w)

		return
	}

	notFoundPath(w, r.URL.Path)
}

// IAMService returns the IAM service prefix of the operations this handler
// serves.
func (*Handler) IAMService() string { return "codeartifact" }
