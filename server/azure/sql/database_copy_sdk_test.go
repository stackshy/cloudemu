package sql_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
)

// seedServerAndSource creates a logical server plus a source database with a
// distinctive collation and SKU, returning the source database's ARM ID so a
// copy/restore create can reference it.
func seedServerAndSource(t *testing.T, servers *armsql.ServersClient, dbs *armsql.DatabasesClient) string {
	t.Helper()

	ctx := context.Background()

	srvPoller, err := servers.BeginCreateOrUpdate(ctx, "rg-1", "srv1", armsql.Server{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("server BeginCreateOrUpdate: %v", err)
	}

	if _, err := srvPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("server PollUntilDone: %v", err)
	}

	srcPoller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "sourcedb", armsql.Database{
		Location: to.Ptr("eastus"),
		SKU:      &armsql.SKU{Name: to.Ptr("S3")},
		Properties: &armsql.DatabaseProperties{
			Collation: to.Ptr("SQL_Latin1_General_CP1_CI_AS"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("source BeginCreateOrUpdate: %v", err)
	}

	srcResp, err := srcPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("source PollUntilDone: %v", err)
	}

	if srcResp.Database.ID == nil {
		t.Fatal("source database missing ID")
	}

	return *srcResp.Database.ID
}

// TestSDKAzureSQLDatabaseCopy exercises a real armsql CreateMode=Copy create:
// the copy is a new, independent database seeded from the source's properties.
func TestSDKAzureSQLDatabaseCopy(t *testing.T) {
	servers, dbs := newSDKClients(t)
	ctx := context.Background()

	sourceID := seedServerAndSource(t, servers, dbs)

	copyPoller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "copydb", armsql.Database{
		Location: to.Ptr("eastus"),
		Properties: &armsql.DatabaseProperties{
			CreateMode:       to.Ptr(armsql.CreateModeCopy),
			SourceDatabaseID: to.Ptr(sourceID),
		},
	}, nil)
	if err != nil {
		t.Fatalf("copy BeginCreateOrUpdate: %v", err)
	}

	copyResp, err := copyPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("copy PollUntilDone: %v", err)
	}

	if copyResp.Database.Name == nil || *copyResp.Database.Name != "copydb" {
		t.Fatalf("got copy name %v, want copydb", copyResp.Database.Name)
	}

	// The copy inherits the source's collation and SKU (request left them unset).
	if copyResp.Database.Properties == nil || copyResp.Database.Properties.Collation == nil ||
		*copyResp.Database.Properties.Collation != "SQL_Latin1_General_CP1_CI_AS" {
		t.Fatalf("copy did not inherit source collation: %+v", copyResp.Database.Properties)
	}

	if copyResp.Database.SKU == nil || copyResp.Database.SKU.Name == nil || *copyResp.Database.SKU.Name != "S3" {
		t.Fatalf("copy did not inherit source SKU: %+v", copyResp.Database.SKU)
	}

	// The copy is independent: deleting the source leaves the copy intact.
	delPoller, err := dbs.BeginDelete(ctx, "rg-1", "srv1", "sourcedb", nil)
	if err != nil {
		t.Fatalf("source BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("source delete PollUntilDone: %v", err)
	}

	got, err := dbs.Get(ctx, "rg-1", "srv1", "copydb", nil)
	if err != nil {
		t.Fatalf("copy Get after source delete: %v", err)
	}

	if got.Database.Name == nil || *got.Database.Name != "copydb" {
		t.Fatal("copy vanished after source delete")
	}
}

// TestSDKAzureSQLDatabaseCopyOverride confirms an explicit SKU/collation on the
// copy request overrides the source's values.
func TestSDKAzureSQLDatabaseCopyOverride(t *testing.T) {
	servers, dbs := newSDKClients(t)
	ctx := context.Background()

	sourceID := seedServerAndSource(t, servers, dbs)

	poller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "copydb", armsql.Database{
		Location: to.Ptr("eastus"),
		SKU:      &armsql.SKU{Name: to.Ptr("S6")},
		Properties: &armsql.DatabaseProperties{
			CreateMode:       to.Ptr(armsql.CreateModeCopy),
			SourceDatabaseID: to.Ptr(sourceID),
		},
	}, nil)
	if err != nil {
		t.Fatalf("copy BeginCreateOrUpdate: %v", err)
	}

	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("copy PollUntilDone: %v", err)
	}

	if resp.Database.SKU == nil || resp.Database.SKU.Name == nil || *resp.Database.SKU.Name != "S6" {
		t.Fatalf("explicit SKU not honored on copy: %+v", resp.Database.SKU)
	}
}

// TestSDKAzureSQLDatabasePointInTimeRestore exercises CreateMode=PointInTimeRestore,
// modeled as a copy of the source's current state.
func TestSDKAzureSQLDatabasePointInTimeRestore(t *testing.T) {
	servers, dbs := newSDKClients(t)
	ctx := context.Background()

	sourceID := seedServerAndSource(t, servers, dbs)

	poller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "restored", armsql.Database{
		Location: to.Ptr("eastus"),
		Properties: &armsql.DatabaseProperties{
			CreateMode:       to.Ptr(armsql.CreateModePointInTimeRestore),
			SourceDatabaseID: to.Ptr(sourceID),
		},
	}, nil)
	if err != nil {
		t.Fatalf("PITR BeginCreateOrUpdate: %v", err)
	}

	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PITR PollUntilDone: %v", err)
	}

	if resp.Database.Name == nil || *resp.Database.Name != "restored" {
		t.Fatalf("got restored name %v, want restored", resp.Database.Name)
	}

	if resp.Database.Properties == nil || resp.Database.Properties.Collation == nil ||
		*resp.Database.Properties.Collation != "SQL_Latin1_General_CP1_CI_AS" {
		t.Fatal("PITR did not inherit source collation")
	}
}

// TestSDKAzureSQLDatabaseCopyMissingSource confirms a copy pointing at a
// nonexistent source fails (SourceDatabaseNotFound), not a silent empty create.
func TestSDKAzureSQLDatabaseCopyMissingSource(t *testing.T) {
	servers, dbs := newSDKClients(t)
	ctx := context.Background()

	// Server exists, source database does not.
	srvPoller, err := servers.BeginCreateOrUpdate(ctx, "rg-1", "srv1", armsql.Server{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("server BeginCreateOrUpdate: %v", err)
	}

	if _, err := srvPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("server PollUntilDone: %v", err)
	}

	missingID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Sql/servers/srv1/databases/ghost"

	poller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "copydb", armsql.Database{
		Location: to.Ptr("eastus"),
		Properties: &armsql.DatabaseProperties{
			CreateMode:       to.Ptr(armsql.CreateModeCopy),
			SourceDatabaseID: to.Ptr(missingID),
		},
	}, nil)
	if err == nil {
		_, err = poller.PollUntilDone(ctx, nil)
	}

	if err == nil {
		t.Fatal("expected error copying from a nonexistent source")
	}

	if !strings.Contains(err.Error(), "SourceDatabaseNotFound") {
		t.Fatalf("expected SourceDatabaseNotFound, got %v", err)
	}

	// The target database must not have been created.
	if _, gErr := dbs.Get(ctx, "rg-1", "srv1", "copydb", nil); gErr == nil {
		t.Fatal("copy target was created despite missing source")
	}
}

// TestSDKAzureSQLDatabaseCopyNoSourceID confirms Copy without a sourceDatabaseId
// is rejected (400) rather than creating an empty database.
func TestSDKAzureSQLDatabaseCopyNoSourceID(t *testing.T) {
	servers, dbs := newSDKClients(t)
	ctx := context.Background()

	srvPoller, err := servers.BeginCreateOrUpdate(ctx, "rg-1", "srv1", armsql.Server{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("server BeginCreateOrUpdate: %v", err)
	}

	if _, err := srvPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("server PollUntilDone: %v", err)
	}

	poller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "copydb", armsql.Database{
		Location: to.Ptr("eastus"),
		Properties: &armsql.DatabaseProperties{
			CreateMode: to.Ptr(armsql.CreateModeCopy),
		},
	}, nil)
	if err == nil {
		_, err = poller.PollUntilDone(ctx, nil)
	}

	if err == nil {
		t.Fatal("expected error for Copy without sourceDatabaseId")
	}
}
