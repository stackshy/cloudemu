package bigtable

import (
	"net/http"

	bt "google.golang.org/api/bigtableadmin/v2"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

func (h *Handler) serveTables(w http.ResponseWriter, r *http.Request, rt *route) {
	if !rt.isResource {
		switch {
		case r.Method == http.MethodPost && rt.verb == "restore":
			h.restoreTable(w, r, rt)
		case r.Method == http.MethodPost:
			h.createTable(w, r, rt)
		case r.Method == http.MethodGet:
			h.listTables(w, r, rt)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if rt.verb != "" {
		h.tableVerb(w, r, rt)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getTable(w, r, rt)
	case http.MethodPatch:
		h.patchTable(w, r, rt)
	case http.MethodDelete:
		h.deleteTable(w, r, rt)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) tableVerb(w http.ResponseWriter, r *http.Request, rt *route) {
	switch rt.verb {
	case "modifyColumnFamilies":
		h.modifyColumnFamilies(w, r, rt)
	case "dropRowRange":
		h.dropRowRange(w, r, rt)
	case "generateConsistencyToken":
		h.generateConsistencyToken(w, r, rt)
	case "checkConsistency":
		h.checkConsistency(w, r, rt)
	case "undelete":
		h.undeleteTable(w, r, rt)
	default:
		if !h.serveIamVerb(w, r, rt.name, rt.verb) {
			gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported verb: "+rt.verb)
		}
	}
}

func (h *Handler) createTable(w http.ResponseWriter, r *http.Request, rt *route) {
	var in bt.CreateTableRequest
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	cfg := btdriver.CreateTableConfig{Parent: rt.parent, TableID: in.TableId}
	if in.Table != nil {
		cfg.ColumnFamilies = fromWireColumnFamilies(in.Table.ColumnFamilies)
		cfg.Granularity = in.Table.Granularity
		cfg.DeletionProtection = in.Table.DeletionProtection
	}

	t, err := h.db.CreateTable(r.Context(), cfg)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toWireTable(t))
}

func (h *Handler) getTable(w http.ResponseWriter, r *http.Request, rt *route) {
	t, err := h.db.GetTable(r.Context(), rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toWireTable(t))
}

func (h *Handler) listTables(w http.ResponseWriter, r *http.Request, rt *route) {
	tables, err := h.db.ListTables(r.Context(), rt.parent)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, next, ok := paginate(w, r, tables)
	if !ok {
		return
	}

	out := &bt.ListTablesResponse{NextPageToken: next}
	for i := range page {
		out.Tables = append(out.Tables, toWireTable(&page[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) patchTable(w http.ResponseWriter, r *http.Request, rt *route) {
	var in bt.Table
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	// Without a mask, always write deletionProtection from the body (legacy
	// behavior). With a mask, write it only when masked — so a patch of some
	// other field (e.g. changeStreamConfig) leaves deletionProtection intact
	// instead of silently resetting it to false.
	dp := in.DeletionProtection
	dpPtr := &dp

	if mask := parseFieldMask(r.URL.Query().Get("updateMask")); mask != nil && !mask.has("deletionProtection") {
		dpPtr = nil
	}

	t, op, err := h.db.UpdateTable(r.Context(), rt.name, dpPtr)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOp(op, toWireTable(t)))
}

func (h *Handler) deleteTable(w http.ResponseWriter, r *http.Request, rt *route) {
	if err := h.db.DeleteTable(r.Context(), rt.name); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}

func (h *Handler) undeleteTable(w http.ResponseWriter, r *http.Request, rt *route) {
	t, op, err := h.db.UndeleteTable(r.Context(), rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOp(op, toWireTable(t)))
}

func (h *Handler) modifyColumnFamilies(w http.ResponseWriter, r *http.Request, rt *route) {
	var in bt.ModifyColumnFamiliesRequest
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	mods := make([]btdriver.ColumnFamilyModification, 0, len(in.Modifications))

	for _, m := range in.Modifications {
		mod := btdriver.ColumnFamilyModification{ID: m.Id, Drop: m.Drop}
		if m.Create != nil {
			mod.Create = &btdriver.ColumnFamily{GCRule: fromWireGCRule(m.Create.GcRule)}
		}

		if m.Update != nil {
			mod.Update = &btdriver.ColumnFamily{GCRule: fromWireGCRule(m.Update.GcRule)}
		}

		mods = append(mods, mod)
	}

	t, err := h.db.ModifyColumnFamilies(r.Context(), rt.name, mods)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toWireTable(t))
}

func (h *Handler) dropRowRange(w http.ResponseWriter, r *http.Request, rt *route) {
	var in bt.DropRowRangeRequest
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	if err := h.db.DropRowRange(r.Context(), rt.name); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}

func (h *Handler) generateConsistencyToken(w http.ResponseWriter, r *http.Request, rt *route) {
	token, err := h.db.GenerateConsistencyToken(r.Context(), rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, &bt.GenerateConsistencyTokenResponse{ConsistencyToken: token})
}

func (h *Handler) checkConsistency(w http.ResponseWriter, r *http.Request, rt *route) {
	var in bt.CheckConsistencyRequest
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	ok, err := h.db.CheckConsistency(r.Context(), rt.name, in.ConsistencyToken)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, &bt.CheckConsistencyResponse{Consistent: ok})
}

func (h *Handler) restoreTable(w http.ResponseWriter, r *http.Request, rt *route) {
	var in bt.RestoreTableRequest
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	t, op, err := h.db.RestoreTable(r.Context(), rt.parent, in.TableId, in.Backup)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOp(op, toWireTable(t)))
}
