package elasticache

import (
	"testing"

	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

// TestToCacheClusterXMLBoundsNodeAllocation proves toCacheClusterXML never
// sizes its per-node allocation beyond maxCacheNodesPerCluster, even if a
// stored CacheInfo somehow carries a pathological NumCacheNodes (the create
// path already rejects that, but this is a defense-in-depth backstop on the
// render path itself).
func TestToCacheClusterXMLBoundsNodeAllocation(t *testing.T) {
	tests := []struct {
		name      string
		numNodes  int
		wantNodes int
	}{
		{name: "typical", numNodes: 3, wantNodes: 3},
		{name: "zero defaults to one", numNodes: 0, wantNodes: 1},
		{name: "over ceiling clamps to max", numNodes: maxCacheNodesPerCluster + 1000000, wantNodes: maxCacheNodesPerCluster},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := toCacheClusterXML(&cachedriver.CacheInfo{
				Name: "c1", Engine: memcachedEngine, Endpoint: "c1.example.com:11211",
				NumCacheNodes: tt.numNodes,
			})

			if out.NumCacheNodes != tt.wantNodes {
				t.Fatalf("NumCacheNodes = %d, want %d", out.NumCacheNodes, tt.wantNodes)
			}

			if out.CacheNodes == nil || len(out.CacheNodes.CacheNode) != tt.wantNodes {
				got := 0
				if out.CacheNodes != nil {
					got = len(out.CacheNodes.CacheNode)
				}

				t.Fatalf("len(CacheNodes) = %d, want %d", got, tt.wantNodes)
			}
		})
	}
}
