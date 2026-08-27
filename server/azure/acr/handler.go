// Package acr implements the Azure Container Registry data-plane catalog API
// (/acr/v1/…) as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry clients
// pointed at this server list repositories and tags, read repository
// properties, and delete repositories against the shared containerregistry
// driver.
//
// ACR has no "create repository" data-plane call — repositories appear when an
// image is pushed — so this handler is list/get/delete oriented. ACR uses
// challenge-based auth; the mock serves anonymously, so the SDK never needs to
// exchange a token.
//
// Coverage:
//
//	GET    /acr/v1/_catalog              — list repositories
//	GET    /acr/v1/{name}                — repository properties
//	DELETE /acr/v1/{name}                — delete repository
//	GET    /acr/v1/{name}/_tags          — list tags
package acr

import (
	"encoding/json"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

const pathPrefix = "/acr/v1/"

const (
	catalogSeg   = "_catalog"
	tagsSeg      = "_tags"
	manifestsSeg = "_manifests"
)

// Handler serves the ACR data-plane catalog API against a ContainerRegistry
// driver.
type Handler struct {
	registry crdriver.ContainerRegistry
}

// New returns an ACR handler backed by reg.
func New(reg crdriver.ContainerRegistry) *Handler {
	return &Handler{registry: reg}
}

// Matches claims /acr/v1/ data-plane requests. Disjoint from ARM
// (/subscriptions/…) and registered before the blob storage REST fallback.
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, pathPrefix)
}

// ServeHTTP routes on the path tail and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, pathPrefix)

	if tail == catalogSeg {
		if r.Method == http.MethodGet {
			h.listRepositories(w, r)
			return
		}

		writeErr(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "unsupported ACR operation")

		return
	}

	repo, resource, reference := parseACRPath(tail)

	switch resource {
	case tagsSeg:
		h.serveTags(w, r, repo, reference)
	case manifestsSeg:
		h.serveManifests(w, r, repo, reference)
	default:
		h.serveRepository(w, r, repo)
	}
}

// parseACRPath splits an ACR data-plane tail into the repository name, the
// resource kind ("_tags" / "_manifests" / ""), and the trailing reference. The
// repository name may be hierarchical (e.g. "team/app"), so the split hinges on
// the "_tags" / "_manifests" marker segment rather than the first slash.
func parseACRPath(tail string) (repo, resource, reference string) {
	for _, marker := range []string{tagsSeg, manifestsSeg} {
		if idx := strings.Index(tail, "/"+marker+"/"); idx >= 0 {
			return tail[:idx], marker, tail[idx+len(marker)+2:]
		}

		if strings.HasSuffix(tail, "/"+marker) {
			return strings.TrimSuffix(tail, "/"+marker), marker, ""
		}
	}

	return tail, "", ""
}

func (h *Handler) serveRepository(w http.ResponseWriter, r *http.Request, repo string) {
	switch r.Method {
	case http.MethodGet:
		h.getRepositoryProperties(w, r, repo)
	case http.MethodDelete:
		h.deleteRepository(w, r, repo)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "unsupported ACR operation")
	}
}

func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request, repo, tag string) {
	if tag == "" {
		if r.Method == http.MethodGet {
			h.listTags(w, r, repo)
			return
		}

		writeErr(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "unsupported ACR operation")

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getTagProperties(w, r, repo, tag)
	case http.MethodDelete:
		h.deleteTag(w, r, repo, tag)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "unsupported ACR operation")
	}
}

func (h *Handler) serveManifests(w http.ResponseWriter, r *http.Request, repo, digest string) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "unsupported ACR operation")
		return
	}

	if digest == "" {
		h.listManifests(w, r, repo)
		return
	}

	h.getManifestProperties(w, r, repo, digest)
}

// writeErr emits an ACR-style error body with the given HTTP status.
func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]string{{"code": code, "message": msg}},
	})
}

// writeCErr maps a canonical cloudemu error to an ACR error response.
func writeCErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeErr(w, http.StatusNotFound, "NAME_UNKNOWN", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeErr(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
	case cerrors.IsFailedPrecondition(err):
		writeErr(w, http.StatusConflict, "DENIED", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}
