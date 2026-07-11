package elasticache

import (
	"net/http"
	"net/url"
	"strconv"

	cachedriver "github.com/stackshy/cloudemu/cache/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// parseTags parses ElastiCache-style Tags.Tag.N.{Key,Value} entries.
func parseTags(form url.Values) map[string]string {
	indices := awsquery.CollectIndices(form, "Tags.Tag")
	if len(indices) == 0 {
		return nil
	}

	out := make(map[string]string, len(indices))

	for _, n := range indices {
		base := "Tags.Tag." + strconv.Itoa(n)
		if k := form.Get(base + ".Key"); k != "" {
			out[k] = form.Get(base + ".Value")
		}
	}

	return out
}

func (h *Handler) createCacheCluster(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	cfg := cachedriver.CacheConfig{
		Name:     form.Get("CacheClusterId"),
		NodeType: form.Get("CacheNodeType"),
		Engine:   form.Get("Engine"),
		Tags:     parseTags(form),
	}

	info, err := h.cache.CreateCache(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createCacheClusterResponse{
		Xmlns:    Namespace,
		Result:   cacheClusterResult{CacheCluster: toCacheClusterXML(info)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeCacheClusters(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("CacheClusterId")

	// DescribeCacheClusters with a CacheClusterId scopes to that one cluster;
	// without it, list them all.
	var infos []cachedriver.CacheInfo

	if id != "" {
		info, err := h.cache.GetCache(r.Context(), id)
		if err != nil {
			writeErr(w, err)
			return
		}

		infos = []cachedriver.CacheInfo{*info}
	} else {
		all, err := h.cache.ListCaches(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}

		infos = all
	}

	out := cacheClustersXML{CacheCluster: make([]cacheClusterXML, 0, len(infos))}
	for i := range infos {
		out.CacheCluster = append(out.CacheCluster, toCacheClusterXML(&infos[i]))
	}

	awsquery.WriteXMLResponse(w, describeCacheClustersResponse{
		Xmlns:    Namespace,
		Result:   describeCacheClustersResult{CacheClusters: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteCacheCluster(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("CacheClusterId")

	// DeleteCacheCluster echoes the cluster (now in the "deleting" state) back
	// in its response, so read it before removing it.
	info, err := h.cache.GetCache(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}

	if derr := h.cache.DeleteCache(r.Context(), id); derr != nil {
		writeErr(w, derr)
		return
	}

	last := *info
	last.Status = "deleting"

	awsquery.WriteXMLResponse(w, deleteCacheClusterResponse{
		Xmlns:    Namespace,
		Result:   cacheClusterResult{CacheCluster: toCacheClusterXML(&last)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
