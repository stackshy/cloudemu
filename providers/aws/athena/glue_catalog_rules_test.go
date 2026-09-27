package athena

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/athena/driver"
	gluedriver "github.com/stackshy/cloudemu/v2/services/glue/driver"
)

func TestGetDatabaseMissingIsMetadataException(t *testing.T) {
	m, _ := newGlueMock(t)

	_, err := m.GetDatabase(context.Background(), "", "ghost")
	requireException(t, err, driver.ExMetadata)

	if got := err.Error(); got != "NotFound: Database ghost not found" {
		t.Fatalf("error = %q", got)
	}
}

func TestListTableMetadataExpressionIsRegex(t *testing.T) {
	m, g := newGlueMock(t)
	ctx := context.Background()

	requireNoError(t, g.CreateDatabase(ctx, "", gluedriver.Database{Name: "db"}), "CreateDatabase")

	for _, name := range []string{"sales", "sales_2024", "sales_2025", "returns"} {
		requireNoError(t, g.CreateTable(ctx, "", "db", gluedriver.Table{Name: name}), "CreateTable "+name)
	}

	cases := []struct {
		expr string
		want []string
	}{
		{"sales_.*", []string{"sales_2024", "sales_2025"}},
		{"SALES_2*", []string{"sales_2024", "sales_2025"}},
		{"sales_202[4]", []string{"sales_2024"}},
		{"ret*|sales", []string{"returns", "sales"}},
		{"*", []string{"returns", "sales", "sales_2024", "sales_2025"}},
	}

	for _, tc := range cases {
		got, _, err := m.ListTableMetadata(ctx, "", "db", tc.expr, driver.Pagination{})
		requireNoError(t, err, "ListTableMetadata "+tc.expr)

		names := make([]string, len(got))
		for i := range got {
			names[i] = got[i].Name
		}

		if len(names) != len(tc.want) {
			t.Fatalf("%q matched %v, want %v", tc.expr, names, tc.want)
		}

		for i := range names {
			if names[i] != tc.want[i] {
				t.Fatalf("%q matched %v, want %v", tc.expr, names, tc.want)
			}
		}
	}

	_, _, err := m.ListTableMetadata(ctx, "", "db", "sales_(", driver.Pagination{})
	requireException(t, err, driver.ExInvalidRequest)
}

func TestCatalogListMaxResultsRange(t *testing.T) {
	m, g := newGlueMock(t)
	ctx := context.Background()

	requireNoError(t, g.CreateDatabase(ctx, "", gluedriver.Database{Name: "db"}), "CreateDatabase")

	for _, n := range []int32{-1, 51} {
		_, _, err := m.ListDatabases(ctx, "", driver.Pagination{MaxResults: n})
		requireException(t, err, driver.ExInvalidRequest)

		_, _, err = m.ListTableMetadata(ctx, "", "db", "", driver.Pagination{MaxResults: n})
		requireException(t, err, driver.ExInvalidRequest)
	}

	for _, n := range []int32{0, 1, 50} {
		_, _, err := m.ListDatabases(ctx, "", driver.Pagination{MaxResults: n})
		requireNoError(t, err, "ListDatabases in range")

		_, _, err = m.ListTableMetadata(ctx, "", "db", "", driver.Pagination{MaxResults: n})
		requireNoError(t, err, "ListTableMetadata in range")
	}
}

func TestEnforcedWorkGroupWithoutLocationRejectsClientLocation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	requireNoError(t, m.CreateWorkGroup(ctx, driver.WorkGroup{
		Name:          "wg",
		Configuration: driver.WorkGroupConfiguration{EnforceWorkGroupConfiguration: ptr(true)},
	}), "CreateWorkGroup")

	_, err := m.StartQueryExecution(ctx, driver.StartQueryExecutionInput{
		QueryString:         "SELECT 1",
		WorkGroup:           "wg",
		ResultConfiguration: &driver.ResultConfiguration{OutputLocation: "s3://client/"},
	})
	requireException(t, err, driver.ExInvalidRequest)
}

func TestPrimaryWorkGroupDoesNotOverrideClient(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	wg, err := m.GetWorkGroup(ctx, driver.DefaultWorkGroup)
	requireNoError(t, err, "GetWorkGroup primary")

	if e := wg.Configuration.EnforceWorkGroupConfiguration; e == nil || *e {
		t.Fatalf("primary EnforceWorkGroupConfiguration = %v, want false", e)
	}

	qe := runQuery(t, m, driver.StartQueryExecutionInput{QueryString: "SELECT 1"})
	if want := "s3://out/" + qe.QueryExecutionID + ".csv"; qe.ResultConfiguration.OutputLocation != want {
		t.Fatalf("OutputLocation = %q, want %q", qe.ResultConfiguration.OutputLocation, want)
	}
}

// TestLegacySnapshotGlueRestoredFirst covers the reverse restore order.
func TestLegacySnapshotGlueRestoredFirst(t *testing.T) {
	ctx := context.Background()

	raw, err := os.ReadFile("testdata/legacy_databases_snapshot.json")
	requireNoError(t, err, "read fixture")

	m, g := newGlueMock(t)
	requireNoError(t, g.Restore(ctx, []byte(`{}`)), "glue Restore")
	requireNoError(t, m.Restore(ctx, raw), "athena Restore")

	dbs, _, err := m.ListDatabases(ctx, "", driver.Pagination{})
	requireNoError(t, err, "ListDatabases")

	if len(dbs) != 2 || dbs[0].Name != "analytics" || dbs[1].Name != "sales" {
		t.Fatalf("ListDatabases = %+v", dbs)
	}

	if _, err := g.GetDatabase(ctx, "", "sales"); err != nil {
		t.Fatalf("glue GetDatabase: %v", err)
	}
}

// blockingCatalog parks the first CreateDatabase until released.
type blockingCatalog struct {
	Catalog

	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

//nolint:gocritic // hugeParam: matches the Catalog signature
func (b *blockingCatalog) CreateDatabase(ctx context.Context, catalogID string, db gluedriver.Database) error {
	b.once.Do(func() {
		close(b.entered)
		<-b.release
	})

	return b.Catalog.CreateDatabase(ctx, catalogID, db)
}

// TestConcurrentCallerWaitsForLegacyImport checks that a caller arriving
// while the import runs sees the imported databases.
func TestConcurrentCallerWaitsForLegacyImport(t *testing.T) {
	ctx := context.Background()

	raw, err := os.ReadFile("testdata/legacy_databases_snapshot.json")
	requireNoError(t, err, "read fixture")

	m, g := newGlueMock(t)
	bc := &blockingCatalog{Catalog: g, entered: make(chan struct{}), release: make(chan struct{})}
	m.SetCatalog(bc)
	requireNoError(t, m.Restore(ctx, raw), "Restore")

	first := make(chan error, 1)

	go func() {
		_, _, err := m.ListDatabases(ctx, "", driver.Pagination{})
		first <- err
	}()

	<-bc.entered

	second := make(chan []driver.Database, 1)

	go func() {
		dbs, _, _ := m.ListDatabases(ctx, "", driver.Pagination{})
		second <- dbs
	}()

	// Give the second caller time to reach the import before releasing it.
	// Without the wait it would skip the import and list nothing.
	time.Sleep(50 * time.Millisecond)
	close(bc.release)
	requireNoError(t, <-first, "first ListDatabases")

	if dbs := <-second; len(dbs) != 2 {
		t.Fatalf("concurrent ListDatabases = %+v, want both imported databases", dbs)
	}
}
