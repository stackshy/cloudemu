package serverkit

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/admin"
)

// timeTravelPrefix is the control-plane sub-space for the named-snapshot
// registry. It sits under the existing snapshot surface but never collides with
// the plain "snapshot" endpoint (export/restore), which is matched exactly.
const timeTravelPrefix = "snapshot/"

// serveTimeTravel routes the named-snapshot registry endpoints:
//
//	GET    /_cloudemu/snapshot/                       list
//	POST   /_cloudemu/snapshot/{name}                 save (capture live state)
//	DELETE /_cloudemu/snapshot/{name}                 delete
//	POST   /_cloudemu/snapshot/{name}/rewind          rewind live state to {name}
//	POST   /_cloudemu/snapshot/{from}/fork/{to}       fork {from} into {to}
func (a *App) serveTimeTravel(w http.ResponseWriter, r *http.Request) {
	sub := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, admin.Prefix), timeTravelPrefix)

	if sub == "" {
		a.serveTimeTravelList(w, r)

		return
	}

	seg := strings.Split(sub, "/")

	switch {
	case len(seg) == 1:
		a.serveTimeTravelByName(w, r, seg[0])
	case len(seg) == 2 && seg[1] == "rewind":
		serveTimeTravelAction(w, r, http.MethodPost, "rewind", func() error { return a.timetravel.Rewind(seg[0]) })
	case len(seg) == 3 && seg[1] == "fork":
		serveTimeTravelAction(w, r, http.MethodPost, "forked", func() error { return a.timetravel.Fork(seg[0], seg[2]) })
	default:
		writeNetErr(w, http.StatusNotFound, "unknown snapshot endpoint")
	}
}

func (a *App) serveTimeTravelList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeNetErr(w, http.StatusMethodNotAllowed, "listing snapshots requires GET")

		return
	}

	writeNetJSON(w, map[string]any{"snapshots": a.timetravel.List()})
}

// serveTimeTravelByName handles POST (save) and DELETE for /snapshot/{name}.
func (a *App) serveTimeTravelByName(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPost:
		if err := a.timetravel.Save(name); err != nil {
			writeNetErr(w, statusForError(err), err.Error())

			return
		}

		writeNetJSON(w, map[string]string{"status": "saved", "name": name})
	case http.MethodDelete:
		if err := a.timetravel.Delete(name); err != nil {
			writeNetErr(w, statusForError(err), err.Error())

			return
		}

		writeNetJSON(w, map[string]string{"status": "deleted", "name": name})
	default:
		writeNetErr(w, http.StatusMethodNotAllowed, "snapshot save requires POST, delete requires DELETE")
	}
}

// serveTimeTravelAction runs a single mutating registry op (rewind/fork) guarded
// by the required method, reporting status on success.
func serveTimeTravelAction(w http.ResponseWriter, r *http.Request, method, status string, do func() error) {
	if r.Method != method {
		writeNetErr(w, http.StatusMethodNotAllowed, "this snapshot action requires "+method)

		return
	}

	if err := do(); err != nil {
		writeNetErr(w, statusForError(err), err.Error())

		return
	}

	writeNetJSON(w, map[string]string{"status": status})
}

// statusForError maps a registry error's canonical code to an HTTP status.
func statusForError(err error) int {
	switch {
	case errors.IsNotFound(err):
		return http.StatusNotFound
	case errors.IsAlreadyExists(err):
		return http.StatusConflict
	case errors.IsInvalidArgument(err):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
