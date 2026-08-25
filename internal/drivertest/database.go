package drivertest

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

// Fixture names shared by the database subtests, kept as constants rather than
// repeated literals.
const (
	confTable = "conformance"
	confPK    = "pk"
	confSK    = "sk"
)

// Expected counts for the fixed fixtures the database tests build.
const (
	wantPartitionItemCount = 2
	paginationItemTotal    = 5
	paginationPageSize     = 2
	paginationMaxRounds    = paginationItemTotal + 1
)

// RunDatabaseConformance runs the shared behavioral contract of
// services/database/driver.Database against a freshly constructed driver
// instance, obtained by calling newDriver. newDriver is called once per subtest
// so each one starts from an empty, isolated backend.
//
// Only behavior genuinely shared across AWS DynamoDB, Azure Cosmos DB and GCP
// Firestore is encoded here — table CRUD, item put/get/update/delete, key-condition
// queries with deterministic pagination, and the Global Secondary Index surface.
// Provider-only semantics (DynamoDB's UpdateItem upsert, item-size ceilings,
// key-type validation, streams/change-feed, transactions, LSIs, ...) stay in each
// provider's own tests, not in a conformance suite every provider must satisfy
// identically.
func RunDatabaseConformance(t *testing.T, newDriver func() dbdriver.Database) {
	t.Helper()

	t.Run("CreateTable", func(t *testing.T) { testCreateTable(t, newDriver()) })
	t.Run("DescribeTable", func(t *testing.T) { testDescribeTable(t, newDriver()) })
	t.Run("DeleteTable", func(t *testing.T) { testDeleteTable(t, newDriver()) })
	t.Run("ListTables", func(t *testing.T) { testListTables(t, newDriver()) })
	t.Run("ItemLifecycle", func(t *testing.T) { testItemLifecycle(t, newDriver()) })
	t.Run("UpdateItem", func(t *testing.T) { testUpdateItem(t, newDriver()) })
	t.Run("Query", func(t *testing.T) { testQuery(t, newDriver()) })
	t.Run("QueryPagination", func(t *testing.T) { testQueryPagination(t, newDriver()) })
	t.Run("SecondaryIndex", func(t *testing.T) { testSecondaryIndex(t, newDriver()) })
}

// testCreateTable covers CreateTable's shared contract: a fresh name succeeds
// and a duplicate name is AlreadyExists.
func testCreateTable(t *testing.T, d dbdriver.Database) {
	t.Helper()

	ctx := context.Background()

	mustCreateTable(t, d, confTable)

	err := d.CreateTable(ctx, dbdriver.TableConfig{Name: confTable, PartitionKey: confPK, SortKey: confSK})
	assertAlreadyExists(t, err, "CreateTable(duplicate)")
}

// testDescribeTable covers DescribeTable's shared contract: a created table
// echoes back its Name/PartitionKey/SortKey, and a missing table is NotFound.
func testDescribeTable(t *testing.T, d dbdriver.Database) {
	t.Helper()

	ctx := context.Background()

	_, err := d.DescribeTable(ctx, "nonexistent")
	assertNotFound(t, err, "DescribeTable(missing)")

	mustCreateTable(t, d, confTable)

	cfg, err := d.DescribeTable(ctx, confTable)
	requireNoError(t, err, "DescribeTable")

	if cfg.Name != confTable {
		t.Errorf("DescribeTable: Name = %q, want %q", cfg.Name, confTable)
	}

	if cfg.PartitionKey != confPK {
		t.Errorf("DescribeTable: PartitionKey = %q, want %q", cfg.PartitionKey, confPK)
	}

	if cfg.SortKey != confSK {
		t.Errorf("DescribeTable: SortKey = %q, want %q", cfg.SortKey, confSK)
	}
}

// testDeleteTable covers DeleteTable's shared contract: a missing table is
// NotFound, and a deleted table is afterwards neither describable nor listed.
func testDeleteTable(t *testing.T, d dbdriver.Database) {
	t.Helper()

	ctx := context.Background()
	assertNotFound(t, d.DeleteTable(ctx, "nonexistent"), "DeleteTable(missing)")

	mustCreateTable(t, d, confTable)
	requireNoError(t, d.DeleteTable(ctx, confTable), "DeleteTable")

	_, err := d.DescribeTable(ctx, confTable)
	assertNotFound(t, err, "DescribeTable(after delete)")

	names, err := d.ListTables(ctx)
	requireNoError(t, err, "ListTables")

	for _, n := range names {
		if n == confTable {
			t.Errorf("ListTables: deleted table %q still present", n)
		}
	}
}

// testListTables covers ListTables' shared contract: an empty backend lists
// nothing, and created tables are all reported back (order is not part of the
// contract, so this only checks set membership).
func testListTables(t *testing.T, d dbdriver.Database) {
	t.Helper()

	ctx := context.Background()

	empty, err := d.ListTables(ctx)
	requireNoError(t, err, "ListTables(empty)")

	if len(empty) != 0 {
		t.Errorf("ListTables(empty): want 0 tables, got %d", len(empty))
	}

	mustCreateTable(t, d, "beta")
	mustCreateTable(t, d, "alpha")

	names, err := d.ListTables(ctx)
	requireNoError(t, err, "ListTables")

	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}

	for _, want := range []string{"alpha", "beta"} {
		if !seen[want] {
			t.Errorf("ListTables: missing table %q", want)
		}
	}
}

// testItemLifecycle covers Put/Get/Delete's shared contract: reads and writes
// against a missing table are NotFound, a stored item round-trips its
// attributes, a Get on a missing key is NotFound, and a deleted item is no
// longer gettable.
func testItemLifecycle(t *testing.T, d dbdriver.Database) {
	t.Helper()

	ctx := context.Background()
	item := map[string]any{confPK: "u1", confSK: "s1", "name": "alice"}

	assertNotFound(t, d.PutItem(ctx, "nosuchtable", item), "PutItem(missing table)")

	_, getMissTable := d.GetItem(ctx, "nosuchtable", keyOf("u1", "s1"))
	assertNotFound(t, getMissTable, "GetItem(missing table)")

	assertNotFound(t, d.DeleteItem(ctx, "nosuchtable", keyOf("u1", "s1")), "DeleteItem(missing table)")

	mustCreateTable(t, d, confTable)

	_, getMissKey := d.GetItem(ctx, confTable, keyOf("u1", "s1"))
	assertNotFound(t, getMissKey, "GetItem(missing key)")

	requireNoError(t, d.PutItem(ctx, confTable, item), "PutItem")

	got, err := d.GetItem(ctx, confTable, keyOf("u1", "s1"))
	requireNoError(t, err, "GetItem")

	if got["name"] != "alice" {
		t.Errorf("GetItem: name = %v, want %q", got["name"], "alice")
	}

	// Deleting a key that does not exist is a no-op success on all three.
	requireNoError(t, d.DeleteItem(ctx, confTable, keyOf("nope", "nope")), "DeleteItem(missing key)")

	requireNoError(t, d.DeleteItem(ctx, confTable, keyOf("u1", "s1")), "DeleteItem")

	_, err = d.GetItem(ctx, confTable, keyOf("u1", "s1"))
	assertNotFound(t, err, "GetItem(after delete)")
}

// testUpdateItem covers UpdateItem's shared contract on an existing item: a SET
// action sets a new attribute and a REMOVE action drops one, both visible on the
// next GetItem. (An UpdateItem against a missing item diverges — DynamoDB upserts,
// Cosmos/Firestore return NotFound — so it is deliberately not asserted here.)
func testUpdateItem(t *testing.T, d dbdriver.Database) {
	t.Helper()

	ctx := context.Background()

	mustCreateTable(t, d, confTable)

	item := map[string]any{confPK: "u1", confSK: "s1", "status": "old"}
	requireNoError(t, d.PutItem(ctx, confTable, item), "PutItem")

	_, err := d.UpdateItem(ctx, dbdriver.UpdateItemInput{
		Table: confTable,
		Key:   keyOf("u1", "s1"),
		Actions: []dbdriver.UpdateAction{
			{Action: "SET", Field: "status", Value: "new"},
			{Action: "SET", Field: "extra", Value: "added"},
		},
	})
	requireNoError(t, err, "UpdateItem(SET)")

	got, err := d.GetItem(ctx, confTable, keyOf("u1", "s1"))
	requireNoError(t, err, "GetItem(after SET)")

	if got["status"] != "new" {
		t.Errorf("UpdateItem: status = %v, want %q", got["status"], "new")
	}

	if got["extra"] != "added" {
		t.Errorf("UpdateItem: extra = %v, want %q", got["extra"], "added")
	}

	_, err = d.UpdateItem(ctx, dbdriver.UpdateItemInput{
		Table:   confTable,
		Key:     keyOf("u1", "s1"),
		Actions: []dbdriver.UpdateAction{{Action: "REMOVE", Field: "extra"}},
	})
	requireNoError(t, err, "UpdateItem(REMOVE)")

	got, err = d.GetItem(ctx, confTable, keyOf("u1", "s1"))
	requireNoError(t, err, "GetItem(after REMOVE)")

	if _, present := got["extra"]; present {
		t.Errorf("UpdateItem(REMOVE): attribute %q still present", "extra")
	}
}

// testQuery covers Query's shared contract: a missing table is NotFound, a
// partition-key condition returns exactly the items under that partition, a
// sort-key equality further narrows the result, and an unknown index is NotFound.
func testQuery(t *testing.T, d dbdriver.Database) {
	t.Helper()

	ctx := context.Background()

	_, err := d.Query(ctx, dbdriver.QueryInput{Table: "nosuchtable"})
	assertNotFound(t, err, "Query(missing table)")

	mustCreateTable(t, d, confTable)

	for _, it := range []map[string]any{
		{confPK: "u1", confSK: "a"},
		{confPK: "u1", confSK: "b"},
		{confPK: "u2", confSK: "c"},
	} {
		requireNoError(t, d.PutItem(ctx, confTable, it), "PutItem")
	}

	byPartition, err := d.Query(ctx, dbdriver.QueryInput{
		Table:        confTable,
		KeyCondition: dbdriver.KeyCondition{PartitionKey: confPK, PartitionVal: "u1"},
	})
	requireNoError(t, err, "Query(partition)")

	if len(byPartition.Items) != wantPartitionItemCount {
		t.Errorf("Query(partition=u1): got %d items, want %d", len(byPartition.Items), wantPartitionItemCount)
	}

	bySort, err := d.Query(ctx, dbdriver.QueryInput{
		Table:        confTable,
		KeyCondition: dbdriver.KeyCondition{PartitionKey: confPK, PartitionVal: "u1", SortOp: "=", SortVal: "a"},
	})
	requireNoError(t, err, "Query(partition+sort)")

	if len(bySort.Items) != 1 || bySort.Items[0][confSK] != "a" {
		t.Errorf("Query(partition=u1, sk=a): got %v, want exactly the (u1,a) item", bySort.Items)
	}

	_, err = d.Query(ctx, dbdriver.QueryInput{
		Table:        confTable,
		IndexName:    "no-such-index",
		KeyCondition: dbdriver.KeyCondition{PartitionVal: "u1"},
	})
	assertNotFound(t, err, "Query(unknown index)")
}

// testQueryPagination covers the pagination determinism Query guarantees: with
// a page Limit smaller than the partition, following NextPageToken pages
// exhaustively through every item with no duplicates and no omissions, ending in
// an empty token; and the same first page, requested twice, returns an identical
// ordering (a stable order is what keeps the page tokens valid).
func testQueryPagination(t *testing.T, d dbdriver.Database) {
	t.Helper()

	ctx := context.Background()

	mustCreateTable(t, d, confTable)

	want := map[string]bool{}

	for i := range paginationItemTotal {
		sk := string(rune('a' + i))
		requireNoError(t, d.PutItem(ctx, confTable, map[string]any{confPK: "u1", confSK: sk}), "PutItem")

		want[sk] = true
	}

	got := collectQueryPages(t, d, paginationPageSize, paginationMaxRounds)

	if len(got) != len(want) {
		t.Fatalf("pagination: collected %d items across pages, want %d", len(got), len(want))
	}

	for sk := range want {
		if !got[sk] {
			t.Errorf("pagination: sort key %q never returned across any page", sk)
		}
	}

	first := firstPageSortKeys(t, d)
	second := firstPageSortKeys(t, d)

	if len(first) != len(second) {
		t.Fatalf("pagination determinism: first page sizes differ (%d vs %d)", len(first), len(second))
	}

	for i := range first {
		if first[i] != second[i] {
			t.Errorf("pagination determinism: first page order differs at %d: %q vs %q", i, first[i], second[i])
		}
	}
}

// collectQueryPages walks every page of the confTable partition "u1" via Query
// with the given page size, following NextPageToken until it is empty, and
// returns the set of sort keys seen. It fails on a duplicate key or on exceeding
// maxRounds (a runaway pagination loop).
func collectQueryPages(t *testing.T, d dbdriver.Database, pageSize, maxRounds int) map[string]bool {
	t.Helper()

	ctx := context.Background()
	got := map[string]bool{}
	token := ""

	for page := range maxRounds {
		result, err := d.Query(ctx, dbdriver.QueryInput{
			Table:        confTable,
			KeyCondition: dbdriver.KeyCondition{PartitionKey: confPK, PartitionVal: "u1"},
			Limit:        pageSize,
			PageToken:    token,
		})
		requireNoError(t, err, "Query(page)")

		for i := range result.Items {
			sk, _ := result.Items[i][confSK].(string)

			if got[sk] {
				t.Errorf("Query(page %d): sort key %q returned more than once", page, sk)
			}

			got[sk] = true
		}

		if result.NextPageToken == "" {
			return got
		}

		token = result.NextPageToken
	}

	t.Fatalf("pagination did not terminate within %d pages", maxRounds)

	return got
}

// firstPageSortKeys returns the ordered sort keys of the first page of the
// confTable partition "u1", used to assert the ordering is deterministic across
// identical calls.
func firstPageSortKeys(t *testing.T, d dbdriver.Database) []string {
	t.Helper()

	result, err := d.Query(context.Background(), dbdriver.QueryInput{
		Table:        confTable,
		KeyCondition: dbdriver.KeyCondition{PartitionKey: confPK, PartitionVal: "u1"},
		Limit:        paginationPageSize,
	})
	requireNoError(t, err, "Query(first page)")

	keys := make([]string, 0, len(result.Items))

	for i := range result.Items {
		sk, _ := result.Items[i][confSK].(string)
		keys = append(keys, sk)
	}

	return keys
}

// testSecondaryIndex covers the Global Secondary Index surface shared by all
// three providers: CreateIndex validates the table and a non-empty name and
// rejects a duplicate, a created index is describable and listed, and DeleteIndex
// removes it — with NotFound reported for a missing table or index throughout.
func testSecondaryIndex(t *testing.T, d dbdriver.Database) {
	t.Helper()

	ctx := context.Background()
	gsi := dbdriver.GSIConfig{Name: "by-email", PartitionKey: "email"}

	_, err := d.CreateIndex(ctx, "nosuchtable", gsi)
	assertNotFound(t, err, "CreateIndex(missing table)")

	mustCreateTable(t, d, confTable)

	_, err = d.CreateIndex(ctx, confTable, dbdriver.GSIConfig{Name: ""})
	assertInvalidArgument(t, err, "CreateIndex(empty name)")

	info, err := d.CreateIndex(ctx, confTable, gsi)
	requireNoError(t, err, "CreateIndex")

	if info.Name != gsi.Name || info.PartitionKey != gsi.PartitionKey {
		t.Errorf("CreateIndex: info = %+v, want name=%q pk=%q", info, gsi.Name, gsi.PartitionKey)
	}

	_, err = d.CreateIndex(ctx, confTable, gsi)
	assertAlreadyExists(t, err, "CreateIndex(duplicate)")

	desc, err := d.DescribeIndex(ctx, confTable, gsi.Name)
	requireNoError(t, err, "DescribeIndex")

	if desc.Name != gsi.Name {
		t.Errorf("DescribeIndex: Name = %q, want %q", desc.Name, gsi.Name)
	}

	testSecondaryIndexDelete(t, d, gsi.Name)
}

// testSecondaryIndexDelete covers the list/delete half of the GSI contract
// against confTable, which already holds the index named indexName: it is listed,
// DeleteIndex removes it (a later Describe is NotFound), and deleting a missing
// index is NotFound.
func testSecondaryIndexDelete(t *testing.T, d dbdriver.Database, indexName string) {
	t.Helper()

	ctx := context.Background()

	indexes, err := d.ListIndexes(ctx, confTable)
	requireNoError(t, err, "ListIndexes")

	if len(indexes) != 1 || indexes[0].Name != indexName {
		t.Errorf("ListIndexes: got %v, want exactly [%q]", indexes, indexName)
	}

	requireNoError(t, d.DeleteIndex(ctx, confTable, indexName), "DeleteIndex")

	_, err = d.DescribeIndex(ctx, confTable, indexName)
	assertNotFound(t, err, "DescribeIndex(after delete)")

	assertNotFound(t, d.DeleteIndex(ctx, confTable, indexName), "DeleteIndex(missing index)")
}

// keyOf builds a primary-key map for the confPK/confSK schema the database
// tests use, so callers name a key inline without repeating the field names.
func keyOf(pk, sk string) map[string]any {
	return map[string]any{confPK: pk, confSK: sk}
}

// mustCreateTable creates a confPK/confSK table and fails the test immediately
// if the driver rejects it, so setup errors surface at the right line instead of
// cascading into unrelated assertion failures.
func mustCreateTable(t *testing.T, d dbdriver.Database, name string) {
	t.Helper()
	requireNoError(t, d.CreateTable(context.Background(),
		dbdriver.TableConfig{Name: name, PartitionKey: confPK, SortKey: confSK}), "CreateTable("+name+")")
}

// assertAlreadyExists reports (t.Errorf, non-fatal) unless err is an
// AlreadyExists error, naming the failing operation.
func assertAlreadyExists(t *testing.T, err error, what string) {
	t.Helper()

	if !cerrors.IsAlreadyExists(err) {
		t.Errorf("%s: want AlreadyExists, got %v", what, err)
	}
}

// assertInvalidArgument reports (t.Errorf, non-fatal) unless err is an
// InvalidArgument error, naming the failing operation.
func assertInvalidArgument(t *testing.T, err error, what string) {
	t.Helper()

	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("%s: want InvalidArgument, got %v", what, err)
	}
}
