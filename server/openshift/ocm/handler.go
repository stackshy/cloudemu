// Package ocm implements the Red Hat OpenShift Cluster Manager (OCM) REST API
// that the `rosa` CLI and OCM SDK drive — the /api/clusters_mgmt/v1 cluster
// surface plus the SSO token endpoint. It is a server.Handler registered on the
// AWS server (ROSA is AWS-hosted); its paths (/api/clusters_mgmt/, /auth/realms/)
// are disjoint from every AWS SDK path, so registration is collision-free.
//
// Coverage:
//
//	POST   /auth/realms/{realm}/protocol/openid-connect/token          — SSO token (rosa login)
//	POST   /api/clusters_mgmt/v1/clusters                              — Create cluster
//	GET    /api/clusters_mgmt/v1/clusters                              — List clusters
//	GET    /api/clusters_mgmt/v1/clusters/{id}                         — Describe cluster
//	DELETE /api/clusters_mgmt/v1/clusters/{id}                         — Delete cluster
//	GET    /api/clusters_mgmt/v1/clusters/{id}/credentials             — Admin kubeconfig
//
// cloudemu is unauthenticated: the token endpoint mints a token for any
// credentials so `rosa login` succeeds, and the cluster ops don't verify it.
package ocm

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/openshift/ocm"
)

const (
	clustersPrefix = "/api/clusters_mgmt/v1/clusters"
	tokenSuffix    = "/protocol/openid-connect/token"
)

// Backend is the OCM surface the handler needs. *ocm.Mock satisfies it.
type Backend interface {
	CreateCluster(ctx context.Context, input ocm.ClusterInput) (*ocm.Cluster, error)
	GetCluster(ctx context.Context, id string) (*ocm.Cluster, error)
	ListClusters(ctx context.Context) []ocm.Cluster
	DeleteCluster(ctx context.Context, id string) error
	Kubeconfig(id string) ([]byte, error)
}

// Handler serves the OCM REST API against a Backend.
type Handler struct {
	be Backend
}

// New returns an OCM handler backed by be.
func New(be Backend) *Handler {
	return &Handler{be: be}
}

// Matches returns true for OCM cluster-management and SSO token paths.
func (*Handler) Matches(r *http.Request) bool {
	p := r.URL.Path

	return strings.HasPrefix(p, clustersPrefix) ||
		(strings.HasPrefix(p, "/auth/realms/") && strings.HasSuffix(p, tokenSuffix))
}

// ServeHTTP routes the request by path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path

	if strings.HasPrefix(p, "/auth/realms/") && strings.HasSuffix(p, tokenSuffix) {
		h.serveToken(w, r)

		return
	}

	// Strip the clusters prefix; what remains selects collection / item / sub.
	rest := strings.Trim(strings.TrimPrefix(p, clustersPrefix), "/")

	if rest == "" {
		h.serveClusterCollection(w, r)

		return
	}

	parts := strings.Split(rest, "/")

	switch {
	case len(parts) == 1:
		h.serveCluster(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "credentials":
		h.serveCredentials(w, r, parts[0])
	default:
		writeOCMError(w, http.StatusNotFound, "404", "OCM sub-resource not found: "+rest)
	}
}

func (h *Handler) serveClusterCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createCluster(w, r)
	case http.MethodGet:
		h.listClusters(w, r)
	default:
		writeOCMError(w, http.StatusMethodNotAllowed, "405", "method not allowed")
	}
}

func (h *Handler) serveCluster(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getCluster(w, r, id)
	case http.MethodDelete:
		h.deleteCluster(w, r, id)
	default:
		writeOCMError(w, http.StatusMethodNotAllowed, "405", "method not allowed")
	}
}

func (h *Handler) serveCredentials(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeOCMError(w, http.StatusMethodNotAllowed, "405", "method not allowed")

		return
	}

	h.getCredentials(w, r, id)
}
