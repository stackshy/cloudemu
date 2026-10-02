package gke

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/providers/gcp/gke"
	"github.com/stackshy/cloudemu/v2/server/gcp/sharedpath"
)

// AlloyDB serves the same /v1/projects/{p}/locations/{l}/clusters paths. When
// both are mounted, GKE keeps every list, operation and node-pool path, and
// hands AlloyDB its unique shapes plus items that only AlloyDB owns.

const clusterIDParam = "clusterId"

// ClusterOwner reports whether another service holds cluster id.
type ClusterOwner interface {
	OwnsCluster(ctx context.Context, project, location, id string) bool
}

// SetAlloyDBOwner wires AlloyDB's ownership view, which turns on the shared
// clusters rules. Nil (the default) keeps GKE claiming every clusters path.
func (h *Handler) SetAlloyDBOwner(o ClusterOwner) { h.alloy = o }

// HasClusterNamed reports whether m holds a cluster named id in any location.
// GKE and AlloyDB both use it, so a create can never fall between the two.
func HasClusterNamed(ctx context.Context, m *gke.Mock, id string) bool {
	if id == "" {
		return false
	}

	all, err := m.ListClusters(ctx, "-")
	if err != nil {
		return false
	}

	for i := range all {
		if all[i].Name == id {
			return true
		}
	}

	return false
}

// matchesSharedCluster decides a clusters path when AlloyDB is mounted too.
func (h *Handler) matchesSharedCluster(r *http.Request, p *gkePath) bool {
	if h.alloy == nil || sharedpath.Is(r, sharedpath.Container) {
		return true
	}

	if p.name == "" {
		return h.claimsSharedCollection(r, p)
	}

	if p.subRes == alloySubInstances || p.subRes == alloySubUsers ||
		(r.Method == http.MethodPatch && p.subRes == "") {
		return false
	}

	ctx := r.Context()

	return HasClusterNamed(ctx, h.gke, p.name) || !h.alloy.OwnsCluster(ctx, p.project, p.location, p.name)
}

// claimsSharedCollection keeps lists and clusterId-less creates, and claims a
// clusterId create only when GKE holds that name, to answer 409. Collection
// verbs (clusters:restore, :createsecondary, ...) are AlloyDB's.
func (h *Handler) claimsSharedCollection(r *http.Request, p *gkePath) bool {
	if p.action != "" {
		return false
	}

	if id := r.URL.Query().Get(clusterIDParam); id != "" && r.Method == http.MethodPost {
		return HasClusterNamed(r.Context(), h.gke, id)
	}

	return true
}

// sharedCreateConflict writes 409 when a create would reuse a cluster id the
// other service holds, and reports whether it did.
func (h *Handler) sharedCreateConflict(w http.ResponseWriter, r *http.Request, p *gkePath, name string) bool {
	if h.alloy == nil {
		return false
	}

	if id := r.URL.Query().Get(clusterIDParam); id != "" && HasClusterNamed(r.Context(), h.gke, id) {
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", "cluster "+id+" already exists")
		return true
	}

	if name != "" && h.alloy.OwnsCluster(r.Context(), p.project, p.location, name) {
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", "cluster "+name+" already exists")
		return true
	}

	return false
}
