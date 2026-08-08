package aws

import (
	"context"
	"testing"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// A DB proxy created through the RDS mock must surface in the cross-service
// inventory via the rdsDiscovery adapter.
func TestResourceDiscoverySurfacesDBProxy(t *testing.T) {
	ctx := context.Background()
	p := New()

	if _, err := p.RDS.CreateDBProxy(ctx, rdsdriver.DBProxyConfig{
		Name: "app-proxy", EngineFamily: "POSTGRESQL",
	}); err != nil {
		t.Fatalf("CreateDBProxy: %v", err)
	}

	res, err := p.ResourceDiscovery.List(ctx, resourcediscovery.Query{
		Services: []string{resourcediscovery.ServiceRelationalDB},
	})
	if err != nil {
		t.Fatalf("discovery List: %v", err)
	}

	var found bool
	for _, r := range res {
		if r.Type == resourcediscovery.TypeDBProxy && r.ID == "app-proxy" {
			found = true
		}
	}

	if !found {
		t.Fatalf("expected DB proxy in discovery output, got %d resources", len(res))
	}
}
