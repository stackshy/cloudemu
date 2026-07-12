// Package cache implements the Azure Cache for Redis (Microsoft.Cache/redis)
// ARM REST API as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis clients
// configured with a custom endpoint hit this handler the same way they hit
// management.azure.com, driving the shared cache driver's cluster control
// plane (CreateOrUpdate/Get/List/Delete).
//
// Matches claims ONLY the Microsoft.Cache ARM provider — a distinct provider
// name from every other Azure handler (compute, network, DBforMySQL, …) — so
// registration order relative to them is unconstrained. It must register before
// the permissive BlobStorage fallback.
//
// Coverage:
//
//	PUT    .../providers/Microsoft.Cache/redis/{name}   — Redis.BeginCreate (LRO, completes inline)
//	GET    .../providers/Microsoft.Cache/redis/{name}   — Redis.Get
//	DELETE .../providers/Microsoft.Cache/redis/{name}   — Redis.BeginDelete (LRO, completes inline)
//	GET    .../providers/Microsoft.Cache/redis          — Redis.ListByResourceGroup
//	GET    .../subscriptions/{sub}/providers/Microsoft.Cache/redis — Redis.ListBySubscription
//
// Only the cluster/instance control plane is mapped — the real Azure Cache SDK
// manages Redis caches, not the Redis data plane. The driver's data-plane
// methods (Set/Get/Incr/…) have no cloud-SDK surface and are out of scope.
package cache

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

const (
	providerName = "Microsoft.Cache"
	typeRedis    = "redis"
)

// Handler serves Microsoft.Cache/redis ARM requests against a cache driver.
type Handler struct {
	cache cachedriver.Cache
}

// New returns an Azure Cache handler backed by c.
func New(c cachedriver.Cache) *Handler {
	return &Handler{cache: c}
}

// Matches claims ARM URLs targeting Microsoft.Cache/redis. The provider name is
// unique among Azure handlers, so registration order is unconstrained;
// registered before the BlobStorage fallback.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && rp.ResourceType == typeRedis
}

// ServeHTTP routes on the parsed path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// Collection list: no cache name (subscription- or RG-scoped list).
	if rp.ResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listCaches(w, r, &rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateCache(w, r, &rp)
	case http.MethodGet:
		h.getCache(w, r, &rp)
	case http.MethodDelete:
		h.deleteCache(w, r, &rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
