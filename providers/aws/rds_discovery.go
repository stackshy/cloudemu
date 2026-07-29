package aws

import (
	"context"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// rdsDiscovery adapts the RDS mock to the resourcediscovery RelationalDatabases
// capability so RDS/Aurora instances, clusters, and snapshots surface in
// Resource Explorer. Kept in the provider package (not services/) to avoid
// inverting the layering — the discovery engine stays free of provider imports.
type rdsDiscovery struct{ m rdsdriver.RelationalDB }

func (a rdsDiscovery) DiscoverDatabases(ctx context.Context) ([]resourcediscovery.DiscoveredDatabase, error) {
	instances, err := a.m.DescribeInstances(ctx, nil)
	if err != nil {
		return nil, err
	}

	clusters, err := a.m.DescribeClusters(ctx, nil)
	if err != nil {
		return nil, err
	}

	snapshots, err := a.m.DescribeSnapshots(ctx, nil, "")
	if err != nil {
		return nil, err
	}

	out := make([]resourcediscovery.DiscoveredDatabase, 0, len(instances)+len(clusters)+len(snapshots))

	for i := range instances {
		in := instances[i]
		out = append(out, resourcediscovery.DiscoveredDatabase{
			Name: in.ID, ARN: in.ARN, Engine: in.Engine,
			Type: resourcediscovery.TypeDBInstance, Tags: in.Tags,
		})
	}

	for i := range clusters {
		c := clusters[i]
		out = append(out, resourcediscovery.DiscoveredDatabase{
			Name: c.ID, ARN: c.ARN, Engine: c.Engine,
			Type: resourcediscovery.TypeDBCluster, Tags: c.Tags,
		})
	}

	for i := range snapshots {
		s := snapshots[i]
		out = append(out, resourcediscovery.DiscoveredDatabase{
			Name: s.ID, ARN: s.ARN, Engine: s.Engine,
			Type: resourcediscovery.TypeDBSnapshot, Tags: s.Tags,
		})
	}

	return out, nil
}
