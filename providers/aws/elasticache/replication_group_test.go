package elasticache

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/cache/driver"
)

// TestMemberClustersBounded proves memberClusters never sizes its allocation
// beyond maxReplicationGroupNodes, regardless of how large or negative the
// caller-supplied node count is.
func TestMemberClustersBounded(t *testing.T) {
	tests := []struct {
		name  string
		nodes int
		want  int
	}{
		{name: "typical", nodes: 3, want: 3},
		{name: "negative clamps to zero", nodes: -1, want: 0},
		{name: "over ceiling clamps to max", nodes: maxReplicationGroupNodes + 1000000, want: maxReplicationGroupNodes},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			members := memberClusters("rg", tt.nodes)
			if len(members) != tt.want {
				t.Fatalf("memberClusters(%d) len = %d, want %d", tt.nodes, len(members), tt.want)
			}
		})
	}
}

// TestCreateReplicationGroupPathologicalNodeCount proves a pathological
// NumCacheNodes on CreateReplicationGroup can't drive an unbounded
// MemberClusters allocation; the request still succeeds, just clamped.
func TestCreateReplicationGroupPathologicalNodeCount(t *testing.T) {
	m := New(config.NewOptions())

	rg, err := m.CreateReplicationGroup(context.Background(), driver.ReplicationGroupConfig{
		ID: "rg-huge", Engine: "redis", NumCacheNodes: maxReplicationGroupNodes + 1000000,
	})
	if err != nil {
		t.Fatalf("CreateReplicationGroup: %v", err)
	}

	if len(rg.MemberClusters) != maxReplicationGroupNodes {
		t.Fatalf("MemberClusters len = %d, want %d", len(rg.MemberClusters), maxReplicationGroupNodes)
	}
}
