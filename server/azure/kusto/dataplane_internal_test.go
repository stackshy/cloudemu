package kusto

import (
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/azure/kusto/kql"
)

func TestClusterFromHost(t *testing.T) {
	cases := map[string]string{
		"":                defaultCluster,
		"127.0.0.1:54321": defaultCluster,
		"localhost":       defaultCluster,
		"testadxcluster.eastus.kusto.windows.net":        "testadxcluster",
		"TestAdx.eastus.kusto.windows.net":               "testadx",
		"ingest-testadxcluster.eastus.kusto.windows.net": "testadxcluster",
		"cluster1.kusto.windows.net:443":                 "cluster1",
	}

	for host, want := range cases {
		if got := clusterFromHost(host); got != want {
			t.Errorf("clusterFromHost(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestHasCmd(t *testing.T) {
	if !hasCmd("show tables", "show tables") {
		t.Error("exact match should be true")
	}

	if hasCmd("show tables", "show table") {
		t.Error(`"show tables" must not match keyword "show table"`)
	}

	if !hasCmd("show table events schema", "show table") {
		t.Error(`"show table events schema" should match "show table"`)
	}

	if !hasCmd("create table t(x:long)", "create table") {
		t.Error("paren-adjacent keyword should match")
	}
}

func TestUnquoteName(t *testing.T) {
	cases := map[string]string{
		"Events":       "Events",
		"['My Table']": "My Table",
		`"T"`:          "T",
		"`T`":          "T",
		"  spaced  ":   "spaced",
	}

	for in, want := range cases {
		if got := unquoteName(in); got != want {
			t.Errorf("unquoteName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExecMgmtLifecycle(t *testing.T) {
	store := newTableStore()

	if _, err := execMgmt(store, "db", ".create table Events (id:long, name:string)"); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Duplicate create is a conflict; create-merge is idempotent.
	if _, err := execMgmt(store, "db", ".create table Events (id:long)"); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create err = %v, want AlreadyExists", err)
	}

	if _, err := execMgmt(store, "db", ".create-merge table Events (id:long)"); err != nil {
		t.Fatalf("create-merge existing: %v", err)
	}

	tables, err := execMgmt(store, "db", ".show tables")
	if err != nil {
		t.Fatalf("show tables: %v", err)
	}

	if len(tables[0].Rows) != 1 {
		t.Fatalf(".show tables rows = %d, want 1", len(tables[0].Rows))
	}

	if _, err := execMgmt(store, "db", ".drop table Events"); err != nil {
		t.Fatalf("drop: %v", err)
	}

	if _, err := execMgmt(store, "db", ".drop table Events"); !cerrors.IsNotFound(err) {
		t.Fatalf("drop missing err = %v, want NotFound", err)
	}

	if _, err := execMgmt(store, "db", ".drop table Events ifexists"); err != nil {
		t.Fatalf("drop ifexists missing: %v", err)
	}
}

func TestExecMgmtShowSchema(t *testing.T) {
	store := newTableStore()
	mustExec(t, store, ".create table T (a:long, b:string)")

	csl := mustExec(t, store, ".show table T cslschema")
	if got := csl[0].Rows[0][1]; got != "a:long,b:string" {
		t.Fatalf("cslschema = %v, want a:long,b:string", got)
	}

	dbTab := mustExec(t, store, ".show database schema")
	if len(dbTab[0].Rows) != 2 {
		t.Fatalf(".show database schema rows = %d, want 2 (one per column)", len(dbTab[0].Rows))
	}

	dbJSON := mustExec(t, store, ".show database schema as json")
	if len(dbJSON[0].Columns) != 1 || dbJSON[0].Columns[0].Name != "DatabaseSchema" {
		t.Fatalf(".show database schema as json columns = %+v, want single DatabaseSchema", dbJSON[0].Columns)
	}
}

func TestExecMgmtErrors(t *testing.T) {
	store := newTableStore()

	if _, err := execMgmt(store, "db", "Events | count"); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("non-dot command err = %v, want InvalidArgument", err)
	}

	if _, err := execMgmt(store, "db", ".create table Bad"); err == nil {
		t.Fatal("create without column list should error")
	}

	if _, err := execMgmt(store, "db", ".show table Missing cslschema"); !cerrors.IsNotFound(err) {
		t.Fatalf("show missing table err = %v, want NotFound", err)
	}

	if _, err := execMgmt(store, "db", ".frobnicate everything"); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("unknown command err = %v, want InvalidArgument", err)
	}
}

func TestEncodeV1(t *testing.T) {
	tables := []kql.Table{{
		Columns: []kql.Column{{Name: "id", Type: kql.TypeLong}, {Name: "name", Type: kql.TypeString}},
		Rows:    [][]any{{int64(1), "a"}},
	}}

	out := encodeV1(tables)
	if len(out.Tables) != 1 {
		t.Fatalf("tables = %d, want 1", len(out.Tables))
	}

	tbl := out.Tables[0]
	if tbl.TableName != "Table_0" {
		t.Errorf("TableName = %q, want Table_0", tbl.TableName)
	}

	if tbl.Columns[0].DataType != "Int64" || tbl.Columns[0].ColumnType != "long" {
		t.Errorf("column 0 = %+v, want Int64/long", tbl.Columns[0])
	}
}

func TestEncodeV1EmptyRows(t *testing.T) {
	out := encodeV1([]kql.Table{{Columns: []kql.Column{{Name: "x", Type: kql.TypeLong}}}})
	if out.Tables[0].Rows == nil {
		t.Fatal("empty Rows must encode as [], not null")
	}
}

func TestDataStoreScoping(t *testing.T) {
	ds := newDataStore()

	a := ds.storeFor("clusterA", "db1")
	b := ds.storeFor("clustera", "DB1")
	c := ds.storeFor("clusterA", "db2")

	if a != b {
		t.Error("cluster/db lookup must be case-insensitive (same store)")
	}

	if a == c {
		t.Error("different databases must have different stores")
	}
}

func mustExec(t *testing.T, store *tableStore, csl string) []kql.Table {
	t.Helper()

	tables, err := execMgmt(store, "db", csl)
	if err != nil {
		t.Fatalf("execMgmt(%q): %v", csl, err)
	}

	return tables
}
