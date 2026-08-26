package bigtable

import (
	"net/http"

	bt "google.golang.org/api/bigtableadmin/v2"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

func clusterConfig(name string, c *bt.Cluster) btdriver.CreateClusterConfig {
	return btdriver.CreateClusterConfig{
		Name: name, Location: c.Location, ServeNodes: int(c.ServeNodes),
		DefaultStorageType: c.DefaultStorageType, Autoscaling: fromWireAutoscaling(c),
	}
}

func (h *Handler) serveClusters(w http.ResponseWriter, r *http.Request, rt *route) {
	if !rt.isResource {
		switch r.Method {
		case http.MethodPost:
			h.createCluster(w, r, rt)
		case http.MethodGet:
			h.listClusters(w, r, rt)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if rt.verb == "getMemoryLayer" {
		if err := h.db.GetClusterMemoryLayer(r.Context(), rt.name); err != nil {
			gcprest.WriteCErr(w, err)
			return
		}

		gcprest.WriteJSON(w, http.StatusOK, struct{}{})

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getCluster(w, r, rt)
	case http.MethodPut, http.MethodPatch:
		h.updateCluster(w, r, rt)
	case http.MethodDelete:
		h.deleteCluster(w, r, rt)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createCluster(w http.ResponseWriter, r *http.Request, rt *route) {
	var in bt.Cluster
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	name := rt.parent + "/clusters/" + r.URL.Query().Get("clusterId")

	c, op, err := h.db.CreateCluster(r.Context(), clusterConfig(name, &in))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOp(op, toWireCluster(c)))
}

func (h *Handler) getCluster(w http.ResponseWriter, r *http.Request, rt *route) {
	c, err := h.db.GetCluster(r.Context(), rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toWireCluster(c))
}

func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request, rt *route) {
	clusters, err := h.db.ListClusters(r.Context(), rt.parent)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, next, ok := paginate(w, r, clusters)
	if !ok {
		return
	}

	out := &bt.ListClustersResponse{NextPageToken: next}
	for i := range page {
		out.Clusters = append(out.Clusters, toWireCluster(&page[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) updateCluster(w http.ResponseWriter, r *http.Request, rt *route) {
	var in bt.Cluster
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	serveNodes := int(in.ServeNodes)
	autoscaling := fromWireAutoscaling(&in)

	// With a field mask, apply only the masked scaling fields: an unmasked
	// serveNodes/autoscaling is dropped so the driver preserves it instead of
	// switching the cluster's scaling mode as a side effect.
	if mask := parseFieldMask(r.URL.Query().Get("updateMask")); mask != nil {
		if !mask.has("serveNodes") {
			serveNodes = 0
		}

		if !mask.contains("autoscaling") {
			autoscaling = nil
		}
	}

	c, op, err := h.db.UpdateCluster(r.Context(), rt.name, serveNodes, autoscaling)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOp(op, toWireCluster(c)))
}

func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request, rt *route) {
	if err := h.db.DeleteCluster(r.Context(), rt.name); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}
