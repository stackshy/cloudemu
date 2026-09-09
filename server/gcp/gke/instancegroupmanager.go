package gke

import (
	"context"
	"hash/fnv"
	"net/http"
	"strconv"

	gcecompute "github.com/stackshy/cloudemu/v2/providers/gcp/compute"
	"github.com/stackshy/cloudemu/v2/providers/gcp/gke"
)

// InstanceGroupManagerRegistrar is the compute-side capability the GKE handler
// uses to keep a backing managed instance group (MIG) in sync with each node
// pool's node count. The GCE Mock satisfies it; wiring is optional, so a GKE
// server with no compute driver simply emits no instanceGroupUrls.
//
// This closes the cross-service gap that made google_container_node_pool's
// node_count drift 0→N forever: the Terraform google provider derives node_count
// by summing the targetSize of the MIGs a node pool's instanceGroupUrls point
// at (via compute instanceGroupManagers.list). With no MIG behind the pool, that
// sum was always 0.
type InstanceGroupManagerRegistrar interface {
	UpsertInstanceGroupManagerGCP(igm gcecompute.InstanceGroupManager)
	DeleteInstanceGroupManagerGCP(zone, name string) error
}

// SetInstanceGroupManagers wires the compute-side MIG registrar. Called from the
// GCP server factory with the GCE Mock so node-pool lifecycle keeps its backing
// MIG's targetSize current.
func (h *Handler) SetInstanceGroupManagers(reg InstanceGroupManagerRegistrar) { h.migs = reg }

// migName derives the stable backing-MIG name for a node pool. Real GKE names
// it "gke-<cluster>-<pool>-<hash>-grp"; the emulator uses a deterministic hash of
// the pool's identity so the same pool always maps to the same MIG across reads.
func migName(location, cluster, pool string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(location + "/" + cluster + "/" + pool))

	return "gke-" + cluster + "-" + pool + "-" + strconv.FormatUint(uint64(h.Sum32()), 16) + "-grp"
}

// syncNodePoolMIG upserts the node pool's backing MIG with targetSize equal to
// the pool's node count, in the pool's location (its zone). A no-op when no
// compute registrar is wired.
func (h *Handler) syncNodePoolMIG(np *gke.NodePool) {
	if h.migs == nil || np == nil {
		return
	}

	name := migName(np.Location, np.ClusterName, np.Name)
	h.migs.UpsertInstanceGroupManagerGCP(gcecompute.InstanceGroupManager{
		Name:             name,
		Zone:             np.Location,
		TargetSize:       int(np.NodeCount),
		BaseInstanceName: "gke-" + np.ClusterName + "-" + np.Name,
	})
}

// removeNodePoolMIG deletes a node pool's backing MIG. A no-op when no registrar
// is wired.
func (h *Handler) removeNodePoolMIG(location, cluster, pool string) {
	if h.migs == nil {
		return
	}

	_ = h.migs.DeleteInstanceGroupManagerGCP(location, migName(location, cluster, pool))
}

// reconcileClusterMIGs upserts a backing MIG for every node pool in a cluster.
// Used after cluster creation, which materializes the default (and any
// caller-specified) node pools in one call.
func (h *Handler) reconcileClusterMIGs(ctx context.Context, location, cluster string) {
	if h.migs == nil {
		return
	}

	pools, err := h.gke.ListNodePools(ctx, location, cluster)
	if err != nil {
		return
	}

	for i := range pools {
		h.syncNodePoolMIG(&pools[i])
	}
}

// removeClusterMIGs deletes the backing MIGs of every node pool in a cluster.
// Called before the cluster (and its pools) are torn down.
func (h *Handler) removeClusterMIGs(ctx context.Context, location, cluster string) {
	if h.migs == nil {
		return
	}

	pools, err := h.gke.ListNodePools(ctx, location, cluster)
	if err != nil {
		return
	}

	for i := range pools {
		h.removeNodePoolMIG(pools[i].Location, pools[i].ClusterName, pools[i].Name)
	}
}

// instanceGroupUrls returns the node pool's instanceGroupUrls, pointing at its
// backing zonal MIG. Terraform reads node_count off the targetSize of the MIG
// these URLs resolve to. Empty when no compute registrar is wired (so no MIG
// exists to resolve). host is the request scheme+host; the host prefix is
// irrelevant to the provider's regex match (it keys on the
// projects/.../instanceGroupManagers/<name> substring), but a real absolute URL
// is emitted for fidelity.
func (h *Handler) instanceGroupUrls(host, project, location, cluster, pool string) []string {
	if h.migs == nil {
		return nil
	}

	name := migName(location, cluster, pool)

	return []string{
		host + "/compute/v1/projects/" + project + "/zones/" + location + "/instanceGroupManagers/" + name,
	}
}

// igmURLsFunc returns a closure that builds a node pool's instanceGroupUrls,
// binding the request host and project. Passed to toClusterResource so each
// embedded node pool carries its backing MIG URL.
func (h *Handler) igmURLsFunc(host, project string) func(np *gke.NodePool) []string {
	return func(np *gke.NodePool) []string {
		return h.instanceGroupUrls(host, project, np.Location, np.ClusterName, np.Name)
	}
}

// reqHost returns the scheme://host of the incoming request.
func reqHost(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host
}
