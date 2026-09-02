package kusto

import (
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

func (h *Handler) serveDatabase(w http.ResponseWriter, r *http.Request, kp kustoPath, dbName string) {
	switch r.Method {
	case http.MethodPut:
		h.createDatabase(w, r, kp, dbName)
	case http.MethodPatch:
		h.updateDatabase(w, r, kp, dbName)
	case http.MethodGet:
		h.getDatabase(w, kp, dbName)
	case http.MethodDelete:
		h.deleteDatabase(w, kp, dbName)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// createDatabase serves the database PUT LRO. armkusto's DatabasesClient.
// BeginCreateOrUpdate wraps this in a poller; a synchronous 201/200 carrying
// provisioningState=Succeeded and no async header finalizes it on the first poll.
func (h *Handler) createDatabase(w http.ResponseWriter, r *http.Request, kp kustoPath, dbName string) {
	var req createDatabaseRequest
	if !decodeBody(w, r, &req) {
		return
	}

	now := time.Now().UTC()

	h.mu.Lock()

	c, ok := h.lookupCluster(kp)
	if !ok {
		h.mu.Unlock()
		writeClusterNotFound(w, kp.cluster)

		return
	}

	db, existed := c.Databases[dbKey(dbName)]
	if !existed {
		db = &databaseState{Name: dbName, CreatedAt: now}
	}

	db.Location = req.Location
	if db.Location == "" {
		db.Location = c.Location
	}

	db.Properties = req.Properties
	db.UpdatedAt = now
	c.Databases[dbKey(dbName)] = db

	resource := toDatabaseResource(c, db)
	h.mu.Unlock()

	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}

	azurearm.WriteJSON(w, status, resource)
}

// updateDatabase serves the ARM PATCH (DatabasesClient.Update): the mutable
// retention windows (softDeletePeriod, hotCachePeriod) and location are replaced
// when supplied and preserved otherwise, and the full database is returned. A
// PATCH on a missing cluster or database is a 404.
func (h *Handler) updateDatabase(w http.ResponseWriter, r *http.Request, kp kustoPath, dbName string) {
	var req updateDatabaseRequest
	if !decodeBody(w, r, &req) {
		return
	}

	h.mu.Lock()

	c, ok := h.lookupCluster(kp)
	if !ok {
		h.mu.Unlock()
		writeClusterNotFound(w, kp.cluster)

		return
	}

	db, ok := c.Databases[dbKey(dbName)]
	if !ok {
		h.mu.Unlock()
		writeDatabaseNotFound(w, dbName)

		return
	}

	if req.Location != "" {
		db.Location = req.Location
	}

	db.Properties = mergeDatabaseProps(db.Properties, req.Properties)
	db.UpdatedAt = time.Now().UTC()

	resource := toDatabaseResource(c, db)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

// mergeDatabaseProps overlays the client-mutable database properties of a PATCH
// onto the existing ones. provisioningState is server-computed and recomputed by
// toDatabaseResource, so it is not merged here.
func mergeDatabaseProps(existing, patch databaseProperties) databaseProperties {
	out := existing

	if patch.SoftDeletePeriod != "" {
		out.SoftDeletePeriod = patch.SoftDeletePeriod
	}

	if patch.HotCachePeriod != "" {
		out.HotCachePeriod = patch.HotCachePeriod
	}

	if patch.IsFollowed != nil {
		out.IsFollowed = patch.IsFollowed
	}

	return out
}

func (h *Handler) getDatabase(w http.ResponseWriter, kp kustoPath, dbName string) {
	h.mu.RLock()

	c, ok := h.lookupCluster(kp)
	if !ok {
		h.mu.RUnlock()
		writeClusterNotFound(w, kp.cluster)

		return
	}

	db, ok := c.Databases[dbKey(dbName)]
	if !ok {
		h.mu.RUnlock()
		writeDatabaseNotFound(w, dbName)

		return
	}

	resource := toDatabaseResource(c, db)
	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

// deleteDatabase serves the database DELETE LRO. A synchronous 200/204 with no
// async header finalizes armkusto's BeginDelete poller on the first poll.
func (h *Handler) deleteDatabase(w http.ResponseWriter, kp kustoPath, dbName string) {
	h.mu.Lock()

	c, ok := h.lookupCluster(kp)
	if !ok {
		h.mu.Unlock()
		writeClusterNotFound(w, kp.cluster)

		return
	}

	if _, ok := c.Databases[dbKey(dbName)]; !ok {
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

		return
	}

	delete(c.Databases, dbKey(dbName))
	h.mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listDatabases(w http.ResponseWriter, r *http.Request, kp kustoPath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	h.mu.RLock()

	c, ok := h.lookupCluster(kp)
	if !ok {
		h.mu.RUnlock()
		writeClusterNotFound(w, kp.cluster)

		return
	}

	resources := make([]any, 0, len(c.Databases))
	for _, name := range sortedKeys(c.Databases) {
		resources = append(resources, toDatabaseResource(c, c.Databases[name]))
	}

	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, paginate(r, resources))
}

// dbKey normalizes a database name to its store key; Kusto database names are
// case-insensitive within a cluster.
func dbKey(name string) string { return strings.ToLower(name) }

func writeDatabaseNotFound(w http.ResponseWriter, name string) {
	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "database not found: "+name)
}

func toDatabaseResource(c *clusterState, db *databaseState) databaseResource {
	props := db.Properties
	props.ProvisioningState = provisioningSucceeded

	resourceType := segClusters + "/" + c.Name + "/" + segDatabases

	return databaseResource{
		ID:         azurearm.BuildResourceID(c.Subscription, c.ResourceGroup, providerName, resourceType, db.Name),
		Name:       c.Name + "/" + db.Name,
		Type:       dbResourceType,
		Kind:       kindReadWrite,
		Location:   db.Location,
		Properties: props,
	}
}
