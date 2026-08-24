// Real-user tests for Cosmos DB per-database isolation: the {db} path segment
// is honored, so a container name is scoped to its database. Two databases can
// hold same-named containers with independent data, a container in one database
// is unreachable through another, and deleting a database removes only its own
// containers.
package cosmosdb_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

// makeContainer creates database db and container name (partition key "/pk")
// through the SDK and returns its client.
func makeContainer(ctx context.Context, t *testing.T, env *cosmosEnv, db, name string) *azcosmos.ContainerClient {
	t.Helper()

	if _, err := env.client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: db}, nil); err != nil {
		t.Fatalf("CreateDatabase(%s): %v", db, err)
	}

	dbClient, err := env.client.NewDatabase(db)
	if err != nil {
		t.Fatalf("NewDatabase(%s): %v", db, err)
	}

	props := azcosmos.ContainerProperties{
		ID:                     name,
		PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{Paths: []string{"/pk"}},
	}
	if _, err := dbClient.CreateContainer(ctx, props, nil); err != nil {
		t.Fatalf("CreateContainer(%s/%s): %v", db, name, err)
	}

	cc, err := dbClient.NewContainer(name)
	if err != nil {
		t.Fatalf("NewContainer(%s/%s): %v", db, name, err)
	}

	return cc
}

// TestCosmosDatabaseIsolation proves that identically named containers in
// different databases have independent contents: a document written to
// appdb/orders is invisible through otherdb/orders, and each holds its own data.
func TestCosmosDatabaseIsolation(t *testing.T) {
	ctx := context.Background()
	env := newCosmosEnv(t)

	app := makeContainer(ctx, t, env, "appdb", "orders")
	other := makeContainer(ctx, t, env, "otherdb", "orders")

	createDoc(ctx, t, app, "cust-1", map[string]any{"id": "o1", "pk": "cust-1", "who": "app"})
	createDoc(ctx, t, other, "cust-1", map[string]any{"id": "o1", "pk": "cust-1", "who": "other"})

	// Same (pk, id) in each database, but the values are independent.
	if got := readDoc(ctx, t, app, "cust-1", "o1"); got["who"] != "app" {
		t.Errorf("appdb/orders o1 who=%v want app", got["who"])
	}

	if got := readDoc(ctx, t, other, "cust-1", "o1"); got["who"] != "other" {
		t.Errorf("otherdb/orders o1 who=%v want other", got["who"])
	}

	// A document that exists only in appdb is unreachable through otherdb.
	createDoc(ctx, t, app, "cust-2", map[string]any{"id": "app-only", "pk": "cust-2"})

	_, err := other.ReadItem(ctx, azcosmos.NewPartitionKeyString("cust-2"), "app-only", nil)
	wantRespErr(t, err, 404, "cross-database read of app-only doc")
}

// TestCosmosContainerNamespacedByDatabase asserts a container created in one
// database is not reachable as a container of another: operations through the
// wrong database's container client 404.
func TestCosmosContainerNamespacedByDatabase(t *testing.T) {
	ctx := context.Background()
	env := newCosmosEnv(t)

	_ = makeContainer(ctx, t, env, "dbA", "widgets")

	// dbB exists but has no "widgets" container.
	if _, err := env.client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: "dbB"}, nil); err != nil {
		t.Fatalf("CreateDatabase(dbB): %v", err)
	}

	dbB, err := env.client.NewDatabase("dbB")
	if err != nil {
		t.Fatalf("NewDatabase(dbB): %v", err)
	}

	widgetsB, err := dbB.NewContainer("widgets")
	if err != nil {
		t.Fatalf("NewContainer(dbB/widgets): %v", err)
	}

	// Reading dbB/widgets (which was never created) 404s even though dbA/widgets
	// exists.
	_, err = widgetsB.Read(ctx, nil)
	wantRespErr(t, err, 404, "read of container in wrong database")

	pk := azcosmos.NewPartitionKeyString("p1")

	_, err = widgetsB.ReadItem(ctx, pk, "any", nil)
	wantRespErr(t, err, 404, "read item in wrong database's container")
}

// TestCosmosDeleteDatabaseRemovesContainers asserts DeleteDatabase drops the
// database's containers while leaving another database's identically named
// container intact.
func TestCosmosDeleteDatabaseRemovesContainers(t *testing.T) {
	ctx := context.Background()
	env := newCosmosEnv(t)

	victim := makeContainer(ctx, t, env, "tmpdb", "events")
	keep := makeContainer(ctx, t, env, "keepdb", "events")

	createDoc(ctx, t, victim, "p", map[string]any{"id": "e1", "pk": "p", "in": "tmpdb"})
	createDoc(ctx, t, keep, "p", map[string]any{"id": "e1", "pk": "p", "in": "keepdb"})

	// Delete the whole tmpdb database.
	tmp, err := env.client.NewDatabase("tmpdb")
	if err != nil {
		t.Fatalf("NewDatabase(tmpdb): %v", err)
	}

	if _, err := tmp.Delete(ctx, nil); err != nil {
		t.Fatalf("DeleteDatabase(tmpdb): %v", err)
	}

	// Its container's documents are gone: reading through it 404s.
	_, err = victim.ReadItem(ctx, azcosmos.NewPartitionKeyString("p"), "e1", nil)
	wantRespErr(t, err, 404, "read from deleted database's container")

	// Re-reading the database itself 404s.
	_, err = tmp.Read(ctx, nil)
	wantRespErr(t, err, 404, "read of deleted database")

	// keepdb/events is untouched.
	if got := readDoc(ctx, t, keep, "p", "e1"); got["in"] != "keepdb" {
		t.Errorf("keepdb/events e1 in=%v want keepdb (unaffected by tmpdb delete)", got["in"])
	}
}

// TestCosmosDuplicateDatabaseCreate asserts creating a database that already
// exists is a 409 Conflict, matching real Cosmos.
func TestCosmosDuplicateDatabaseCreate(t *testing.T) {
	ctx := context.Background()
	env := newCosmosEnv(t)

	if _, err := env.client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: "dupdb"}, nil); err != nil {
		t.Fatalf("first CreateDatabase: %v", err)
	}

	_, err := env.client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: "dupdb"}, nil)
	wantRespErr(t, err, 409, "duplicate CreateDatabase")
}

// TestCosmosListDatabases asserts the databases the SDK created are the ones
// listed back through the query pager.
func TestCosmosListDatabases(t *testing.T) {
	ctx := context.Background()
	env := newCosmosEnv(t)

	for _, id := range []string{"alpha", "beta", "gamma"} {
		if _, err := env.client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: id}, nil); err != nil {
			t.Fatalf("CreateDatabase(%s): %v", id, err)
		}
	}

	pager := env.client.NewQueryDatabasesPager("SELECT * FROM root", nil)
	seen := map[string]bool{}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		for _, d := range page.Databases {
			seen[d.ID] = true
		}
	}

	for _, id := range []string{"alpha", "beta", "gamma"} {
		if !seen[id] {
			t.Errorf("database %q missing from list (got %v)", id, seen)
		}
	}
}
