package cache

import (
	"net/http"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// createOrUpdateCache handles PUT — Redis.BeginCreate — and PATCH — Redis.Update.
// The LRO completes inline: returning 201/200 with the resource body terminates
// the SDK's poller on the first response.
//
// A cache found by name is only treated as "this request's resource" when its
// recorded scope matches the request path's subscription+resourceGroup — Redis
// cache names are globally unique (they get a public DNS hostname), so a name
// that already exists under a DIFFERENT resource group is not this scope's
// cache. PATCH never creates: real Azure's Update requires the cache to
// already exist here, so a missing or wrong-scope name is a 404. PUT keeps
// upsert semantics; a wrong-scope name falls through to CreateCache, whose
// existing AlreadyExists check already answers "name taken" (409 Conflict).
func (h *Handler) createOrUpdateCache(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body redisJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	if rejectClusteringOnNonPremium(w, &body) {
		return
	}

	reqScope := scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup}

	existing, err := h.cache.GetCache(r.Context(), rp.ResourceName)

	switch {
	case err == nil && existing.Scope.Matches(reqScope):
		h.updateExistingCache(w, r, rp, &body)
	case r.Method == http.MethodPatch:
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
			"cache "+rp.ResourceName+" not found in resource group "+rp.ResourceGroup)
	default:
		h.createNewCache(w, r, rp, &body)
	}
}

// updateExistingCache applies body's mutable fields to a cache already
// confirmed to exist in the request's resource-group scope.
func (h *Handler) updateExistingCache(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, body *redisJSON) {
	info, err := h.cache.UpdateCache(r.Context(), cacheConfigFromBody(rp, body))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	resp := toRedisJSON(rp, info)
	attachAccessKeys(&resp, info)
	azurearm.WriteJSON(w, http.StatusOK, resp)
}

// createNewCache provisions a cache under the request's resource-group scope.
// If the name is already taken (globally, by another scope), CreateCache's own
// AlreadyExists check fires and maps to 409 Conflict.
func (h *Handler) createNewCache(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, body *redisJSON) {
	info, err := h.cache.CreateCache(r.Context(), cacheConfigFromBody(rp, body))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	resp := toRedisJSON(rp, info)
	attachAccessKeys(&resp, info)
	azurearm.WriteJSON(w, http.StatusCreated, resp)
}

// cacheConfigFromBody builds the driver config shared by create and update.
func cacheConfigFromBody(rp *azurearm.ResourcePath, body *redisJSON) cachedriver.CacheConfig {
	cfg := cachedriver.CacheConfig{
		Name:     rp.ResourceName,
		Engine:   "redis",
		Location: body.Location,
		NodeType: nodeTypeFromBody(body),
		Tags:     body.Tags,
		Scope:    scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup},
	}
	applySKUAndClustering(&cfg, body)
	applyRedisProperties(&cfg, body)

	return cfg
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

// applyRedisProperties copies the remaining top-level Redis properties
// (redisConfiguration, enableNonSslPort, minimumTlsVersion, publicNetworkAccess,
// redisVersion) from the request onto the driver config so they round-trip
// instead of being dropped. Absent fields stay zero/nil, which the driver's
// UpdateCache treats as "leave unchanged" so a partial PATCH does not wipe them.
func applyRedisProperties(cfg *cachedriver.CacheConfig, body *redisJSON) {
	if body.Properties == nil {
		return
	}

	cfg.RedisConfiguration = body.Properties.RedisConfiguration
	cfg.EnableNonSSLPort = body.Properties.EnableNonSSLPort
	cfg.MinimumTLSVersion = body.Properties.MinimumTLSVersion
	cfg.PublicNetworkAccess = body.Properties.PublicNetworkAccess
	cfg.RedisVersion = body.Properties.RedisVersion
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

// deleteCache handles DELETE — Redis.BeginDelete. Returning 200/204 completes
// the SDK's poller on the first response.
//
// A cache found by name but recorded under a DIFFERENT resource group than the
// request path is, from this scope's perspective, not here: real ARM resource
// IDs are scoped by subscription+resourceGroup, so a DELETE naming the wrong
// group must not touch the resource that actually lives elsewhere — it is a
// no-op success, exactly like deleting a name that never existed.
func (h *Handler) deleteCache(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	info, err := h.cache.GetCache(r.Context(), rp.ResourceName)
	if err != nil {
		writeDeleteStatus(w, err)
		return
	}

	if !info.Scope.Matches(scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup}) {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	writeDeleteStatus(w, h.cache.DeleteCache(r.Context(), rp.ResourceName))
}

// writeDeleteStatus maps a delete result to the ARM idempotent-DELETE
// contract: success is 200, "already gone" (NotFound) is 204 No Content
// rather than an error — the same convention every other Azure handler in
// this codebase follows (acr, aks, containerapps, cosmosaccount, cosmosdb,
// databricks, dns, eventgrid, eventhub, kusto, loganalytics, managedidentity).
func writeDeleteStatus(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusOK)
	case cerrors.IsNotFound(err):
		w.WriteHeader(http.StatusNoContent)
	default:
		azurearm.WriteCErr(w, err)
	}
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
