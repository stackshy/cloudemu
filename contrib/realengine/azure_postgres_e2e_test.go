package realengine_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
	_ "github.com/lib/pq"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/realengine"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

type azureFakeCred struct{}

func (azureFakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// TestAzurePostgresFlexE2E runs the real-user flow against Azure Database for
// PostgreSQL Flexible Server: create the server with the real Azure SDK, read
// its fully-qualified domain name, connect to it with a real Postgres client
// using the administrator credentials, run SQL, then delete — all against
// CloudEmu backed by a real embedded Postgres (no Docker, no cloud account).
func TestAzurePostgresFlexE2E(t *testing.T) {
	// Default engine port (5432) — the port Azure PostgreSQL clients always use.
	eng := realengine.NewPostgres(0)
	t.Cleanup(func() { _ = eng.Close() })

	cloudP := cloudemu.NewAzure(config.WithDatabaseEngine(eng))
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{PostgresFlex: cloudP.PostgresFlex}))
	t.Cleanup(ts.Close)

	client, err := armpostgresqlflexibleservers.NewServersClient("sub-1", azureFakeCred{}, armOpts(ts))
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	ctx := context.Background()

	const (
		rg     = "rg-1"
		server = "app-pg"
		user   = "pgadmin"
		pass   = "Sup3rs3cret1"
	)

	// 1. Create the server — like `az postgres flexible-server create`.
	createPoller, err := client.BeginCreate(ctx, rg, server, armpostgresqlflexibleservers.Server{
		Location: to.Ptr("eastus"),
		SKU:      &armpostgresqlflexibleservers.SKU{Name: to.Ptr("Standard_B1ms"), Tier: to.Ptr(armpostgresqlflexibleservers.SKUTierBurstable)},
		Properties: &armpostgresqlflexibleservers.ServerProperties{
			AdministratorLogin:         to.Ptr(user),
			AdministratorLoginPassword: to.Ptr(pass),
			Version:                    to.Ptr(armpostgresqlflexibleservers.ServerVersionFourteen),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	if _, err := createPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("create poll: %v", err)
	}

	// 2. Read the endpoint the SDK reports (the real Postgres host).
	got, err := client.Get(ctx, rg, server, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.FullyQualifiedDomainName == nil {
		t.Fatalf("no FQDN reported: %+v", got.Server)
	}

	host := *got.Properties.FullyQualifiedDomainName

	// Connect exactly as a real Azure client would: the FQDN from the SDK on
	// Azure PostgreSQL's fixed port 5432 — no out-of-band port knowledge. The
	// provisioned database defaults to the server name when create carries no
	// DBName.
	dsn := fmt.Sprintf("host=%s port=5432 user=%s password=%s dbname=%s sslmode=disable", host, user, pass, server)

	// 3. Connect with a real Postgres client and run real SQL.
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}

	if _, err := db.Exec("CREATE TABLE accounts (id int primary key, name text)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	if _, err := db.Exec("INSERT INTO accounts VALUES (1, 'cloudemu')"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var name string
	if err := db.QueryRow("SELECT name FROM accounts WHERE id = 1").Scan(&name); err != nil {
		t.Fatalf("select: %v", err)
	}

	if name != "cloudemu" {
		t.Fatalf("round-trip mismatch: got %q", name)
	}

	_ = db.Close()

	// 4. Delete the server — the real database is torn down.
	delPoller, err := client.BeginDelete(ctx, rg, server, nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete poll: %v", err)
	}

	gone, _ := sql.Open("postgres", dsn)
	defer gone.Close()

	if err := gone.Ping(); err == nil {
		t.Fatal("expected connection to the deleted server's database to fail")
	}
}

func armOpts(ts *httptest.Server) *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.Configuration{
				ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
				},
			},
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}
}
