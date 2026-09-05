package gke

import (
	"context"
	"strings"
	"testing"
)

// TestListClustersDeterministicOrder proves ListClusters sorts by name rather
// than returning random map-iteration order.
func TestListClustersDeterministicOrder(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	for _, name := range []string{"gamma", "alpha", "beta"} {
		if _, _, err := m.CreateCluster(ctx, &CreateClusterInput{Name: name, Location: "us-central1"}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	got, err := m.ListClusters(ctx, "us-central1")
	requireNoError(t, err)

	names := make([]string, 0, len(got))
	for i := range got {
		names = append(names, got[i].Name)
	}

	assertEqual(t, "alpha,beta,gamma", strings.Join(names, ","))
}

// TestListNodePoolsDeterministicOrder proves ListNodePools returns a stable
// order (default-pool first from cluster create, then by name for pools sharing
// a creation timestamp under the fake clock).
func TestListNodePoolsDeterministicOrder(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, _, err := m.CreateCluster(ctx, &CreateClusterInput{Name: "c", Location: "us-central1"}); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	for _, name := range []string{"zeta", "delta"} {
		spec := NodePoolSpec{Name: name}
		if _, _, err := m.CreateNodePool(ctx, "us-central1", "c", &spec); err != nil {
			t.Fatalf("create pool %s: %v", name, err)
		}
	}

	got, err := m.ListNodePools(ctx, "us-central1", "c")
	requireNoError(t, err)

	names := make([]string, 0, len(got))
	for i := range got {
		names = append(names, got[i].Name)
	}

	// default-pool from cluster create has the earliest timestamp; the two added
	// pools share the fake clock's time and tie-break by name.
	assertEqual(t, "default-pool,delta,zeta", strings.Join(names, ","))
}

// TestOperationTargetLinkStoredProjectRelative proves the operation's stored
// TargetLink omits the project prefix, so the handler injects the request's
// project when rendering the wire targetLink (not the mock's configured
// default project).
func TestOperationTargetLinkStoredProjectRelative(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, op, err := m.CreateCluster(ctx, &CreateClusterInput{Name: "prod", Location: "us-central1"})
	requireNoError(t, err)

	if strings.Contains(op.TargetLink, "projects/") {
		t.Fatalf("stored TargetLink should be project-relative, got %q", op.TargetLink)
	}

	assertEqual(t, "locations/us-central1/clusters/prod", op.TargetLink)
}
