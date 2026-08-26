package elasticache

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
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

	nodes, err := parseNodeCount("NumCacheNodes", form.Get("NumCacheNodes"))
	if err != nil {
		writeErr(w, err)
		return
	}

	port, err := parseNodeCount("Port", form.Get("Port"))
	if err != nil {
		writeErr(w, err)
		return
	}

	cfg := cachedriver.CacheConfig{
		Name:            form.Get("CacheClusterId"),
		NodeType:        form.Get("CacheNodeType"),
		Engine:          form.Get("Engine"),
		EngineVersion:   form.Get("EngineVersion"),
		NumCacheNodes:   nodes,
		Port:            port,
		SubnetGroupName: form.Get("CacheSubnetGroupName"),
		Tags:            parseTags(form),
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

// cacheModifier is the AWS-specific ModifyCacheCluster surface. It's not part
// of the portable Cache driver (Azure Cache and GCP Memorystore also implement
// it), so the handler type-asserts for it.
type cacheModifier interface {
	ModifyCache(ctx context.Context, name, nodeType, engine string) (*cachedriver.CacheInfo, error)
}

func (h *Handler) modifyCacheCluster(w http.ResponseWriter, r *http.Request) {
	mod, ok := h.cache.(cacheModifier)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "ModifyCacheCluster not supported"))
		return
	}

	info, err := mod.ModifyCache(r.Context(),
		r.Form.Get("CacheClusterId"), r.Form.Get("CacheNodeType"), r.Form.Get("Engine"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyCacheClusterResponse{
		Xmlns:    Namespace,
		Result:   cacheClusterResult{CacheCluster: toCacheClusterXML(info)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// cacheRebooter is the AWS-specific RebootCacheCluster surface. Like
// ModifyCache it is not part of the portable Cache driver, so the handler
// type-asserts for it.
type cacheRebooter interface {
	RebootCache(ctx context.Context, name string) (*cachedriver.CacheInfo, error)
}

func (h *Handler) rebootCacheCluster(w http.ResponseWriter, r *http.Request) {
	rebooter, ok := h.cache.(cacheRebooter)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "RebootCacheCluster not supported"))
		return
	}

	info, err := rebooter.RebootCache(r.Context(), r.Form.Get("CacheClusterId"))
	if err != nil {
		writeErr(w, err)
		return
	}

	// Real ElastiCache reports the cluster as rebooting until the nodes cycle.
	rebooting := *info
	rebooting.Status = "rebooting cache cluster nodes"

	awsquery.WriteXMLResponse(w, rebootCacheClusterResponse{
		Xmlns:    Namespace,
		Result:   cacheClusterResult{CacheCluster: toCacheClusterXML(&rebooting)},
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
		all, err := h.cache.ListCaches(r.Context(), scope.Scope{})
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
