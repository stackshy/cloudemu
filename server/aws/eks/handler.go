// Package eks implements the AWS EKS REST/JSON control-plane API as a
// server.Handler. Point the real aws-sdk-go-v2/service/eks client at a
// Server registered with this handler and CreateCluster, CreateNodegroup,
// CreateFargateProfile, CreateAddon, and their friends work against an
// in-memory EKS driver.
//
// Wave 1 covers control-plane resources only; the Kubernetes data plane
// (Pods, Deployments, Services, …) is out of scope and is deferred to
// Wave 2. Until Wave 2 ships, the cluster Endpoint field returns a
// placeholder URL and the CertificateAuthority field returns a stub PEM,
// so kubeconfig generation works syntactically without a real apiserver.
//
// EKS uses REST/JSON (not the AWS query protocol). URL paths follow the
// shape the SDK emits, e.g. POST /clusters, POST
// /clusters/{name}/node-groups, POST /clusters/{name}/addons/{addon}/update.
// The handler's Matches predicate is rooted at /clusters so it does not
// shadow the catch-all S3 handler that may be registered alongside.
package eks

import (
	"encoding/json"
	"net/http"
	"strings"

	eksdriver "github.com/stackshy/cloudemu/providers/aws/eks/driver"
)

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20

	pathPrefix = "/clusters"

	// segNodeGroups, segFargateProfiles, segAddons are the EKS sub-resource
	// path segments. Real SDK kebab-cases them (note "node-groups" with a
	// hyphen; the JSON body field is camelCase "nodegroupName").
	segNodeGroups      = "node-groups"
	segFargateProfiles = "fargate-profiles"
	segAddons          = "addons"
	segUpdates         = "updates"
	segUpdateConfig    = "update-config"
	segUpdateVersion   = "update-version"
	segUpdate          = "update"
)

// Path-segment counts the dispatcher branches on. Naming each one keeps the
// switch inside ServeHTTP free of magic numbers.
const (
	pathSegsCluster            = 1 // /clusters/{name}
	pathSegsClusterSubresource = 2 // /clusters/{name}/{action}
	pathSegsChildResource      = 3 // /clusters/{name}/{kind}/{child}
	pathSegsChildAction        = 4 // /clusters/{name}/{kind}/{child}/{action}
)

// Handler serves AWS EKS REST/JSON requests against an EKS driver.
type Handler struct {
	eks eksdriver.EKS
}

// New returns an EKS handler backed by the supplied driver.
func New(eks eksdriver.EKS) *Handler {
	return &Handler{eks: eks}
}

// Matches claims any request rooted at /clusters or exactly /clusters. The
// predicate is intentionally narrow: it rejects anything outside that path
// so the catch-all S3 handler can serve unrelated REST URLs without
// interference.
func (*Handler) Matches(r *http.Request) bool {
	if r.URL.Path == pathPrefix {
		return true
	}

	return strings.HasPrefix(r.URL.Path, pathPrefix+"/")
}

// ServeHTTP routes EKS requests by URL shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path)

	switch len(parts) {
	case 0:
		// /clusters — collection.
		h.serveClustersCollection(w, r)

	case pathSegsCluster:
		// /clusters/{name} — cluster resource.
		h.serveCluster(w, r, parts[0])

	case pathSegsClusterSubresource:
		// /clusters/{name}/{action} — cluster sub-resource.
		h.serveClusterSubresource(w, r, parts[0], parts[1])

	case pathSegsChildResource:
		// /clusters/{name}/{kind}/{child} — child resource.
		h.serveChildResource(w, r, parts[0], parts[1], parts[2])

	case pathSegsChildAction:
		// /clusters/{name}/{kind}/{child}/{action} — child action.
		h.serveChildAction(w, r, parts[0], parts[1], parts[2], parts[3])

	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported path: "+r.URL.Path)
	}
}

func (h *Handler) serveClustersCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createCluster(w, r)
	case http.MethodGet:
		h.listClusters(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveCluster(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		h.describeCluster(w, r, name)
	case http.MethodDelete:
		h.deleteCluster(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveClusterSubresource(w http.ResponseWriter, r *http.Request, name, action string) {
	switch action {
	case segUpdateConfig:
		if r.Method != http.MethodPost {
			methodNotAllowed(w)

			return
		}

		h.updateClusterConfig(w, r, name)

	case segUpdates:
		// UpdateClusterVersion goes here; the SDK posts to /clusters/{n}/updates.
		if r.Method != http.MethodPost {
			methodNotAllowed(w)

			return
		}

		h.updateClusterVersion(w, r, name)

	case segNodeGroups:
		h.serveNodegroupsCollection(w, r, name)

	case segFargateProfiles:
		h.serveFargateCollection(w, r, name)

	case segAddons:
		h.serveAddonsCollection(w, r, name)

	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException",
			"unknown cluster sub-resource: "+action)
	}
}

func (h *Handler) serveNodegroupsCollection(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPost:
		h.createNodegroup(w, r, name)
	case http.MethodGet:
		h.listNodegroups(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveFargateCollection(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPost:
		h.createFargateProfile(w, r, name)
	case http.MethodGet:
		h.listFargateProfiles(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveAddonsCollection(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPost:
		h.createAddon(w, r, name)
	case http.MethodGet:
		h.listAddons(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveChildResource(w http.ResponseWriter, r *http.Request, clusterName, kind, child string) {
	switch kind {
	case segNodeGroups:
		h.serveNodegroup(w, r, clusterName, child)
	case segFargateProfiles:
		h.serveFargateProfile(w, r, clusterName, child)
	case segAddons:
		h.serveAddon(w, r, clusterName, child)
	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException",
			"unknown child resource: "+kind)
	}
}

func (h *Handler) serveNodegroup(w http.ResponseWriter, r *http.Request, clusterName, ngName string) {
	switch r.Method {
	case http.MethodGet:
		h.describeNodegroup(w, r, clusterName, ngName)
	case http.MethodDelete:
		h.deleteNodegroup(w, r, clusterName, ngName)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveFargateProfile(w http.ResponseWriter, r *http.Request, clusterName, profileName string) {
	switch r.Method {
	case http.MethodGet:
		h.describeFargateProfile(w, r, clusterName, profileName)
	case http.MethodDelete:
		h.deleteFargateProfile(w, r, clusterName, profileName)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveAddon(w http.ResponseWriter, r *http.Request, clusterName, addonName string) {
	switch r.Method {
	case http.MethodGet:
		h.describeAddon(w, r, clusterName, addonName)
	case http.MethodDelete:
		h.deleteAddon(w, r, clusterName, addonName)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveChildAction(w http.ResponseWriter, r *http.Request, clusterName, kind, child, action string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	switch {
	case kind == segNodeGroups && action == segUpdateConfig:
		h.updateNodegroupConfig(w, r, clusterName, child)
	case kind == segNodeGroups && action == segUpdateVersion:
		h.updateNodegroupVersion(w, r, clusterName, child)
	case kind == segAddons && action == segUpdate:
		h.updateAddon(w, r, clusterName, child)
	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException",
			"unknown child action: "+kind+"/"+action)
	}
}

// splitPath strips the /clusters prefix and splits the remainder. The
// returned slice is empty for the bare /clusters URL.
func splitPath(p string) []string {
	rest := strings.TrimPrefix(p, pathPrefix)
	rest = strings.TrimPrefix(rest, "/")

	if rest == "" {
		return nil
	}

	return strings.Split(rest, "/")
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidParameterException", "invalid JSON: "+err.Error())

		return false
	}

	return true
}

// writeJSON encodes v as the JSON response body. Real EKS only ever returns
// 200 on success (errors go through writeError), so the status is fixed.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}
