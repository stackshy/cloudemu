package synapse

import (
	"maps"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

func (h *Handler) serveBigDataPool(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		h.listBigDataPools(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut, http.MethodPatch:
		h.putBigDataPool(w, r, rp)
	case http.MethodGet:
		h.getBigDataPool(w, rp)
	case http.MethodDelete:
		h.deleteBigDataPool(w, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// putBigDataPool serves the Spark-pool create/update LRO with a synchronous body.
func (h *Handler) putBigDataPool(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var req bigDataPoolRequest
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

	pool, existed := ws.BigDataPools[k]
	if !existed {
		pool = &bigDataPoolState{Name: name}
	}

	pool.Location = req.Location
	pool.Tags = maps.Clone(req.Tags)
	pool.Props = req.Properties
	ws.BigDataPools[k] = pool

	resource := toBigDataPoolResponse(ws, pool)
	h.mu.Unlock()

	// The armsynapse BigDataPoolsClient.BeginCreateOrUpdate poller accepts a
	// synchronous 200 (or 202), not 201.
	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) getBigDataPool(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	childGet(h, w, rp, bigDataPoolsOf, writeBigDataPoolNotFound,
		func(ws *workspaceState, p *bigDataPoolState) any { return toBigDataPoolResponse(ws, p) })
}

func (h *Handler) deleteBigDataPool(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	childDelete(h, w, rp, bigDataPoolsOf)
}

func (h *Handler) listBigDataPools(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	childList(h, w, r, rp, bigDataPoolsOf,
		func(ws *workspaceState, p *bigDataPoolState) any { return toBigDataPoolResponse(ws, p) })
}

func bigDataPoolsOf(ws *workspaceState) map[string]*bigDataPoolState { return ws.BigDataPools }

func writeBigDataPoolNotFound(w http.ResponseWriter, name string) {
	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "Big Data pool not found: "+name)
}

func toBigDataPoolResponse(ws *workspaceState, pool *bigDataPoolState) bigDataPoolResponse {
	id := azurearm.BuildResourceID(ws.Subscription, ws.ResourceGroup, providerName, typeWorkspaces, ws.Name) +
		"/" + childBigData + "/" + pool.Name

	return bigDataPoolResponse{
		ID:       id,
		Name:     pool.Name,
		Type:     armTypeBigDataPool,
		Location: pool.Location,
		Tags:     pool.Tags,
		Properties: bigDataPoolRespProps{
			ProvisioningState: provisioningSucceeded,
			NodeCount:         pool.Props.NodeCount,
			NodeSize:          pool.Props.NodeSize,
			NodeSizeFamily:    pool.Props.NodeSizeFamily,
			AutoScale:         pool.Props.AutoScale,
			AutoPause:         pool.Props.AutoPause,
			SparkVersion:      pool.Props.SparkVersion,
		},
	}
}
