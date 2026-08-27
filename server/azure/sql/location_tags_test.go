package sql_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
)

// TestSDKAzureSQLServerEchoesLocation asserts the submitted region round-trips
// instead of leaking an AWS-style default region.
func TestSDKAzureSQLServerEchoesLocation(t *testing.T) {
	servers, _ := newSDKClients(t)
	ctx := context.Background()

	poller, err := servers.BeginCreateOrUpdate(ctx, "rg-1", "loc1", armsql.Server{
		Location: to.Ptr("westus2"),
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	got, err := servers.Get(ctx, "rg-1", "loc1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Server.Location == nil || *got.Server.Location != "westus2" {
		t.Fatalf("server location = %v, want westus2", got.Server.Location)
	}
}

// TestSDKAzureSQLDatabaseEchoesLocationAndTags asserts a database round-trips
// its submitted location and tags.
func TestSDKAzureSQLDatabaseEchoesLocationAndTags(t *testing.T) {
	servers, dbs := newSDKClients(t)
	ctx := context.Background()

	srvPoller, err := servers.BeginCreateOrUpdate(ctx, "rg-1", "srv1", armsql.Server{
		Location: to.Ptr("westus2"),
	}, nil)
	if err != nil {
		t.Fatalf("Server BeginCreateOrUpdate: %v", err)
	}

	if _, err := srvPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Server PollUntilDone: %v", err)
	}

	dbPoller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "appdb", armsql.Database{
		Location: to.Ptr("westus2"),
		Tags:     map[string]*string{"team": to.Ptr("data")},
		SKU:      &armsql.SKU{Name: to.Ptr("S0")},
	}, nil)
	if err != nil {
		t.Fatalf("DB BeginCreateOrUpdate: %v", err)
	}

	if _, err := dbPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("DB PollUntilDone: %v", err)
	}

	got, err := dbs.Get(ctx, "rg-1", "srv1", "appdb", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Database.Location == nil || *got.Database.Location != "westus2" {
		t.Fatalf("database location = %v, want westus2", got.Database.Location)
	}

	tag, ok := got.Database.Tags["team"]
	if !ok || tag == nil || *tag != "data" {
		t.Fatalf("database tag team = %v, want data", got.Database.Tags)
	}
}

// TestSDKAzureSQLDatabaseUnderMissingServer asserts a database PUT under a
// never-created server fails with 404 ParentResourceNotFound.
func TestSDKAzureSQLDatabaseUnderMissingServer(t *testing.T) {
	_, dbs := newSDKClients(t)
	ctx := context.Background()

	poller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "ghostsrv", "db1", armsql.Database{
		Location: to.Ptr("westus2"),
	}, nil)
	if err == nil {
		_, err = poller.PollUntilDone(ctx, nil)
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected an azcore.ResponseError, got %T: %v", err, err)
	}

	if respErr.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", respErr.StatusCode)
	}

	if respErr.ErrorCode != "ParentResourceNotFound" {
		t.Fatalf("error code = %q, want ParentResourceNotFound", respErr.ErrorCode)
	}
}
