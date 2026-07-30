package alloydb

import (
	"net/http"

	alloydb "google.golang.org/api/alloydb/v1"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func (h *Handler) serveUsers(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	users, ok := h.usersCap()
	if !ok {
		writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "AlloyDB users not wired")
		return
	}

	if p.subID == "" {
		switch r.Method {
		case http.MethodPost:
			h.createUser(w, r, p, users)
		case http.MethodGet:
			h.listUsers(w, r, p, users)
		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getUser(w, r, p, users)
	case http.MethodDelete:
		h.deleteUser(w, r, p, users)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
	}
}

func (*Handler) createUser(w http.ResponseWriter, r *http.Request, p *alloyPath, users rdsdriver.Users) {
	var body alloydb.User
	if !decodeJSON(w, r, &body) {
		return
	}

	u, err := users.CreateUser(r.Context(), rdsdriver.UserConfig{
		Instance: p.clusterID,
		Name:     r.URL.Query().Get("userId"),
		Password: body.Password,
	})
	if err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toWireUser(u))
}

func (*Handler) listUsers(w http.ResponseWriter, r *http.Request, p *alloyPath, users rdsdriver.Users) {
	list, err := users.ListUsers(r.Context(), p.clusterID)
	if err != nil {
		writeCErr(w, err)
		return
	}

	out := &alloydb.ListUsersResponse{Users: make([]*alloydb.User, 0, len(list))}
	for i := range list {
		out.Users = append(out.Users, toWireUser(&list[i]))
	}

	writeJSON(w, http.StatusOK, out)
}

func (*Handler) getUser(w http.ResponseWriter, r *http.Request, p *alloyPath, users rdsdriver.Users) {
	u, err := users.GetUser(r.Context(), p.clusterID, p.subID)
	if err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toWireUser(u))
}

func (*Handler) deleteUser(w http.ResponseWriter, r *http.Request, p *alloyPath, users rdsdriver.Users) {
	if err := users.DeleteUser(r.Context(), p.clusterID, p.subID); err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, struct{}{})
}
