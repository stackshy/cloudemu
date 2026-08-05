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

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

const (
	providerName             = "Microsoft.Sql"
	resourceServers          = "servers"
	resourceManagedInstances = "managedInstances"
	subResourceDatabases     = "databases"

	subFirewallRules  = "firewallRules"
	subVNetRules      = "virtualNetworkRules"
	subElasticPools   = "elasticPools"
	subFailoverGroups = "failoverGroups"
	subAdministrators = "administrators"

	subMIStart    = "start"
	subMIStop     = "stop"
	subMIFailover = "failover"

	actionFailover      = "failover"
	actionForceFailover = "forceFailoverAllowDataLoss"
)

// Handler serves Microsoft.Sql ARM requests against a relationaldb driver.
type Handler struct {
	db rdsdriver.RelationalDB
}

// New returns an Azure SQL handler backed by db.
func New(db rdsdriver.RelationalDB) *Handler {
	return &Handler{db: db}
}

// Matches returns true for ARM Microsoft.Sql server and managed-instance paths.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	if rp.Provider != providerName {
		return false
	}

	return rp.ResourceType == resourceServers || rp.ResourceType == resourceManagedInstances
}

// ServeHTTP routes the request based on path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if rp.ResourceType == resourceManagedInstances {
		h.serveManagedInstanceRoute(w, r, &rp)
		return
	}

	// Child resources: .../servers/{srv}/{type}[/{name}].
	if rp.SubResource != "" {
		h.serveServerChild(w, r, &rp)
		return
	}

	// Server-scoped or list.
	if rp.ResourceName == "" {
		h.serveServerCollection(w, r, &rp)
		return
	}

	h.serveServer(w, r, &rp)
}

// serveServerChild dispatches a .../servers/{srv}/{type}[/{name}] path to the
// matching child-resource handler.
func (h *Handler) serveServerChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch rp.SubResource {
	case subResourceDatabases:
		h.serveDatabaseRoute(w, r, rp)
	case subFirewallRules:
		h.serveFirewallRule(w, r, rp)
	case subVNetRules:
		h.serveVNetRule(w, r, rp)
	case subElasticPools:
		h.serveElasticPool(w, r, rp)
	case subFailoverGroups:
		h.serveFailoverGroup(w, r, rp)
	case subAdministrators:
		h.serveAADAdmin(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unsupported sub-resource: "+rp.SubResource)
	}
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
	db, ok := h.databases()
	if !ok {
		writeUnsupported(w, "databases")
		return
	}

	// rp.ResourceName is the server name; rp.SubResourceName is the database
	// name (or empty for the collection).
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listDatabases(w, r, rp, db)

		return
	}

	switch r.Method {
	case http.MethodPut, http.MethodPatch:
		h.putDatabase(w, r, rp, db)
	case http.MethodGet:
		h.getDatabase(w, r, rp, db)
	case http.MethodDelete:
		h.deleteDatabase(w, r, rp, db)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
