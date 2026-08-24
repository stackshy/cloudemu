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

	if rejectClusteringOnNonPremium(w, &body) {
		return
	}

	cfg := cachedriver.CacheConfig{
		Name:     rp.ResourceName,
		Engine:   "redis",
		Location: body.Location,
		NodeType: nodeTypeFromBody(&body),
		Tags:     body.Tags,
		Scope:    scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup},
	}
	applySKUAndClustering(&cfg, &body)

	if _, err := h.cache.GetCache(r.Context(), rp.ResourceName); err == nil {
		info, uerr := h.cache.UpdateCache(r.Context(), cfg)
		if uerr != nil {
			azurearm.WriteCErr(w, uerr)
			return
		}

		resp := toRedisJSON(rp, info)
		attachAccessKeys(&resp, info)
		azurearm.WriteJSON(w, http.StatusOK, resp)

		return
	}

	info, err := h.cache.CreateCache(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	resp := toRedisJSON(rp, info)
	attachAccessKeys(&resp, info)
	azurearm.WriteJSON(w, http.StatusCreated, resp)
}

// attachAccessKeys adds the cache's access keys to a Create/Update response.
// Real Azure returns properties.accessKeys only on create/update (never on
// Get/List), so Get and List keep using the bare toRedisJSON.
func attachAccessKeys(out *redisJSON, info *cachedriver.CacheInfo) {
	if info.PrimaryKey == "" && info.SecondaryKey == "" {
		return
	}

	out.Properties.AccessKeys = &accessKeysJSON{
		PrimaryKey:   info.PrimaryKey,
		SecondaryKey: info.SecondaryKey,
	}
}

// skuNamePremium is the Redis SKU tier that supports clustering (shardCount /
// replicasPerPrimary). Real Azure rejects those fields on Basic/Standard.
const skuNamePremium = "Premium"

// rejectClusteringOnNonPremium writes a 400 and returns true when the body sets
// shardCount or replicasPerPrimary on a non-Premium SKU — clustering and
// replicas are Premium-only features, and real Azure rejects them otherwise.
func rejectClusteringOnNonPremium(w http.ResponseWriter, body *redisJSON) bool {
	if body.Properties == nil {
		return false
	}

	clustered := body.Properties.ShardCount > 0 ||
		body.Properties.ReplicasPerPrimary > 0 || body.Properties.ReplicasPerMaster > 0
	if !clustered {
		return false
	}

	if body.Properties.SKU != nil && body.Properties.SKU.Name == skuNamePremium {
		return false
	}

	azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameterValue",
		"shardCount/replicasPerPrimary require a Premium SKU")

	return true
}

// applySKUAndClustering copies the request's SKU family/capacity and the
// Premium clustering fields (shardCount, replicasPerPrimary — replicasPerMaster
// is accepted as the legacy alias) onto the driver config, so a clustered
// Premium cache round-trips instead of collapsing to a stub SKU.
func applySKUAndClustering(cfg *cachedriver.CacheConfig, body *redisJSON) {
	if body.Properties == nil {
		return
	}

	if sku := body.Properties.SKU; sku != nil {
		cfg.SKUFamily = sku.Family
		cfg.SKUCapacity = sku.Capacity
	}

	cfg.ShardCount = body.Properties.ShardCount

	cfg.ReplicasPerPrimary = body.Properties.ReplicasPerPrimary
	if cfg.ReplicasPerPrimary == 0 {
		cfg.ReplicasPerPrimary = body.Properties.ReplicasPerMaster
	}
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

// getCache handles GET on a single resource — Redis.Get. The driver keys caches
// by name alone, so the handler enforces the request's resource-group scope: a
// cache created in one group must not resolve under a different group in the URL
// (real ARM answers 404, since the id would contradict the request path).
func (h *Handler) getCache(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	info, err := h.cache.GetCache(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if !info.Scope.Matches(scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup}) {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
			"cache "+rp.ResourceName+" not found in resource group "+rp.ResourceGroup)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toRedisJSON(rp, info))
}

// listKeys handles POST .../redis/{name}/listKeys — Redis.ListKeys. Returns the
// cache's primary and secondary access keys.
func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	keys, ok := h.cache.(cachedriver.AccessKeys)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidAction", "access keys are not supported by this backend")
		return
	}

	if !h.cacheInScope(w, r, rp) {
		return
	}

	primary, secondary, err := keys.ListCacheKeys(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, accessKeysJSON{PrimaryKey: primary, SecondaryKey: secondary})
}

// regenerateKey handles POST .../redis/{name}/regenerateKey — Redis.RegenerateKey.
// The body selects which key to rotate ("Primary" or "Secondary"); the response
// carries both current keys.
func (h *Handler) regenerateKey(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	keys, ok := h.cache.(cachedriver.AccessKeys)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidAction", "access keys are not supported by this backend")
		return
	}

	var body regenerateKeyRequest
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	if body.KeyType != "Primary" && body.KeyType != "Secondary" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameterValue", "keyType must be Primary or Secondary")
		return
	}

	if !h.cacheInScope(w, r, rp) {
		return
	}

	primary, secondary, err := keys.RegenerateCacheKey(r.Context(), rp.ResourceName, body.KeyType)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, accessKeysJSON{PrimaryKey: primary, SecondaryKey: secondary})
}

// cacheInScope resolves the cache and verifies it lives in the request's
// resource group, writing a 404 (and returning false) when it does not. Used by
// the key sub-actions, which address a cache by name under a scoped path.
func (h *Handler) cacheInScope(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) bool {
	info, err := h.cache.GetCache(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return false
	}

	if !info.Scope.Matches(scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup}) {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
			"cache "+rp.ResourceName+" not found in resource group "+rp.ResourceGroup)
		return false
	}

	return true
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
