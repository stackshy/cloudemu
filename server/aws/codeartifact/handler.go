// Package codeartifact implements the AWS CodeArtifact control-plane API
// (restJson1) as a server.Handler. Point the real
// aws-sdk-go-v2/service/codeartifact client (or the `aws codeartifact` CLI, or
// the aws_codeartifact_domain / aws_codeartifact_repository Terraform resources)
// at a Server registered with this handler and the domain, repository, external
// connection and tagging operations work end-to-end against an in-memory driver.
//
// CodeArtifact routes by HTTP verb + a fixed path under the /v1/ prefix, with the
// resource identity carried in the QUERY STRING rather than the path — for
// example POST /v1/domain?domain=my-domain (CreateDomain),
// GET /v1/repository?domain=d&repository=r (DescribeRepository),
// POST /v1/repositories (ListRepositories). There is no X-Amz-Target header.
// Matches claims the /v1/domain(s) and /v1/repository(ies) trees — which are
// distinctive to CodeArtifact — and the shared /v1/tag, /v1/tags and /v1/untag
// paths only when their ?resourceArn= query names a CodeArtifact (:codeartifact:)
// ARN, so it runs before the S3 catch-all and never shadows a sibling service's
// tag operations. (CodeArtifact's /v1/tags carries no path segment and takes
// ?resourceArn=, unlike the /v1/tags/{arn} path style of MQ, Batch, AppSync and
// Kafka, so the two never collide.)
//
// This is a control-plane-only surface: there is no package data plane (no
// package publish, version resolution or asset storage).
package codeartifact

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// apiPrefix is the version prefix every CodeArtifact operation path carries.
const apiPrefix = "/v1/"

// Path roots below the /v1/ prefix.
const (
	rootDomain       = "domain"
	rootDomains      = "domains"
	rootRepository   = "repository"
	rootRepositories = "repositories"
	rootTag          = "tag"
	rootTags         = "tags"
	rootUntag        = "untag"

	subRepositories       = "repositories"
	subExternalConnection = "external-connection"
)

// arnMarker scopes the shared tag roots to CodeArtifact ARNs.
const arnMarker = ":codeartifact:"

// Handler serves AWS CodeArtifact requests against a driver.
type Handler struct {
	ca driver.CodeArtifact
}

// New returns a CodeArtifact handler backed by d.
func New(d driver.CodeArtifact) *Handler {
	return &Handler{ca: d}
}

// Matches claims the CodeArtifact path shapes. The /v1/domain(s) and
// /v1/repository(ies) roots are unique to CodeArtifact. The /v1/tag, /v1/tags and
// /v1/untag roots are claimed only when their ?resourceArn= query names a
// CodeArtifact ARN; anything else falls through.
func (*Handler) Matches(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, apiPrefix) {
		return false
	}

	segs := splitPath(strings.TrimPrefix(r.URL.Path, apiPrefix))
	if len(segs) == 0 {
		return false
	}

	switch segs[0] {
	case rootDomain, rootDomains, rootRepository, rootRepositories:
		return true
	case rootTag, rootTags, rootUntag:
		return strings.Contains(r.URL.Query().Get("resourceArn"), arnMarker)
	default:
		return false
	}
}

// ServeHTTP dispatches a CodeArtifact request on its path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(strings.TrimPrefix(r.URL.Path, apiPrefix))
	if len(segs) == 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch segs[0] {
	case rootDomain:
		h.serveDomain(w, r, segs[1:])
	case rootDomains:
		h.serveDomains(w, r, segs[1:])
	case rootRepository:
		h.serveRepository(w, r, segs[1:])
	case rootRepositories:
		h.serveRepositories(w, r, segs[1:])
	case rootTag:
		h.serveTag(w, r, segs[1:])
	case rootTags:
		h.serveListTags(w, r, segs[1:])
	case rootUntag:
		h.serveUntag(w, r, segs[1:])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveDomain routes /v1/domain and /v1/domain/repositories.
func (h *Handler) serveDomain(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		h.serveDomainItem(w, r)
	case 1:
		if rest[0] != subRepositories {
			notFoundPath(w, r.URL.Path)

			return
		}

		h.listRepositoriesInDomain(w, r)
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveDomainItem handles the verb-keyed operations on /v1/domain.
func (h *Handler) serveDomainItem(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createDomain(w, r)
	case http.MethodGet:
		h.describeDomain(w, r)
	case http.MethodDelete:
		h.deleteDomain(w, r)
	default:
		methodNotAllowed(w)
	}
}

// serveDomains handles POST /v1/domains (ListDomains).
func (h *Handler) serveDomains(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	h.listDomains(w, r)
}

// serveRepository routes /v1/repository and /v1/repository/external-connection.
func (h *Handler) serveRepository(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		h.serveRepositoryItem(w, r)
	case 1:
		if rest[0] != subExternalConnection {
			notFoundPath(w, r.URL.Path)

			return
		}

		h.serveExternalConnection(w, r)
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveRepositoryItem handles the verb-keyed operations on /v1/repository.
func (h *Handler) serveRepositoryItem(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createRepository(w, r)
	case http.MethodGet:
		h.describeRepository(w, r)
	case http.MethodPut:
		h.updateRepository(w, r)
	case http.MethodDelete:
		h.deleteRepository(w, r)
	default:
		methodNotAllowed(w)
	}
}

// serveExternalConnection handles the external-connection subresource.
func (h *Handler) serveExternalConnection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.associateExternalConnection(w, r)
	case http.MethodDelete:
		h.disassociateExternalConnection(w, r)
	default:
		methodNotAllowed(w)
	}
}

// serveRepositories handles POST /v1/repositories (ListRepositories).
func (h *Handler) serveRepositories(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	h.listRepositories(w, r)
}

// splitPath splits a URL path into its non-empty segments.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}

	return strings.Split(p, "/")
}
