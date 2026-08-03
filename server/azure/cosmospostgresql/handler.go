// Package cosmospostgresql implements the Azure Cosmos DB for PostgreSQL ARM
// REST API (Microsoft.DBforPostgreSQL/serverGroupsv2) as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmosforpostgresql/armcosmosforpostgresql
// clients configured with a custom endpoint hit this handler the same way they
// hit management.azure.com.
//
// Create/update RPCs return the resource inline with a terminal
// provisioningState so the SDK's LRO poller completes on the first response;
// the cluster start/stop/restart/promote actions reply 202 + Location and the
// poller reads a terminal status from the operationStatuses URL.
package cosmospostgresql

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
)

// Handler serves Microsoft.DBforPostgreSQL/serverGroupsv2 ARM requests.
type Handler struct {
	db cpgdriver.CosmosPostgreSQL
}

// New returns a Cosmos DB for PostgreSQL handler backed by db.
func New(db cpgdriver.CosmosPostgreSQL) *Handler {
	return &Handler{db: db}
}

// Matches claims ARM Microsoft.DBforPostgreSQL/serverGroupsv2 paths, the
// subscription-scoped checkNameAvailability path, and the
// locations/operationStatuses paths its long-running actions poll.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	if rp.Provider != providerName {
		return false
	}

	switch rp.ResourceType {
	case resourceType, resourceCheckName:
		return true
	case resourceLocations:
		return rp.SubResource == subOperationStatuses
	default:
		return false
	}
}

// ServeHTTP routes the request based on path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	switch rp.ResourceType {
	case resourceLocations:
		h.operationStatus(w, r)
	case resourceCheckName:
		h.checkNameAvailability(w, r)
	case resourceType:
		h.serveServerGroups(w, r, &rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unsupported path: "+r.URL.Path)
	}
}

func (h *Handler) serveServerGroups(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	// Collection: .../serverGroupsv2 (resource-group- or subscription-scoped).
	if rp.ResourceName == "" {
		h.listClusters(w, r, rp)
		return
	}

	// Child paths: .../serverGroupsv2/{name}/{subResource}[/{subName}].
	if rp.SubResource != "" {
		h.serveClusterChild(w, r, rp)
		return
	}

	h.serveCluster(w, r, rp)
}

func (h *Handler) serveCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateCluster(w, r, rp)
	case http.MethodGet:
		h.getCluster(w, r, rp)
	case http.MethodPatch:
		h.updateCluster(w, r, rp)
	case http.MethodDelete:
		h.deleteCluster(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) serveClusterChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if action := h.clusterAction(rp.SubResource); action != nil {
		h.postClusterAction(w, r, rp, action)
		return
	}

	switch rp.SubResource {
	case subFirewallRules:
		h.serveFirewallRules(w, r, rp)
	case subRoles:
		h.serveRoles(w, r, rp)
	case subServers:
		h.serveServers(w, r, rp)
	case subConfigurations:
		h.serveConfigurations(w, r, rp)
	case subCoordinatorCfgs:
		h.serveServerConfig(w, r, rp, true)
	case subNodeCfgs:
		h.serveServerConfig(w, r, rp, false)
	case subPrivateEPs:
		h.servePrivateEndpoints(w, r, rp)
	case subPrivateLinks:
		h.servePrivateLinks(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unsupported sub-resource: "+rp.SubResource)
	}
}

// clusterAction returns the driver action for a POST verb sub-resource, or nil
// if the sub-resource isn't a cluster action.
func (h *Handler) clusterAction(sub string) func(context.Context, string, string) error {
	switch sub {
	case actionRestart:
		return h.db.RestartCluster
	case actionStart:
		return h.db.StartCluster
	case actionStop:
		return h.db.StopCluster
	case actionPromote:
		return h.db.PromoteReadReplica
	default:
		return nil
	}
}

// crudHandlers is the set of method handlers for a cluster child collection.
type crudHandlers struct {
	put  func(http.ResponseWriter, *http.Request, *azurearm.ResourcePath)
	get  func(http.ResponseWriter, *http.Request, *azurearm.ResourcePath)
	del  func(http.ResponseWriter, *http.Request, *azurearm.ResourcePath)
	list func(http.ResponseWriter, *http.Request, *azurearm.ResourcePath)
}

// serveCRUD routes a child collection: GET on the collection lists, and
// PUT/GET/DELETE on a named item dispatch to the item handlers.
func serveCRUD(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, hs crudHandlers) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		hs.list(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		hs.put(w, r, rp)
	case http.MethodGet:
		hs.get(w, r, rp)
	case http.MethodDelete:
		hs.del(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveReadOnly routes a read-only child collection: GET on the collection
// lists, GET on a named item fetches it; any other method is rejected.
func serveReadOnly(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath,
	list, get func(http.ResponseWriter, *http.Request, *azurearm.ResourcePath),
) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	if rp.SubResourceName == "" {
		list(w, r, rp)
		return
	}

	get(w, r, rp)
}

// armListOf builds an ARM list envelope by converting each driver value.
func armListOf[D any, W any](items []D, conv func(*D) W) armList[W] {
	out := armList[W]{Value: make([]W, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, conv(&items[i]))
	}

	return out
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
