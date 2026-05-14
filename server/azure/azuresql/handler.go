// Package azuresql implements the Azure SQL Database (Microsoft.Sql) ARM
// REST API as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql clients
// configured with a custom endpoint hit this handler the same way they hit
// management.azure.com.
//
// MVP coverage:
//
//	PUT    .../providers/Microsoft.Sql/servers/{s}                      — Create or update server
//	GET    .../providers/Microsoft.Sql/servers/{s}                      — Get server
//	DELETE .../providers/Microsoft.Sql/servers/{s}                      — Delete server (cascade-deletes databases)
//	GET    .../providers/Microsoft.Sql/servers                          — List servers in RG
//	PUT    .../providers/Microsoft.Sql/servers/{s}/databases/{d}        — Create or update database
//	PATCH  .../providers/Microsoft.Sql/servers/{s}/databases/{d}        — Update database
//	GET    .../providers/Microsoft.Sql/servers/{s}/databases/{d}        — Get database
//	DELETE .../providers/Microsoft.Sql/servers/{s}/databases/{d}        — Delete database
//	GET    .../providers/Microsoft.Sql/servers/{s}/databases            — List databases on a server
//
// Mutating ops return 200 OK with the resource body inline so the SDK's LRO
// poller terminates on the first response.
package azuresql

import (
	"net/http"

	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

const (
	providerName         = "Microsoft.Sql"
	resourceServers      = "servers"
	subResourceDatabases = "databases"
)

// Handler serves Microsoft.Sql ARM requests against a relationaldb driver.
type Handler struct {
	db rdsdriver.RelationalDB
}

// New returns an Azure SQL handler backed by db.
func New(db rdsdriver.RelationalDB) *Handler {
	return &Handler{db: db}
}

// Matches returns true for ARM Microsoft.Sql server/database paths.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && rp.ResourceType == resourceServers
}

// ServeHTTP routes the request based on path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// Database-scoped: .../servers/{srv}/databases[/{db}]
	if rp.SubResource == subResourceDatabases {
		h.serveDatabaseRoute(w, r, &rp)
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

func (h *Handler) serveDatabaseRoute(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	// rp.ResourceName is the server name; rp.SubResourceName is the database
	// name (or empty for the collection).
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listDatabases(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateDatabase(w, r, rp)
	case http.MethodPatch:
		h.updateDatabase(w, r, rp)
	case http.MethodGet:
		h.getDatabase(w, r, rp)
	case http.MethodDelete:
		h.deleteDatabase(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
