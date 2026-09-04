package memorydb

import (
	"context"
	"slices"
	"testing"

	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

func TestAllowedNodeTypeUpdatesFamilyLadder(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		wantUp   []string
		wantDown []string
	}{
		{
			name:     "mid-family offers both directions",
			current:  "db.r6g.xlarge",
			wantUp:   []string{"db.r6g.2xlarge", "db.r6g.4xlarge", "db.r6g.8xlarge", "db.r6g.12xlarge", "db.r6g.16xlarge"},
			wantDown: []string{"db.r6g.large"},
		},
		{
			name:     "smallest node scales up only",
			current:  "db.t4g.small",
			wantUp:   []string{"db.t4g.medium"},
			wantDown: []string{},
		},
		{
			name:     "largest node scales down only",
			current:  "db.r7g.16xlarge",
			wantUp:   []string{},
			wantDown: []string{"db.r7g.large", "db.r7g.xlarge", "db.r7g.2xlarge", "db.r7g.4xlarge", "db.r7g.8xlarge", "db.r7g.12xlarge"},
		},
		{
			name:     "unknown node type yields empty non-nil lists",
			current:  "db.custom.mega",
			wantUp:   []string{},
			wantDown: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			up, down := allowedNodeTypeUpdates(tc.current)

			if up == nil || down == nil {
				t.Fatalf("results must be non-nil: up=%v down=%v", up, down)
			}

			if slices.Contains(up, tc.current) || slices.Contains(down, tc.current) {
				t.Errorf("current type %q must not be offered: up=%v down=%v", tc.current, up, down)
			}

			if !slices.Equal(up, tc.wantUp) {
				t.Errorf("scale-up = %v, want %v", up, tc.wantUp)
			}

			if !slices.Equal(down, tc.wantDown) {
				t.Errorf("scale-down = %v, want %v", down, tc.wantDown)
			}
		})
	}
}

func TestListAllowedNodeTypeUpdatesUsesClusterNodeType(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "ha", NodeType: "db.r6g.large"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	up, down, err := m.ListAllowedNodeTypeUpdates(ctx, "ha")
	requireNoError(t, err)

	if len(down) != 0 {
		t.Errorf("db.r6g.large is the smallest in its family; scale-down = %v, want empty", down)
	}

	if !slices.Contains(up, "db.r6g.xlarge") {
		t.Errorf("scale-up = %v, want it to contain db.r6g.xlarge", up)
	}

	if _, _, err := m.ListAllowedNodeTypeUpdates(ctx, "missing"); err == nil {
		t.Error("expected NotFound for a missing cluster")
	}
}

func TestListAllowedMultiRegionClusterUpdatesUsesNodeType(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	mrc, err := m.CreateMultiRegionCluster(ctx, mdbdriver.CreateMultiRegionClusterConfig{
		NameSuffix: "app", NodeType: "db.r7g.large", NumShards: 1,
	})
	requireNoError(t, err)

	up, err := m.ListAllowedMultiRegionClusterUpdates(ctx, mrc.Name)
	requireNoError(t, err)

	if slices.Contains(up, "db.r7g.large") {
		t.Errorf("scale-up must not offer the current node type: %v", up)
	}

	if !slices.Contains(up, "db.r7g.xlarge") {
		t.Errorf("scale-up = %v, want it to contain db.r7g.xlarge", up)
	}

	if _, err := m.ListAllowedMultiRegionClusterUpdates(ctx, "missing"); err == nil {
		t.Error("expected NotFound for a missing multi-region cluster")
	}
}
