package gcp

import (
	"context"

	gkeprov "github.com/stackshy/cloudemu/v2/providers/gcp/gke"
	alloydbsrv "github.com/stackshy/cloudemu/v2/server/gcp/alloydb"
	gkesrv "github.com/stackshy/cloudemu/v2/server/gcp/gke"
	managedkafkasrv "github.com/stackshy/cloudemu/v2/server/gcp/managedkafka"
	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// GKE, AlloyDB and Managed Kafka all serve
// /v1/projects/{p}/locations/{l}/clusters[/{c}]. These adapters give the
// Managed Kafka handler a read-only view of whichever of GKE / AlloyDB is
// enabled (never both; see New), so it routes that collection by ownership. Each
// answers with the same scoping its own handler serves: GKE clusters are keyed
// by location (the GKE mock is project-agnostic), and AlloyDB's handler lists
// every cluster it holds for any project+location.

var (
	_ managedkafkasrv.ClusterSibling = gkeClusterSibling{}
	_ managedkafkasrv.ClusterSibling = alloyDBClusterSibling{}
	_ managedkafkasrv.ClusterSibling = kafkaSibling{}
	_ gkesrv.ClusterOwner            = alloyDBClusterSibling{}
	_ alloydbsrv.GKENamer            = gkeNamer{}
)

type gkeClusterSibling struct{ m *gkeprov.Mock }

func (s gkeClusterSibling) HasClusters(ctx context.Context, _, location string) bool {
	all, err := s.m.ListClusters(ctx, location)

	return err == nil && len(all) > 0
}

func (s gkeClusterSibling) OwnsCluster(ctx context.Context, _, location, id string) bool {
	_, err := s.m.GetCluster(ctx, location, id)

	return err == nil
}

type alloyDBClusterSibling struct{ db rdbdriver.RelationalDB }

func (s alloyDBClusterSibling) HasClusters(ctx context.Context, _, _ string) bool {
	all, err := s.db.DescribeClusters(ctx, nil)

	return err == nil && len(all) > 0
}

func (s alloyDBClusterSibling) OwnsCluster(ctx context.Context, _, _, id string) bool {
	found, err := s.db.DescribeClusters(ctx, []string{id})

	return err == nil && len(found) > 0
}

// kafkaSibling is Managed Kafka's view when GKE and AlloyDB are both mounted.
// Lists consult GKE only, so they route as before; items see both, so a
// labels-only PATCH of an AlloyDB cluster is not claimed by Kafka.
type kafkaSibling struct {
	gke   gkeClusterSibling
	alloy alloyDBClusterSibling
}

func (s kafkaSibling) HasClusters(ctx context.Context, project, location string) bool {
	return s.gke.HasClusters(ctx, project, location)
}

func (s kafkaSibling) OwnsCluster(ctx context.Context, project, location, id string) bool {
	return s.gke.OwnsCluster(ctx, project, location, id) || s.alloy.OwnsCluster(ctx, project, location, id)
}

// gkeNamer gives AlloyDB the same any-location GKE name check GKE itself uses.
type gkeNamer struct{ m *gkeprov.Mock }

func (n gkeNamer) HasClusterNamed(ctx context.Context, id string) bool {
	return gkesrv.HasClusterNamed(ctx, n.m, id)
}
