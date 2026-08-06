package bigtable

import (
	"net/http"

	bt "google.golang.org/api/bigtableadmin/v2"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

func (h *Handler) serveAppProfiles(w http.ResponseWriter, r *http.Request, rt *route) {
	if !rt.isResource {
		switch r.Method {
		case http.MethodPost:
			h.createAppProfile(w, r, rt)
		case http.MethodGet:
			h.listAppProfiles(w, r, rt)
		default:
			methodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getAppProfile(w, r, rt)
	case http.MethodPatch:
		h.updateAppProfile(w, r, rt)
	case http.MethodDelete:
		h.deleteAppProfile(w, r, rt)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createAppProfile(w http.ResponseWriter, r *http.Request, rt *route) {
	var in bt.AppProfile
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	cfg := fromWireAppProfile(rt.parent, r.URL.Query().Get("appProfileId"), &in)

	a, err := h.db.CreateAppProfile(r.Context(), cfg)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toWireAppProfile(a))
}

func (h *Handler) getAppProfile(w http.ResponseWriter, r *http.Request, rt *route) {
	a, err := h.db.GetAppProfile(r.Context(), rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toWireAppProfile(a))
}

func (h *Handler) listAppProfiles(w http.ResponseWriter, r *http.Request, rt *route) {
	profiles, err := h.db.ListAppProfiles(r.Context(), rt.parent)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	out := &bt.ListAppProfilesResponse{}
	for i := range profiles {
		out.AppProfiles = append(out.AppProfiles, toWireAppProfile(&profiles[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) updateAppProfile(w http.ResponseWriter, r *http.Request, rt *route) {
	var in bt.AppProfile
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	cfg := fromWireAppProfile(rt.parent, lastSegment(rt.name), &in)

	a, op, err := h.db.UpdateAppProfile(r.Context(), rt.name, cfg)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOp(op, toWireAppProfile(a)))
}

func (h *Handler) deleteAppProfile(w http.ResponseWriter, r *http.Request, rt *route) {
	if err := h.db.DeleteAppProfile(r.Context(), rt.name); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}
