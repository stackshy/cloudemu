package athena

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/glue"
	"github.com/stackshy/cloudemu/v2/services/athena/driver"
	gluedriver "github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// newGlueMock builds an Athena mock backed by a Glue mock, as the AWS
// provider wires them.
func newGlueMock(t *testing.T) (*Mock, *glue.Mock) {
	t.Helper()

	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(1_700_000_000, 0))))
	g := glue.New(opts)
	m := New(opts)
	m.SetCatalog(g)

	return m, g
}

func runQuery(t *testing.T, m *Mock, in driver.StartQueryExecutionInput) *driver.QueryExecution {
	t.Helper()

	ctx := context.Background()

	if in.ResultConfiguration == nil && in.WorkGroup == "" {
		in.ResultConfiguration = &driver.ResultConfiguration{OutputLocation: "s3://out/"}
	}

	id, err := m.StartQueryExecution(ctx, in)
	requireNoError(t, err, "StartQueryExecution")

	qe, err := m.GetQueryExecution(ctx, id)
	requireNoError(t, err, "GetQueryExecution")

	return qe
}

func requireException(t *testing.T, err error, want string) {
	t.Helper()

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != want {
		t.Fatalf("error = %v, want %s", err, want)
	}
}

func TestGlueTablesShowInAwsDataCatalog(t *testing.T) {
	m, g := newGlueMock(t)
	ctx := context.Background()

	requireNoError(t, g.CreateDatabase(ctx, "", gluedriver.Database{Name: "sales", Description: "from glue"}), "glue CreateDatabase")
	requireNoError(t, g.CreateTable(ctx, "", "sales", gluedriver.Table{
		Name:      "orders",
		TableType: "EXTERNAL_TABLE",
		Parameters: map[string]string{
			"EXTERNAL": "TRUE",
		},
		StorageDescriptor: &gluedriver.StorageDescriptor{
			Columns:      []gluedriver.Column{{Name: "id", Type: "int"}, {Name: "note", Type: "string", Comment: "free text"}},
			Location:     "s3://data/orders/",
			InputFormat:  "org.apache.hadoop.mapred.TextInputFormat",
			OutputFormat: "org.apache.hadoop.hive.ql.io.HiveIgnoreKeyTextOutputFormat",
			SerdeInfo: &gluedriver.SerDeInfo{
				SerializationLibrary: "org.apache.hadoop.hive.serde2.lazy.LazySimpleSerDe",
				Parameters:           map[string]string{"field.delim": ","},
			},
		},
		PartitionKeys: []gluedriver.Column{{Name: "dt", Type: "string"}},
	}), "glue CreateTable")
	requireNoError(t, g.CreateTable(ctx, "", "sales", gluedriver.Table{Name: "returns"}), "glue CreateTable returns")

	dbs, _, err := m.ListDatabases(ctx, driver.DefaultDataCatalog, driver.Pagination{})
	requireNoError(t, err, "ListDatabases")

	if len(dbs) != 1 || dbs[0].Name != "sales" || dbs[0].Description != "from glue" {
		t.Fatalf("ListDatabases = %+v, want the Glue database", dbs)
	}

	db, err := m.GetDatabase(ctx, "", "sales")
	requireNoError(t, err, "GetDatabase")

	if db.Description != "from glue" {
		t.Fatalf("GetDatabase = %+v", db)
	}

	tables, _, err := m.ListTableMetadata(ctx, driver.DefaultDataCatalog, "sales", "", driver.Pagination{})
	requireNoError(t, err, "ListTableMetadata")

	if len(tables) != 2 || tables[0].Name != "orders" || tables[1].Name != "returns" {
		t.Fatalf("ListTableMetadata = %+v", tables)
	}

	filtered, _, err := m.ListTableMetadata(ctx, driver.DefaultDataCatalog, "sales", "ret*", driver.Pagination{})
	requireNoError(t, err, "ListTableMetadata filtered")

	if len(filtered) != 1 || filtered[0].Name != "returns" {
		t.Fatalf("filtered ListTableMetadata = %+v", filtered)
	}

	tm, err := m.GetTableMetadata(ctx, driver.DefaultDataCatalog, "sales", "orders")
	requireNoError(t, err, "GetTableMetadata")

	if tm.TableType != "EXTERNAL_TABLE" || len(tm.Columns) != 2 || tm.Columns[1].Comment != "free text" ||
		len(tm.PartitionKeys) != 1 || tm.PartitionKeys[0].Name != "dt" {
		t.Fatalf("GetTableMetadata = %+v", tm)
	}

	wantParams := map[string]string{
		"EXTERNAL":                "TRUE",
		"location":                "s3://data/orders/",
		"inputformat":             "org.apache.hadoop.mapred.TextInputFormat",
		"outputformat":            "org.apache.hadoop.hive.ql.io.HiveIgnoreKeyTextOutputFormat",
		"serde.serialization.lib": "org.apache.hadoop.hive.serde2.lazy.LazySimpleSerDe",
		"serde.param.field.delim": ",",
	}
	for k, v := range wantParams {
		if tm.Parameters[k] != v {
			t.Fatalf("Parameters[%q] = %q, want %q (all: %v)", k, tm.Parameters[k], v, tm.Parameters)
		}
	}

	_, err = m.GetTableMetadata(ctx, driver.DefaultDataCatalog, "sales", "ghost")
	requireException(t, err, driver.ExMetadata)

	_, _, err = m.ListTableMetadata(ctx, driver.DefaultDataCatalog, "nodb", "", driver.Pagination{})
	requireException(t, err, driver.ExMetadata)
}

func TestAthenaDatabaseDDLWritesGlue(t *testing.T) {
	m, g := newGlueMock(t)
	ctx := context.Background()

	qe := runQuery(t, m, driver.StartQueryExecutionInput{
		QueryString: "CREATE DATABASE IF NOT EXISTS Sales COMMENT 'it''s sales' LOCATION 's3://lake/sales/' " +
			"WITH DBPROPERTIES ('owner'='bi', 'tier' = 'gold');",
	})
	if qe.Status.State != driver.QueryStateSucceeded {
		t.Fatalf("CREATE state = %s (%s)", qe.Status.State, qe.Status.StateChangeReason)
	}

	db, err := g.GetDatabase(ctx, "", "sales")
	requireNoError(t, err, "glue GetDatabase after Athena DDL")

	if db.Description != "it's sales" || db.LocationURI != "s3://lake/sales/" ||
		db.Parameters["owner"] != "bi" || db.Parameters["tier"] != "gold" {
		t.Fatalf("glue database = %+v", db)
	}

	requireNoError(t, g.CreateTable(ctx, "", "sales", gluedriver.Table{Name: "orders"}), "glue CreateTable")

	qe = runQuery(t, m, driver.StartQueryExecutionInput{QueryString: "DROP DATABASE sales"})
	if qe.Status.State != driver.QueryStateFailed ||
		qe.Status.StateChangeReason != "InvalidOperationException: Database sales is not empty. One or more tables exist." {
		t.Fatalf("DROP non-empty = %s %q", qe.Status.State, qe.Status.StateChangeReason)
	}

	qe = runQuery(t, m, driver.StartQueryExecutionInput{QueryString: "DROP SCHEMA sales CASCADE"})
	if qe.Status.State != driver.QueryStateSucceeded {
		t.Fatalf("DROP CASCADE = %s %q", qe.Status.State, qe.Status.StateChangeReason)
	}

	if _, err := g.GetDatabase(ctx, "", "sales"); err == nil {
		t.Fatalf("glue database should be gone after DROP CASCADE")
	}

	qe = runQuery(t, m, driver.StartQueryExecutionInput{QueryString: "DROP DATABASE IF EXISTS sales"})
	if qe.Status.State != driver.QueryStateSucceeded {
		t.Fatalf("DROP IF EXISTS = %s %q", qe.Status.State, qe.Status.StateChangeReason)
	}
}

func TestDatabaseDDLFailureReasons(t *testing.T) {
	m, _ := newGlueMock(t)

	cases := []struct {
		name, query, catalog, want string
	}{
		{"missing name", "CREATE DATABASE", "", "SYNTAX_ERROR: database name is required"},
		{"trailing junk", "CREATE DATABASE d BOGUS", "", "SYNTAX_ERROR: unexpected token 'BOGUS'"},
		{"unknown catalog", "CREATE DATABASE d", "nope", "CATALOG_NOT_FOUND: Catalog 'nope' does not exist"},
		{"drop missing", "DROP DATABASE ghost", "", "Database does not exist: ghost"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			qe := runQuery(t, m, driver.StartQueryExecutionInput{
				QueryString:           tc.query,
				QueryExecutionContext: &driver.QueryExecutionContext{Catalog: tc.catalog},
			})
			if qe.Status.State != driver.QueryStateFailed || qe.Status.StateChangeReason != tc.want {
				t.Fatalf("got %s %q, want FAILED %q", qe.Status.State, qe.Status.StateChangeReason, tc.want)
			}
		})
	}
}

func TestUnwiredCatalogFallback(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	qe := runQuery(t, m, driver.StartQueryExecutionInput{QueryString: "CREATE DATABASE analytics COMMENT 'local'"})
	if qe.Status.State != driver.QueryStateSucceeded {
		t.Fatalf("CREATE = %s %q", qe.Status.State, qe.Status.StateChangeReason)
	}

	dbs, _, err := m.ListDatabases(ctx, "", driver.Pagination{})
	requireNoError(t, err, "ListDatabases")

	if len(dbs) != 1 || dbs[0].Name != "analytics" || dbs[0].Description != "local" {
		t.Fatalf("ListDatabases = %+v", dbs)
	}

	tables, _, err := m.ListTableMetadata(ctx, "", "analytics", "", driver.Pagination{})
	requireNoError(t, err, "ListTableMetadata")

	if len(tables) != 0 {
		t.Fatalf("fallback tables = %+v", tables)
	}

	_, err = m.GetTableMetadata(ctx, "", "analytics", "t")
	requireException(t, err, driver.ExMetadata)

	// The fallback store survives a snapshot round trip.
	raw, err := m.Snapshot(ctx, false)
	requireNoError(t, err, "Snapshot")

	restored := newMock(t)
	requireNoError(t, restored.Restore(ctx, raw), "Restore")

	db, err := restored.GetDatabase(ctx, "", "analytics")
	requireNoError(t, err, "GetDatabase after restore")

	if db.Description != "local" {
		t.Fatalf("restored database = %+v", db)
	}
}

// TestLegacySnapshotImportsIntoGlue restores a snapshot written before the
// Glue unification. Persist restores athena before glue, and the glue restore
// clears the store, so the import must happen after both.
func TestLegacySnapshotImportsIntoGlue(t *testing.T) {
	ctx := context.Background()

	raw, err := os.ReadFile("testdata/legacy_databases_snapshot.json")
	requireNoError(t, err, "read fixture")

	m, g := newGlueMock(t)
	requireNoError(t, m.Restore(ctx, raw), "athena Restore")
	requireNoError(t, g.Restore(ctx, []byte(`{}`)), "glue Restore")

	dbs, _, err := m.ListDatabases(ctx, "", driver.Pagination{})
	requireNoError(t, err, "ListDatabases")

	if len(dbs) != 2 || dbs[0].Name != "analytics" || dbs[1].Name != "sales" || dbs[1].Description != "legacy sales" {
		t.Fatalf("ListDatabases after legacy restore = %+v", dbs)
	}

	gdb, err := g.GetDatabase(ctx, "", "sales")
	requireNoError(t, err, "glue GetDatabase of imported database")

	if gdb.Parameters["owner"] != "bi" {
		t.Fatalf("imported glue database = %+v", gdb)
	}

	// Imported databases now live in Glue only. The one under an unknown
	// catalog stays pending so it is not lost.
	snap, err := m.Snapshot(ctx, false)
	requireNoError(t, err, "Snapshot")

	s := string(snap)
	if strings.Contains(s, "AwsDataCatalog/sales") || !strings.Contains(s, "ghostcatalog/orphan") {
		t.Fatalf("athena snapshot after import = %s", s)
	}
}

func TestLegacyImportKeepsExistingGlueDatabase(t *testing.T) {
	ctx := context.Background()

	raw, err := os.ReadFile("testdata/legacy_databases_snapshot.json")
	requireNoError(t, err, "read fixture")

	m, g := newGlueMock(t)
	requireNoError(t, m.Restore(ctx, raw), "athena Restore")
	requireNoError(t, g.CreateDatabase(ctx, "", gluedriver.Database{Name: "sales", Description: "glue wins"}), "glue CreateDatabase")

	db, err := m.GetDatabase(ctx, "", "sales")
	requireNoError(t, err, "GetDatabase")

	if db.Description != "glue wins" {
		t.Fatalf("GetDatabase = %+v, want the existing Glue database kept", db)
	}
}

// reentrantCatalog calls back into Athena from inside every catalog call, so
// a service lock held across the DDL and list seam deadlocks or trips -race.
type reentrantCatalog struct {
	Catalog

	m *Mock
}

func (r *reentrantCatalog) reenter(ctx context.Context) {
	_, _, _ = r.m.ListDatabases(ctx, "", driver.Pagination{})
	_, _, _ = r.m.ListQueryExecutions(ctx, "", driver.Pagination{})
}

//nolint:gocritic // hugeParam: matches the Catalog signature
func (r *reentrantCatalog) CreateDatabase(ctx context.Context, catalogID string, db gluedriver.Database) error {
	r.reenter(ctx)

	return r.Catalog.CreateDatabase(ctx, catalogID, db)
}

func (r *reentrantCatalog) GetDatabases(
	ctx context.Context, catalogID string, page gluedriver.TablePagination,
) ([]gluedriver.Database, string, error) {
	_, _, _ = r.m.ListQueryExecutions(ctx, "", driver.Pagination{})

	return r.Catalog.GetDatabases(ctx, catalogID, page)
}

func TestCatalogSeamReentrantConcurrent(t *testing.T) {
	ctx := context.Background()

	raw, err := os.ReadFile("testdata/legacy_databases_snapshot.json")
	requireNoError(t, err, "read fixture")

	m, g := newGlueMock(t)
	requireNoError(t, m.Restore(ctx, raw), "Restore")

	// The legacy import holds importMu across catalog calls and may not be
	// re-entered, so finish it before wiring the re-entrant catalog.
	_, _, err = m.ListDatabases(ctx, "", driver.Pagination{})
	requireNoError(t, err, "ListDatabases import")
	m.SetCatalog(&reentrantCatalog{Catalog: g, m: m})

	const workers = 8

	var wg sync.WaitGroup

	for i := range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			name := "db" + string(rune('a'+i))
			_, _ = m.StartQueryExecution(ctx, driver.StartQueryExecutionInput{
				QueryString:         "CREATE DATABASE " + name,
				ResultConfiguration: &driver.ResultConfiguration{OutputLocation: "s3://out/"},
			})
			_, _, _ = m.ListDatabases(ctx, "", driver.Pagination{})
			_, _ = m.Snapshot(ctx, false)
		}()
	}

	wg.Wait()

	dbs, _, err := m.ListDatabases(ctx, "", driver.Pagination{MaxResults: 50})
	requireNoError(t, err, "ListDatabases")

	if len(dbs) != workers+2 {
		t.Fatalf("databases = %d, want %d", len(dbs), workers+2)
	}
}

func TestResultConfigurationPrecedence(t *testing.T) {
	sse := &driver.EncryptionConfiguration{EncryptionOption: "SSE_S3"}
	kms := &driver.EncryptionConfiguration{EncryptionOption: "SSE_KMS", KmsKey: "arn:aws:kms:us-east-1:123456789012:key/k"}

	cases := []struct {
		name       string
		enforce    bool
		wgRC       *driver.ResultConfiguration
		clientRC   *driver.ResultConfiguration
		wantPrefix string
		wantEnc    string
		wantOwner  string
	}{
		{
			name: "enforced workgroup wins", enforce: true,
			wgRC:       &driver.ResultConfiguration{OutputLocation: "s3://wg/", EncryptionConfiguration: sse},
			clientRC:   &driver.ResultConfiguration{OutputLocation: "s3://client/", EncryptionConfiguration: kms},
			wantPrefix: "s3://wg/", wantEnc: "SSE_S3",
		},
		{
			name: "enforced ignores client fields the workgroup leaves unset", enforce: true,
			wgRC:       &driver.ResultConfiguration{OutputLocation: "s3://wg/"},
			clientRC:   &driver.ResultConfiguration{OutputLocation: "s3://client/", EncryptionConfiguration: kms, ExpectedBucketOwner: "111111111111"},
			wantPrefix: "s3://wg/",
		},
		{
			name: "not enforced client wins", enforce: false,
			wgRC:       &driver.ResultConfiguration{OutputLocation: "s3://wg/", EncryptionConfiguration: sse},
			clientRC:   &driver.ResultConfiguration{OutputLocation: "s3://client/", EncryptionConfiguration: kms},
			wantPrefix: "s3://client/", wantEnc: "SSE_KMS",
		},
		{
			name: "not enforced falls back to workgroup", enforce: false,
			wgRC:       &driver.ResultConfiguration{OutputLocation: "s3://wg/prefix", EncryptionConfiguration: sse},
			wantPrefix: "s3://wg/prefix/", wantEnc: "SSE_S3",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMock(t)
			ctx := context.Background()

			requireNoError(t, m.CreateWorkGroup(ctx, driver.WorkGroup{
				Name: "wg",
				Configuration: driver.WorkGroupConfiguration{
					EnforceWorkGroupConfiguration: ptr(tc.enforce),
					ResultConfiguration:           tc.wgRC,
				},
			}), "CreateWorkGroup")

			qe := runQuery(t, m, driver.StartQueryExecutionInput{
				QueryString: "SELECT 1", WorkGroup: "wg", ResultConfiguration: tc.clientRC,
			})

			rc := qe.ResultConfiguration
			if rc == nil || rc.OutputLocation != tc.wantPrefix+qe.QueryExecutionID+".csv" {
				t.Fatalf("OutputLocation = %+v, want %s<id>.csv", rc, tc.wantPrefix)
			}

			gotEnc := ""
			if rc.EncryptionConfiguration != nil {
				gotEnc = rc.EncryptionConfiguration.EncryptionOption
			}

			if gotEnc != tc.wantEnc {
				t.Fatalf("EncryptionConfiguration = %+v, want %q", rc.EncryptionConfiguration, tc.wantEnc)
			}

			if rc.ExpectedBucketOwner != tc.wantOwner {
				t.Fatalf("ExpectedBucketOwner = %q, want %q", rc.ExpectedBucketOwner, tc.wantOwner)
			}
		})
	}
}

func TestDDLResultObjectIsText(t *testing.T) {
	m := newMock(t)

	qe := runQuery(t, m, driver.StartQueryExecutionInput{QueryString: "CREATE DATABASE d"})

	if want := "s3://out/" + qe.QueryExecutionID + ".txt"; qe.ResultConfiguration.OutputLocation != want {
		t.Fatalf("DDL OutputLocation = %q, want %q", qe.ResultConfiguration.OutputLocation, want)
	}
}
