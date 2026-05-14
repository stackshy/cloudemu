// Package postgresflex implements the Azure Database for PostgreSQL Flexible
// Server (Microsoft.DBforPostgreSQL/flexibleServers) ARM REST API as a
// server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers
// clients configured with a custom endpoint hit this handler the same way
// they hit management.azure.com.
//
// MVP coverage:
//
//	PUT    .../providers/Microsoft.DBforPostgreSQL/flexibleServers/{name}            — Create
//	GET    .../providers/Microsoft.DBforPostgreSQL/flexibleServers/{name}            — Get
//	PATCH  .../providers/Microsoft.DBforPostgreSQL/flexibleServers/{name}            — Update
//	DELETE .../providers/Microsoft.DBforPostgreSQL/flexibleServers/{name}            — Delete
//	GET    .../providers/Microsoft.DBforPostgreSQL/flexibleServers                   — List by RG
//	POST   .../providers/Microsoft.DBforPostgreSQL/flexibleServers/{name}/start      — Start
//	POST   .../providers/Microsoft.DBforPostgreSQL/flexibleServers/{name}/stop       — Stop
//	POST   .../providers/Microsoft.DBforPostgreSQL/flexibleServers/{name}/restart    — Restart
//
// Mutating ops return 200 OK with the resource body inline so the SDK's LRO
// poller terminates on the first response.
package postgresflex

import (
	"net/http"

	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

const (
	providerName            = "Microsoft.DBforPostgreSQL"
	resourceFlexibleServers = "flexibleServers"

	subResourceStart   = "start"
	subResourceStop    = "stop"
	subResourceRestart = "restart"
)

// Handler serves Microsoft.DBforPostgreSQL ARM requests against a
// relationaldb driver.
type Handler struct {
	db rdsdriver.RelationalDB
}

// New returns a Postgres Flex handler backed by db.
func New(db rdsdriver.RelationalDB) *Handler {
	return &Handler{db: db}
}

// Matches returns true for ARM Microsoft.DBforPostgreSQL/flexibleServers paths.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && rp.ResourceType == resourceFlexibleServers
}

// ServeHTTP routes the request based on path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// Lifecycle action: .../flexibleServers/{name}/{start|stop|restart}.
	if rp.SubResource != "" {
		h.serveLifecycleAction(w, r, &rp)
		return
	}

	// Server-scoped or list.
	if rp.ResourceName == "" {
		h.serveServerCollection(w, r, &rp)
		return
	}

	h.serveServer(w, r, &rp)
}

func (h *Handler) serveServer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateServer(w, r, rp)
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

func (h *Handler) serveServerCollection(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	h.listServers(w, r, rp)
}

func (h *Handler) serveLifecycleAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	switch rp.SubResource {
	case subResourceStart:
		h.startServer(w, r, rp)
	case subResourceStop:
		h.stopServer(w, r, rp)
	case subResourceRestart:
		h.restartServer(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unsupported action: "+rp.SubResource)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
