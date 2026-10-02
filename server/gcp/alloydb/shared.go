package alloydb

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/gcp/sharedpath"
)

// GKE serves the same /v1/projects/{p}/locations/{l}/clusters paths. When both
// are mounted, AlloyDB claims a hinted request, its unique shapes, and items it
// owns that GKE does not. Unhinted lists stay with GKE.

const (
	clusterIDParam     = "clusterId"
	actionCloudSQL     = "restoreFromCloudSQL"
	actionUpgradeClstr = "upgrade"
)

// GKENamer reports whether GKE holds a cluster named id in any location.
type GKENamer interface {
	HasClusterNamed(ctx context.Context, id string) bool
}

// SetSharedPath turns on the shared clusters rules against GKE's view. Nil
// (the default) keeps AlloyDB claiming every clusters and backups path.
func (h *Handler) SetSharedPath(gke GKENamer) { h.gke = gke }

// matchesShared decides a clusters or backups path when GKE is mounted too.
func (h *Handler) matchesShared(r *http.Request, collection string) bool {
	if h.gke == nil || sharedpath.Is(r, sharedpath.AlloyDB) {
		return true
	}

	if collection == collectionBackups {
		return true
	}

	p, ok := parsePath(r.URL.Path)
	if !ok {
		return false
	}

	if p.clusterID == "" {
		return h.claimsSharedCollection(r, &p)
	}

	if p.sub == subInstances || p.sub == subUsers || isClusterPatch(r, &p) {
		return true
	}

	ctx := r.Context()

	return h.ownsCluster(ctx, p.clusterID) && !h.gke.HasClusterNamed(ctx, p.clusterID)
}

// claimsSharedCollection claims the collection verbs GKE lacks and a create
// whose clusterId GKE does not hold. Lists stay with GKE.
func (h *Handler) claimsSharedCollection(r *http.Request, p *alloyPath) bool {
	switch p.collAction {
	case actionCreateSecondary, actionRestore, actionCloudSQL:
		return true
	case "":
		id := r.URL.Query().Get(clusterIDParam)

		return r.Method == http.MethodPost && id != "" && !h.gke.HasClusterNamed(r.Context(), id)
	default:
		return false
	}
}

// isClusterPatch reports a PATCH of a cluster or its :upgrade. GKE updates
// clusters with PUT, so a PATCH is AlloyDB's.
func isClusterPatch(r *http.Request, p *alloyPath) bool {
	return p.sub == "" && r.Method == http.MethodPatch &&
		(p.clusterAction == "" || p.clusterAction == actionUpgradeClstr)
}

func (h *Handler) ownsCluster(ctx context.Context, id string) bool {
	found, err := h.db.DescribeClusters(ctx, []string{id})

	return err == nil && len(found) > 0
}
