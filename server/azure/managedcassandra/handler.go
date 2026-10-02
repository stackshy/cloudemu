// Package managedcassandra implements the Azure Managed Instance for Apache
// Cassandra ARM REST API (Microsoft.DocumentDB/cassandraClusters) as a
// server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos
// CassandraClusters/CassandraDataCenters clients configured with a custom
// endpoint hit this handler the same way they hit management.azure.com.
//
// Mutating ops return the resource inline with a terminal provisioningState so
// the SDK's LRO poller completes on the first response.
package managedcassandra

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	mcdriver "github.com/stackshy/cloudemu/v2/services/managedcassandra/driver"
)

// Handler serves Microsoft.DocumentDB/cassandraClusters ARM requests.
type Handler struct {
	db mcdriver.ManagedCassandra

	// invokeResults holds InvokeCommand outputs keyed by a synthetic operation
	// id, consumed by the operationStatuses poll the SDK LRO issues.
	invokeResults sync.Map // map[string]string
	opCounter     atomic.Uint64
}

// New returns a Managed Cassandra handler backed by db.
func New(db mcdriver.ManagedCassandra) *Handler {
	return &Handler{db: db}
}

// PurgeResourceGroup deletes every cluster in the resource group, with its
// data centers, backing the resource-group cascade. Clusters record only their
// resource group (single estate).
func (h *Handler) PurgeResourceGroup(ctx context.Context, _, resourceGroup string) error {
	clusters, err := h.db.ListClustersByResourceGroup(ctx, resourceGroup)
	if err != nil {
		return err
	}

	var errs []error

	for i := range clusters {
		if err := h.db.DeleteCluster(ctx, clusters[i].ResourceGroup, clusters[i].Name); err != nil &&
			!cerrors.IsNotFound(err) {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// Matches claims ARM Microsoft.DocumentDB/cassandraClusters paths, plus the
// locations/operationStatuses paths its long-running actions poll.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	if rp.Provider != providerName {
		return false
	}

	return rp.ResourceType == resourceType ||
		(rp.ResourceType == resourceLocations && rp.SubResource == subOperationStatuses)
}

// ServeHTTP routes the request based on path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// LRO status poll: .../locations/{loc}/operationStatuses/{id}.
	if rp.ResourceType == resourceLocations {
		h.operationStatus(w, r)
		return
	}

	// Collection: .../cassandraClusters (resource-group- or subscription-scoped).
	if rp.ResourceName == "" {
		h.listClusters(w, r, &rp)
		return
	}

	// Child paths: .../cassandraClusters/{name}/{subResource}[/{subName}].
	if rp.SubResource != "" {
		h.serveClusterChild(w, r, &rp)
		return
	}

	h.serveCluster(w, r, &rp)
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
	if azurearm.TooDeep(w, r, rp, childMaxDepth) {
		return
	}

	switch rp.SubResource {
	case subResourceDCs:
		h.serveDataCenterRoute(w, r, rp)
	case actionDeallocate:
		h.postClusterAction(w, r, rp, h.db.DeallocateCluster)
	case actionStart:
		h.postClusterAction(w, r, rp, h.db.StartCluster)
	case actionInvokeCommand:
		h.invokeCommand(w, r, rp)
	case actionStatus:
		h.clusterStatus(w, r, rp)
	default:
		azurearm.WriteUnknownType(w, r, rp)
	}
}

func (h *Handler) serveDataCenterRoute(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	// rp.ResourceName is the cluster; rp.SubResourceName is the datacenter name
	// (empty for the collection).
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listDataCenters(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateDataCenter(w, r, rp)
	case http.MethodGet:
		h.getDataCenter(w, r, rp)
	case http.MethodPatch:
		h.updateDataCenter(w, r, rp)
	case http.MethodDelete:
		h.deleteDataCenter(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
