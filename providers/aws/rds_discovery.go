package aws

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// redshiftClusters is the slice of the Redshift mock that discovery reads.
type redshiftClusters interface {
	DescribeClusters(ctx context.Context, ids []string) ([]rdsdriver.Cluster, error)
}

// rdsDiscovery adapts the RDS mock (plus Redshift) to the resourcediscovery
// RelationalDatabases capability so RDS/Aurora instances, clusters, snapshots,
// and proxies — and Redshift clusters — surface in Resource Explorer. Kept in
// the provider package (not services/) to avoid inverting the layering — the
// discovery engine stays free of provider imports.
type rdsDiscovery struct {
	m        rdsdriver.RelationalDB
	redshift redshiftClusters
}

// AddTagsToResource lets the Resource Groups Tagging API apply tags to an RDS
// resource by its ARN, delegating to the RDS mock's optional Tagging capability.
func (a rdsDiscovery) AddTagsToResource(ctx context.Context, arn string, tags map[string]string) error {
	tg, ok := a.m.(rdsdriver.Tagging)
	if !ok {
		return cerrors.New(cerrors.FailedPrecondition, "relational database driver does not support tagging")
	}

	return tg.AddTagsToResource(ctx, arn, tags)
}

// RemoveTagsFromResource removes tags from an RDS resource by its ARN.
func (a rdsDiscovery) RemoveTagsFromResource(ctx context.Context, arn string, keys []string) error {
	tg, ok := a.m.(rdsdriver.Tagging)
	if !ok {
		return cerrors.New(cerrors.FailedPrecondition, "relational database driver does not support tagging")
	}

	return tg.RemoveTagsFromResource(ctx, arn, keys)
}

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

	var proxies []rdsdriver.DBProxy
	if pd, ok := a.m.(rdsdriver.DBProxies); ok {
		proxies, err = pd.DescribeDBProxies(ctx, nil)
		if err != nil {
			return nil, err
		}
	}

	out := make([]resourcediscovery.DiscoveredDatabase, 0,
		len(instances)+len(clusters)+len(snapshots)+len(proxies))

	for i := range instances {
		in := instances[i]
		out = append(out, resourcediscovery.DiscoveredDatabase{
			Name: in.ID, ARN: in.ARN,
			Type: resourcediscovery.TypeDBInstance, Tags: in.Tags,
		})
	}

	for i := range clusters {
		c := clusters[i]
		out = append(out, resourcediscovery.DiscoveredDatabase{
			Name: c.ID, ARN: c.ARN,
			Type: resourcediscovery.TypeDBCluster, Tags: c.Tags,
		})
	}

	for i := range snapshots {
		s := snapshots[i]
		out = append(out, resourcediscovery.DiscoveredDatabase{
			Name: s.ID, ARN: s.ARN,
			Type: resourcediscovery.TypeDBSnapshot, Tags: s.Tags,
		})
	}

	for i := range proxies {
		pr := proxies[i]
		out = append(out, resourcediscovery.DiscoveredDatabase{
			Name: pr.Name, ARN: pr.ARN,
			Type: resourcediscovery.TypeDBProxy,
		})
	}

	if a.redshift != nil {
		clusters, err := a.redshift.DescribeClusters(ctx, nil)
		if err != nil {
			return nil, err
		}

		for i := range clusters {
			c := clusters[i]
			out = append(out, resourcediscovery.DiscoveredDatabase{
				Name: c.ID, ARN: c.ARN,
				Type:    resourcediscovery.TypeCluster,
				Service: resourcediscovery.ServiceRedshift,
				Tags:    c.Tags,
			})
		}
	}

	return out, nil
}
