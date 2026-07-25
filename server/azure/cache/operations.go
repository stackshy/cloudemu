package cache

import (
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// createOrUpdateCache handles PUT — Redis.BeginCreate. The LRO completes inline:
// returning 201/200 with the resource body terminates the SDK's poller on the
// first response. Create when absent, otherwise apply the request's mutable
// fields (SKU, tags) via UpdateCache — ARM PUT semantics, so the caller's
// changes are never silently discarded.
func (h *Handler) createOrUpdateCache(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body redisJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := cachedriver.CacheConfig{
		Name:     rp.ResourceName,
		Engine:   "redis",
		NodeType: nodeTypeFromBody(&body),
		Tags:     body.Tags,
		Scope:    scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup},
	}

	if _, err := h.cache.GetCache(r.Context(), rp.ResourceName); err == nil {
		info, uerr := h.cache.UpdateCache(r.Context(), cfg)
		if uerr != nil {
			azurearm.WriteCErr(w, uerr)
			return
		}
		azurearm.WriteJSON(w, http.StatusOK, toRedisJSON(rp, info))
		return
	}

	info, err := h.cache.CreateCache(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusCreated, toRedisJSON(rp, info))
}

// nodeTypeFromBody derives the driver's node-type string from the request SKU.
// Azure node types look like "Standard_C1"; when the body carries a SKU we
// reconstruct that shape, otherwise the driver's default applies.
func nodeTypeFromBody(body *redisJSON) string {
	if body.Properties == nil || body.Properties.SKU == nil {
		return ""
	}

	sku := body.Properties.SKU
	if sku.Name == "" {
		return ""
	}

	family := sku.Family
	if family == "" {
		family = "C"
	}

	return sku.Name + "_" + family + strconv.Itoa(sku.Capacity)
}

// getCache handles GET on a single resource — Redis.Get.
func (h *Handler) getCache(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	info, err := h.cache.GetCache(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toRedisJSON(rp, info))
}

// deleteCache handles DELETE — Redis.BeginDelete. Returning 200 with an empty
// body completes the SDK's poller on the first response.
func (h *Handler) deleteCache(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.cache.DeleteCache(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// listCaches handles GET on the collection — Redis.ListByResourceGroup /
// ListBySubscription. The filter carries the path's subscription and, for
// RG-level lists, its resource group; subscription-level lists leave the
// resource group empty so the filter spans the subscription's groups.
func (h *Handler) listCaches(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	infos, err := h.cache.ListCaches(r.Context(),
		scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]redisJSON, 0, len(infos))
	for i := range infos {
		out = append(out, toRedisJSON(rp, &infos[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, redisListResult{Value: out})
}
