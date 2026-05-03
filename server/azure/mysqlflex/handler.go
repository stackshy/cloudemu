// Package mysqlflex implements the Azure Database for MySQL — Flexible Server
// (Microsoft.DBforMySQL/flexibleServers) ARM REST API as a server.Handler.
// Real github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mysql/armmysqlflexibleservers
// clients configured with a custom endpoint hit this handler the same way they
// hit management.azure.com.
//
// MVP coverage:
//
//	PUT    .../providers/Microsoft.DBforMySQL/flexibleServers/{name}          — Create
//	GET    .../providers/Microsoft.DBforMySQL/flexibleServers/{name}          — Get
//	PATCH  .../providers/Microsoft.DBforMySQL/flexibleServers/{name}          — Update
//	DELETE .../providers/Microsoft.DBforMySQL/flexibleServers/{name}          — Delete
//	GET    .../providers/Microsoft.DBforMySQL/flexibleServers                 — List
//	POST   .../providers/Microsoft.DBforMySQL/flexibleServers/{name}/start    — Start
//	POST   .../providers/Microsoft.DBforMySQL/flexibleServers/{name}/stop     — Stop
//	POST   .../providers/Microsoft.DBforMySQL/flexibleServers/{name}/restart  — Restart
//
// Mutating ops return 200 OK with the resource body inline so the SDK's LRO
// poller terminates on the first response.
package mysqlflex

import (
	"net/http"

	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

const (
	providerName        = "Microsoft.DBforMySQL"
	resourceFlexServers = "flexibleServers"

	subStart   = "start"
	subStop    = "stop"
	subRestart = "restart"
)

// Handler serves Microsoft.DBforMySQL/flexibleServers ARM requests against a
// relationaldb driver.
type Handler struct {
	db rdsdriver.RelationalDB
}

// New returns a MySQL Flexible Server handler backed by db.
func New(db rdsdriver.RelationalDB) *Handler {
	return &Handler{db: db}
}

// Matches returns true for ARM Microsoft.DBforMySQL/flexibleServers paths.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && rp.ResourceType == resourceFlexServers
}

// ServeHTTP routes the request based on path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// Action sub-resources: start, stop, restart.
	if rp.SubResource != "" {
		h.serveAction(w, r, &rp)
		return
	}

	// Server-scoped or list.
	if rp.ResourceName == "" {
		h.serveCollection(w, r, &rp)
		return
	}

	h.serveServer(w, r, &rp)
}

func (h *Handler) serveServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createServer(w, r, rp)
	case http.MethodGet:
		h.getServer(w, r, rp)
	case http.MethodPatch:
		h.updateServer(w, r, rp)
	case http.MethodDelete:
		h.deleteServer(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	h.listServers(w, r, rp)
}

func (h *Handler) serveAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	switch rp.SubResource {
	case subStart:
		h.startServer(w, r, rp)
	case subStop:
		h.stopServer(w, r, rp)
	case subRestart:
		h.restartServer(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unsupported action: "+rp.SubResource)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
