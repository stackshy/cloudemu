package mysql_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mysql/armmysqlflexibleservers"
	_ "github.com/go-sql-driver/mysql"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine/internal/dtest"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine/mysql"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestAzureMySQLFlexE2E runs the real-user flow against Azure Database for MySQL —
// Flexible Server: create the server with the real Azure SDK, read its
// fully-qualified domain name, connect with a real MySQL client using the
// administrator credentials on the fixed port 3306, run SQL, then delete — all
// against CloudEmu backed by a real MySQL container (no cloud account).
func TestAzureMySQLFlexE2E(t *testing.T) {
	if !dtest.DockerUp() {
		t.Skip("docker daemon not available")
	}

	// Default engine port (3306) — the port Azure MySQL clients always use, so the
	// FQDN alone is enough to connect.
	eng := mysql.New(0)
	t.Cleanup(func() { _ = eng.Close() })

	cloudP := cloudemu.NewAzure(config.WithDatabaseEngine(eng))
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{MySQLFlex: cloudP.MySQLFlex}))
	t.Cleanup(ts.Close)

	client, err := armmysqlflexibleservers.NewServersClient("sub-1", dtest.FakeCred{}, dtest.ARMOptions(ts))
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	ctx := context.Background()

	const (
		rg     = "rg-1"
		server = "app-my"
		user   = "myadmin"
		pass   = "Sup3rs3cret1"
	)

	// 1. Create the server — like `az mysql flexible-server create`.
	createPoller, err := client.BeginCreate(ctx, rg, server, armmysqlflexibleservers.Server{
		Location: to.Ptr("eastus"),
		SKU:      &armmysqlflexibleservers.SKU{Name: to.Ptr("Standard_B1ms"), Tier: to.Ptr(armmysqlflexibleservers.SKUTierBurstable)},
		Properties: &armmysqlflexibleservers.ServerProperties{
			AdministratorLogin:         to.Ptr(user),
			AdministratorLoginPassword: to.Ptr(pass),
			Version:                    to.Ptr(armmysqlflexibleservers.ServerVersionEight021),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	if _, err := createPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("create poll: %v", err)
	}

	// 2. Read the endpoint the SDK reports (the real MySQL host).
	got, err := client.Get(ctx, rg, server, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.FullyQualifiedDomainName == nil {
		t.Fatalf("no FQDN reported: %+v", got.Server)
	}

	host := *got.Properties.FullyQualifiedDomainName

	// Connect exactly as a real Azure client would: the FQDN from the SDK on
	// MySQL Flexible Server's fixed port 3306 — no out-of-band port knowledge. The
	// provisioned database defaults to the server name when create carries no
	// DBName.
	dsn := fmt.Sprintf("%s:%s@tcp(%s:3306)/%s", user, pass, host, server)

	// 3. Connect with a real MySQL client and run real SQL.
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}

	db.SetConnMaxLifetime(time.Minute)

	if _, err := db.Exec("CREATE TABLE accounts (id int primary key, name varchar(64))"); err != nil {
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

	gone, _ := sql.Open("mysql", dsn)
	defer gone.Close()

	if err := gone.Ping(); err == nil {
		t.Fatal("expected connection to the deleted server's database to fail")
	}
}
