package alloydb

import (
	"net/http"

	alloydb "google.golang.org/api/alloydb/v1"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

const defaultBackupType = "ON_DEMAND"

func (h *Handler) serveBackups(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	if p.backupID == "" {
		switch r.Method {
		case http.MethodPost:
			h.createBackup(w, r, p)
		case http.MethodGet:
			h.listBackups(w, r, p)
		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getBackup(w, r, p)
	case http.MethodDelete:
		h.deleteBackup(w, r, p)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
	}
}

func (h *Handler) createBackup(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	var body alloydb.Backup
	if !decodeJSON(w, r, &body) {
		return
	}

	snap, err := h.db.CreateClusterSnapshot(r.Context(), rdsdriver.ClusterSnapshotConfig{
		ID:        r.URL.Query().Get("backupId"),
		ClusterID: lastSegment(body.ClusterName),
	})
	if err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, h.doneOperation(p, "create-backup", toWireBackup(snap, defaultBackupType)))
}

func (h *Handler) listBackups(w http.ResponseWriter, r *http.Request, _ *alloyPath) {
	snaps, err := h.db.DescribeClusterSnapshots(r.Context(), nil, "")
	if err != nil {
		writeCErr(w, err)
		return
	}

	out := &alloydb.ListBackupsResponse{Backups: make([]*alloydb.Backup, 0, len(snaps))}
	for i := range snaps {
		out.Backups = append(out.Backups, toWireBackup(&snaps[i], defaultBackupType))
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) getBackup(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	snaps, err := h.db.DescribeClusterSnapshots(r.Context(), []string{p.backupID}, "")
	if err != nil {
		writeCErr(w, err)
		return
	}

	if len(snaps) == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "backup "+p.backupID+" not found")
		return
	}

	writeJSON(w, http.StatusOK, toWireBackup(&snaps[0], defaultBackupType))
}

func (h *Handler) deleteBackup(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	if err := h.db.DeleteClusterSnapshot(r.Context(), p.backupID); err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, h.doneOperation(p, "delete-backup", nil))
}

func (*Handler) serveOperation(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
		return
	}

	name := "projects/" + p.project + "/locations/" + p.location + "/operations/" + p.operationID
	writeJSON(w, http.StatusOK, &alloydb.Operation{Name: name, Done: true})
}
