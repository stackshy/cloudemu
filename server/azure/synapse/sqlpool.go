package synapse

import (
	"maps"
	"net/http"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

func (h *Handler) serveSQLPool(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch {
	case rp.SubResourceName == "":
		h.listSQLPools(w, r, rp)
	case rp.SubResourceAction != "":
		h.sqlPoolAction(w, r, rp)
	default:
		h.sqlPoolCRUD(w, r, rp)
	}
}

func (h *Handler) sqlPoolCRUD(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut, http.MethodPatch:
		h.putSQLPool(w, r, rp)
	case http.MethodGet:
		h.getSQLPool(w, rp)
	case http.MethodDelete:
		h.deleteSQLPool(w, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// putSQLPool serves the SQL-pool create/update LRO with a synchronous body.
func (h *Handler) putSQLPool(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var req sqlPoolRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	h.mu.Lock()

	ws, ok := h.getWorkspace(rp)
	if !ok {
		h.mu.Unlock()
		writeParentNotFound(w, rp.ResourceName)

		return
	}

	name := rp.SubResourceName
	k := strings.ToLower(name)

	pool, existed := ws.SQLPools[k]
	if !existed {
		pool = &sqlPoolState{Name: name, Status: sqlPoolStatusOnline}
	}

	pool.Location = req.Location
	pool.Tags = maps.Clone(req.Tags)
	pool.SKU = req.SKU
	pool.Props = req.Properties
	ws.SQLPools[k] = pool

	resource := toSQLPoolResponse(ws, pool)
	h.mu.Unlock()

	// The armsynapse SQLPoolsClient.BeginCreate poller accepts a synchronous 200
	// (or 202), not 201 — a 201 fails its initial-response status check.
	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) getSQLPool(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	childGet(h, w, rp, sqlPoolsOf, writeSQLPoolNotFound,
		func(ws *workspaceState, p *sqlPoolState) any { return toSQLPoolResponse(ws, p) })
}

func (h *Handler) deleteSQLPool(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	childDelete(h, w, rp, sqlPoolsOf)
}

func sqlPoolsOf(ws *workspaceState) map[string]*sqlPoolState { return ws.SQLPools }

// sqlPoolAction serves pause/resume, toggling the pool's run status. The
// synchronous 200 body makes the armsynapse BeginPause/BeginResume poller
// finalize on its first poll.
func (h *Handler) sqlPoolAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	status, ok := sqlPoolActionStatus(rp.SubResourceAction)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "unsupported SQL pool action")
		return
	}

	h.mu.Lock()

	ws, ok := h.getWorkspace(rp)
	if !ok {
		h.mu.Unlock()
		writeParentNotFound(w, rp.ResourceName)

		return
	}

	pool, ok := ws.SQLPools[strings.ToLower(rp.SubResourceName)]
	if !ok {
		h.mu.Unlock()
		writeSQLPoolNotFound(w, rp.SubResourceName)

		return
	}

	pool.Status = status
	resource := toSQLPoolResponse(ws, pool)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) listSQLPools(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	childList(h, w, r, rp, sqlPoolsOf,
		func(ws *workspaceState, p *sqlPoolState) any { return toSQLPoolResponse(ws, p) })
}

// sqlPoolActionStatus maps a pause/resume verb to the resulting run status.
func sqlPoolActionStatus(action string) (string, bool) {
	switch strings.ToLower(action) {
	case actionPause:
		return sqlPoolStatusPaused, true
	case actionResume:
		return sqlPoolStatusOnline, true
	default:
		return "", false
	}
}

func writeSQLPoolNotFound(w http.ResponseWriter, name string) {
	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "SQL pool not found: "+name)
}

func toSQLPoolResponse(ws *workspaceState, pool *sqlPoolState) sqlPoolResponse {
	id := azurearm.BuildResourceID(ws.Subscription, ws.ResourceGroup, providerName, typeWorkspaces, ws.Name) +
		"/" + childSQLPools + "/" + pool.Name

	return sqlPoolResponse{
		ID:       id,
		Name:     pool.Name,
		Type:     armTypeSQLPool,
		Location: pool.Location,
		Tags:     pool.Tags,
		SKU:      pool.SKU,
		Properties: sqlPoolRespProps{
			ProvisioningState:  provisioningSucceeded,
			Status:             pool.Status,
			Collation:          pool.Props.Collation,
			MaxSizeBytes:       pool.Props.MaxSizeBytes,
			CreateMode:         pool.Props.CreateMode,
			StorageAccountType: pool.Props.StorageAccountType,
		},
	}
}

// sortedNames returns the map keys of a child map in ascending order, so list
// responses are deterministic.
func sortedNames[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}
