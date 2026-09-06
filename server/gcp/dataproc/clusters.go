package dataproc

import (
	"net/http"
	"strings"
	"time"

	dp "google.golang.org/api/dataproc/v1"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	dpdriver "github.com/stackshy/cloudemu/v2/services/dataproc/driver"
)

func (h *Handler) createCluster(w http.ResponseWriter, r *http.Request, project, region string) {
	var body dp.Cluster
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	if body.ClusterName == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "clusterName is required")
		return
	}

	cfg := dpdriver.CreateClusterConfig{
		ProjectID:   project,
		Region:      region,
		ClusterName: body.ClusterName,
		Config:      fromWireConfig(body.Config),
		Labels:      body.Labels,
	}

	c, op, err := h.db.CreateCluster(r.Context(), &cfg)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOperation(op.Name, clusterResponseAny(toWireCluster(c))))
}

func (h *Handler) getCluster(w http.ResponseWriter, r *http.Request, project, region, name string) {
	c, err := h.db.GetCluster(r.Context(), project, region, name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toWireCluster(c))
}

func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request, project, region string) {
	clusters, err := h.db.ListClusters(r.Context(), project, region)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	out := &dp.ListClustersResponse{Clusters: make([]*dp.Cluster, 0, len(clusters))}
	for i := range clusters {
		out.Clusters = append(out.Clusters, toWireCluster(&clusters[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) patchCluster(w http.ResponseWriter, r *http.Request, project, region, name string) {
	var body dp.Cluster
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	cfg := dpdriver.UpdateClusterConfig{
		FieldMask: normalizeMask(r.URL.Query().Get("updateMask")),
		Labels:    body.Labels,
	}

	if body.Config != nil {
		if body.Config.WorkerConfig != nil {
			cfg.WorkerNumInstances = body.Config.WorkerConfig.NumInstances
		}

		if body.Config.SecondaryWorkerConfig != nil {
			cfg.SecondaryWorkerNumInstances = body.Config.SecondaryWorkerConfig.NumInstances
		}
	}

	c, op, err := h.db.UpdateCluster(r.Context(), project, region, name, cfg)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOperation(op.Name, clusterResponseAny(toWireCluster(c))))
}

func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request, project, region, name string) {
	op, err := h.db.DeleteCluster(r.Context(), project, region, name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOperation(op.Name, nil))
}

// normalizeMask splits a comma-separated updateMask into trimmed, lowercase
// paths so "config.worker_config.num_instances" (and stray whitespace) match the
// driver's field tokens.
func normalizeMask(mask string) []string {
	if mask == "" {
		return nil
	}

	parts := strings.Split(mask, ",")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		tok := strings.ToLower(strings.TrimSpace(p))
		if tok != "" {
			out = append(out, tok)
		}
	}

	return out
}

// formatTime renders t as RFC3339Nano; a zero time renders as the empty string.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339Nano)
}
