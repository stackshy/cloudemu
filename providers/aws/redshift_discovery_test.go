package aws

import (
	"context"
	"testing"

	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// A Redshift cluster created through the mock must surface in the cross-service
// inventory under the redshift service (not rds).
func TestResourceDiscoverySurfacesRedshift(t *testing.T) {
	ctx := context.Background()
	p := New()

	if _, err := p.Redshift.CreateCluster(ctx, rdbdriver.ClusterConfig{
		ID: "analytics", Engine: "redshift",
		Tags: map[string]string{"env": "prod"},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	res, err := p.ResourceDiscovery.List(ctx, resourcediscovery.Query{
		Services: []string{resourcediscovery.ServiceRedshift},
	})
	if err != nil {
		t.Fatalf("discovery List: %v", err)
	}

	var found bool
	for _, r := range res {
		if r.Service == resourcediscovery.ServiceRedshift && r.ID == "analytics" {
			found = true
			if r.Type != resourcediscovery.TypeCluster {
				t.Errorf("redshift cluster type = %q, want %q", r.Type, resourcediscovery.TypeCluster)
			}
		}
	}

	if !found {
		t.Fatalf("expected Redshift cluster in discovery output, got %d resources", len(res))
	}
}
