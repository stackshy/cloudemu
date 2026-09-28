package gcp

import (
	"context"

	gkeprov "github.com/stackshy/cloudemu/v2/providers/gcp/gke"
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
