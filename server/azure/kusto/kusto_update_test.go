package kusto_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/kusto/armkusto"
)

// TestSDKKustoClusterUpdate drives the cluster PATCH (ClustersClient.Update):
// tags merge into the existing set, sku capacity and a mutable property are
// applied, and the synthesized URIs / run state are preserved.
func TestSDKKustoClusterUpdate(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()

	clusters, err := armkusto.NewClustersClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewClustersClient: %v", err)
	}

	createCluster(t, ctx, clusters)

	poller, err := clusters.BeginUpdate(ctx, rgName, clusterName, armkusto.ClusterUpdate{
		Tags: map[string]*string{"team": to.Ptr("data")},
		SKU: &armkusto.AzureSKU{
			Name:     to.Ptr(armkusto.AzureSKUNameStandardD11V2),
			Tier:     to.Ptr(armkusto.AzureSKUTierStandard),
			Capacity: to.Ptr[int32](4),
		},
		Properties: &armkusto.ClusterProperties{EnableStreamingIngest: to.Ptr(true)},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate cluster: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll cluster update: %v", err)
	}

	got, err := clusters.Get(ctx, rgName, clusterName, nil)
	if err != nil {
		t.Fatalf("Get cluster: %v", err)
	}

	// Existing tag preserved, new tag merged in.
	if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
		t.Errorf("tag env = %v, want test (existing tag must survive)", got.Tags["env"])
	}

	if got.Tags["team"] == nil || *got.Tags["team"] != "data" {
		t.Errorf("tag team = %v, want data", got.Tags["team"])
	}

	if got.SKU == nil || got.SKU.Capacity == nil || *got.SKU.Capacity != 4 {
		t.Errorf("sku capacity = %v, want 4", got.SKU)
	}

	if got.Properties == nil || got.Properties.EnableStreamingIngest == nil || !*got.Properties.EnableStreamingIngest {
		t.Errorf("enableStreamingIngest not applied: %+v", got.Properties)
	}

	// Server-computed fields survive the PATCH.
	assertClusterURIs(t, got.Properties)

	if got.Properties.State == nil || *got.Properties.State != armkusto.StateRunning {
		t.Errorf("state = %v, want Running (preserved)", got.Properties.State)
	}
}

// TestSDKKustoClusterUpdateMissing verifies a PATCH on a missing cluster is a 404.
func TestSDKKustoClusterUpdateMissing(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()

	clusters, err := armkusto.NewClustersClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewClustersClient: %v", err)
	}

	_, err = clusters.BeginUpdate(ctx, rgName, "no-such-cluster",
		armkusto.ClusterUpdate{Tags: map[string]*string{"x": to.Ptr("y")}}, nil)
	if err == nil {
		t.Fatal("expected error updating a missing cluster, got nil")
	}
}

// TestSDKKustoDatabaseUpdate drives the database PATCH (DatabasesClient.Update):
// the supplied retention window is applied and the untouched one is preserved.
func TestSDKKustoDatabaseUpdate(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()

	clusters, err := armkusto.NewClustersClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewClustersClient: %v", err)
	}

	createCluster(t, ctx, clusters)

	databases, err := armkusto.NewDatabasesClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewDatabasesClient: %v", err)
	}

	createDatabase(t, ctx, databases)

	poller, err := databases.BeginUpdate(ctx, rgName, clusterName, dbName, &armkusto.ReadWriteDatabase{
		Kind:       to.Ptr(armkusto.KindReadWrite),
		Properties: &armkusto.ReadWriteDatabaseProperties{HotCachePeriod: to.Ptr("P14D")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate database: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll database update: %v", err)
	}

	got, err := databases.Get(ctx, rgName, clusterName, dbName, nil)
	if err != nil {
		t.Fatalf("Get database: %v", err)
	}

	rw, ok := got.DatabaseClassification.(*armkusto.ReadWriteDatabase)
	if !ok {
		t.Fatalf("database kind = %T, want *ReadWriteDatabase", got.DatabaseClassification)
	}

	if rw.Properties == nil || rw.Properties.HotCachePeriod == nil || *rw.Properties.HotCachePeriod != "P14D" {
		t.Errorf("hotCachePeriod = %v, want P14D", rw.Properties)
	}

	// The retention window not sent in the PATCH must be preserved.
	if rw.Properties.SoftDeletePeriod == nil || *rw.Properties.SoftDeletePeriod != "P30D" {
		t.Errorf("softDeletePeriod = %v, want P30D (preserved)", rw.Properties.SoftDeletePeriod)
	}
}

// TestSDKKustoDatabaseUpdateMissing verifies a PATCH on a missing database is a 404.
func TestSDKKustoDatabaseUpdateMissing(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()

	clusters, err := armkusto.NewClustersClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewClustersClient: %v", err)
	}

	createCluster(t, ctx, clusters)

	databases, err := armkusto.NewDatabasesClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewDatabasesClient: %v", err)
	}

	_, err = databases.BeginUpdate(ctx, rgName, clusterName, "no-such-db", &armkusto.ReadWriteDatabase{
		Kind:       to.Ptr(armkusto.KindReadWrite),
		Properties: &armkusto.ReadWriteDatabaseProperties{HotCachePeriod: to.Ptr("P1D")},
	}, nil)
	if err == nil {
		t.Fatal("expected error updating a missing database, got nil")
	}
}
