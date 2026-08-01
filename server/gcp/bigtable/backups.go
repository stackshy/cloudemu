package bigtable

import (
	"net/http"
	"time"

	bt "google.golang.org/api/bigtableadmin/v2"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

//nolint:gocyclo // a flat verb/method switch over the backup operations is clearest.
func (h *Handler) serveBackups(w http.ResponseWriter, r *http.Request, rt *route) {
	if !rt.isResource {
		switch {
		case r.Method == http.MethodPost && rt.verb == "copy":
			h.copyBackup(w, r, rt)
		case r.Method == http.MethodPost:
			h.createBackup(w, r, rt)
		case r.Method == http.MethodGet:
			h.listBackups(w, r, rt)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if rt.verb != "" {
		if !h.serveIamVerb(w, r, rt.name, rt.verb) {
			gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported verb: "+rt.verb)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getBackup(w, r, rt)
	case http.MethodPatch:
		h.patchBackup(w, r, rt)
	case http.MethodDelete:
		h.deleteBackup(w, r, rt)
	default:
		methodNotAllowed(w)
	}
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)

	return t
}

func (h *Handler) createBackup(w http.ResponseWriter, r *http.Request, rt *route) {
	var in bt.Backup
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	b, op, err := h.db.CreateBackup(r.Context(), btdriver.CreateBackupConfig{
		Parent: rt.parent, BackupID: r.URL.Query().Get("backupId"),
		SourceTable: in.SourceTable, ExpireTime: parseTime(in.ExpireTime), BackupType: in.BackupType,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOp(op, toWireBackup(b)))
}

func (h *Handler) copyBackup(w http.ResponseWriter, r *http.Request, rt *route) {
	var in bt.CopyBackupRequest
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	b, op, err := h.db.CopyBackup(r.Context(), btdriver.CopyBackupConfig{
		Parent: rt.parent, BackupID: in.BackupId, SourceBackup: in.SourceBackup, ExpireTime: parseTime(in.ExpireTime),
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOp(op, toWireBackup(b)))
}

func (h *Handler) getBackup(w http.ResponseWriter, r *http.Request, rt *route) {
	b, err := h.db.GetBackup(r.Context(), rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toWireBackup(b))
}

func (h *Handler) listBackups(w http.ResponseWriter, r *http.Request, rt *route) {
	backups, err := h.db.ListBackups(r.Context(), rt.parent)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	out := &bt.ListBackupsResponse{}
	for i := range backups {
		out.Backups = append(out.Backups, toWireBackup(&backups[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) patchBackup(w http.ResponseWriter, r *http.Request, rt *route) {
	var in bt.Backup
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	b, err := h.db.UpdateBackup(r.Context(), rt.name, parseTime(in.ExpireTime))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toWireBackup(b))
}

func (h *Handler) deleteBackup(w http.ResponseWriter, r *http.Request, rt *route) {
	if err := h.db.DeleteBackup(r.Context(), rt.name); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}
