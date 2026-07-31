package alloydb

import (
	"net/http"
	"strings"

	alloydb "google.golang.org/api/alloydb/v1"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// lastSegment returns the final path segment of a resource name, or the input
// if it has no '/'. Used to turn a full clusterName into its bare id.
func lastSegment(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}

	return name
}

func (h *Handler) serveClusterCollection(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	switch r.Method {
	case http.MethodPost:
		h.createCluster(w, r, p)
	case http.MethodGet:
		h.listClusters(w, r, p)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
	}
}

func (h *Handler) createCluster(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	adb, ok := h.alloyCap()
	if !ok {
		writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "AlloyDB capability not wired")
		return
	}

	var body alloydb.Cluster
	if !decodeJSON(w, r, &body) {
		return
	}

	cfg := rdsdriver.AlloyDBClusterConfig{
		ID:              r.URL.Query().Get("clusterId"),
		DatabaseVersion: body.DatabaseVersion,
		Network:         body.Network,
		Tags:            body.Labels,
	}

	if body.InitialUser != nil {
		cfg.InitialUser = body.InitialUser.User
		cfg.InitialPassword = body.InitialUser.Password
	}

	if body.ContinuousBackupConfig != nil {
		cfg.ContinuousBackup = body.ContinuousBackupConfig.Enabled
	}

	if body.AutomatedBackupPolicy != nil {
		cfg.AutomatedBackupEnabled = body.AutomatedBackupPolicy.Enabled
	}

	c, err := adb.CreateAlloyDBCluster(r.Context(), cfg)
	if err != nil {
		writeCErr(w, err)
		return
	}

	info, _ := adb.AlloyDBClusterInfo(r.Context(), c.ID)
	writeJSON(w, http.StatusOK, h.doneOperation(p, "create-cluster", h.toWireCluster(c, info)))
}

func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request, _ *alloyPath) {
	adb, ok := h.alloyCap()
	if !ok {
		writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "AlloyDB capability not wired")
		return
	}

	clusters, err := h.db.DescribeClusters(r.Context(), nil)
	if err != nil {
		writeCErr(w, err)
		return
	}

	out := &alloydb.ListClustersResponse{Clusters: make([]*alloydb.Cluster, 0, len(clusters))}

	for i := range clusters {
		info, _ := adb.AlloyDBClusterInfo(r.Context(), clusters[i].ID)
		out.Clusters = append(out.Clusters, h.toWireCluster(&clusters[i], info))
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) serveClusterItem(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	// Custom methods are POST-only; a non-POST (e.g. GET .../{c}:promote) must
	// not trigger the state change, and an unknown verb is a 404.
	if p.clusterAction != "" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
			return
		}

		if p.clusterAction != actionPromote {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "unsupported cluster action: "+p.clusterAction)
			return
		}

		h.promoteCluster(w, r, p)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getCluster(w, r, p)
	case http.MethodPatch:
		h.patchCluster(w, r, p)
	case http.MethodDelete:
		h.deleteCluster(w, r, p)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
	}
}

func (h *Handler) getCluster(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	adb, ok := h.alloyCap()
	if !ok {
		writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "AlloyDB capability not wired")
		return
	}

	clusters, err := h.db.DescribeClusters(r.Context(), []string{p.clusterID})
	if err != nil {
		writeCErr(w, err)
		return
	}

	if len(clusters) == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "cluster "+p.clusterID+" not found")
		return
	}

	info, _ := adb.AlloyDBClusterInfo(r.Context(), p.clusterID)
	writeJSON(w, http.StatusOK, h.toWireCluster(&clusters[0], info))
}

func (h *Handler) patchCluster(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	adb, ok := h.alloyCap()
	if !ok {
		writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "AlloyDB capability not wired")
		return
	}

	var body alloydb.Cluster
	if !decodeJSON(w, r, &body) {
		return
	}

	c, err := h.db.ModifyCluster(r.Context(), p.clusterID, rdsdriver.ModifyInstanceInput{
		EngineVersion: body.DatabaseVersion,
		Tags:          body.Labels,
	})
	if err != nil {
		writeCErr(w, err)
		return
	}

	info, _ := adb.AlloyDBClusterInfo(r.Context(), c.ID)
	writeJSON(w, http.StatusOK, h.doneOperation(p, "update-cluster", h.toWireCluster(c, info)))
}

func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	if err := h.db.DeleteCluster(r.Context(), p.clusterID); err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, h.doneOperation(p, "delete-cluster", nil))
}

func (h *Handler) promoteCluster(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	adb, ok := h.alloyCap()
	if !ok {
		writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "AlloyDB capability not wired")
		return
	}

	c, err := adb.PromoteCluster(r.Context(), p.clusterID)
	if err != nil {
		writeCErr(w, err)
		return
	}

	info, _ := adb.AlloyDBClusterInfo(r.Context(), c.ID)
	writeJSON(w, http.StatusOK, h.doneOperation(p, "promote-cluster", h.toWireCluster(c, info)))
}

func (h *Handler) createSecondaryCluster(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	adb, ok := h.alloyCap()
	if !ok {
		writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "AlloyDB capability not wired")
		return
	}

	var body alloydb.Cluster
	if !decodeJSON(w, r, &body) {
		return
	}

	var primary string
	if body.SecondaryConfig != nil {
		primary = lastSegment(body.SecondaryConfig.PrimaryClusterName)
	}

	c, err := adb.CreateSecondaryCluster(r.Context(), rdsdriver.SecondaryClusterConfig{
		ID:             r.URL.Query().Get("clusterId"),
		PrimaryCluster: primary,
		Tags:           body.Labels,
	})
	if err != nil {
		writeCErr(w, err)
		return
	}

	info, _ := adb.AlloyDBClusterInfo(r.Context(), c.ID)
	writeJSON(w, http.StatusOK, h.doneOperation(p, "create-secondary", h.toWireCluster(c, info)))
}

func (h *Handler) restoreCluster(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	adb, ok := h.alloyCap()
	if !ok {
		writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "AlloyDB capability not wired")
		return
	}

	var body alloydb.RestoreClusterRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	if body.BackupSource == nil || body.ClusterId == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "backupSource and clusterId are required")
		return
	}

	c, err := h.db.RestoreClusterFromSnapshot(r.Context(), rdsdriver.RestoreClusterInput{
		NewClusterID: body.ClusterId,
		SnapshotID:   lastSegment(body.BackupSource.BackupName),
	})
	if err != nil {
		writeCErr(w, err)
		return
	}

	info, _ := adb.AlloyDBClusterInfo(r.Context(), c.ID)
	writeJSON(w, http.StatusOK, h.doneOperation(p, "restore-cluster", h.toWireCluster(c, info)))
}
