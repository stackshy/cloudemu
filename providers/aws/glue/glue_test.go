package glue_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	glueprovider "github.com/stackshy/cloudemu/v2/providers/aws/glue"
	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

func newMock() *glueprovider.Mock {
	return glueprovider.New(config.NewOptions(
		config.WithAccountID("123456789012"),
		config.WithRegion("us-east-1"),
	))
}

func TestDatabaseCRUD(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if err := m.CreateDatabase(ctx, "", driver.Database{Name: "sales", Description: "d"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	// Duplicate create must be AlreadyExistsException.
	err := m.CreateDatabase(ctx, "", driver.Database{Name: "sales"})
	if !isException(err, driver.ExAlreadyExists) {
		t.Fatalf("duplicate create = %v, want AlreadyExistsException", err)
	}

	db, err := m.GetDatabase(ctx, "", "sales")
	if err != nil || db.Name != "sales" {
		t.Fatalf("GetDatabase: %v %+v", err, db)
	}

	// Catalog ID must default to the account ID.
	if db.CatalogID != "123456789012" {
		t.Fatalf("CatalogID = %q, want account id", db.CatalogID)
	}

	if err := m.DeleteDatabase(ctx, "", "sales"); err != nil {
		t.Fatalf("DeleteDatabase: %v", err)
	}

	if _, err := m.GetDatabase(ctx, "", "sales"); !isException(err, driver.ExEntityNotFound) {
		t.Fatalf("Get after delete = %v, want EntityNotFoundException", err)
	}
}

func TestTablePartitionLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if err := m.CreateDatabase(ctx, "", driver.Database{Name: "db"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	// A table under a missing database is EntityNotFound.
	if err := m.CreateTable(ctx, "", "missing", driver.Table{Name: "t"}); !isException(err, driver.ExEntityNotFound) {
		t.Fatalf("table under missing db = %v", err)
	}

	tbl := driver.Table{
		Name: "events",
		StorageDescriptor: &driver.StorageDescriptor{
			Columns: []driver.Column{{Name: "id", Type: "string"}},
		},
		PartitionKeys: []driver.Column{{Name: "dt", Type: "string"}},
	}
	if err := m.CreateTable(ctx, "", "db", tbl); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	// UpdateTable appends a version.
	if err := m.UpdateTable(ctx, "", "db", tbl); err != nil {
		t.Fatalf("UpdateTable: %v", err)
	}

	vers, _, err := m.GetTableVersions(ctx, "", "db", "events", driver.TablePagination{})
	if err != nil || len(vers) != 2 {
		t.Fatalf("GetTableVersions = %d versions, %v", len(vers), err)
	}

	// Partition CRUD.
	if err := m.CreatePartition(ctx, "", "db", "events", driver.Partition{Values: []string{"2024-01-01"}}); err != nil {
		t.Fatalf("CreatePartition: %v", err)
	}

	p, err := m.GetPartition(ctx, "", "db", "events", []string{"2024-01-01"})
	if err != nil || len(p.Values) != 1 {
		t.Fatalf("GetPartition: %v %+v", err, p)
	}

	// Deleting the database must cascade to tables and partitions.
	if err := m.DeleteDatabase(ctx, "", "db"); err != nil {
		t.Fatalf("DeleteDatabase: %v", err)
	}

	if _, err := m.GetTable(ctx, "", "db", "events"); !isException(err, driver.ExEntityNotFound) {
		t.Fatalf("table after db delete = %v", err)
	}
}

func TestJobRunCompletesSucceeded(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, err := m.CreateJob(ctx, driver.Job{Name: "etl", Role: "r"}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	runID, err := m.StartJobRun(ctx, "etl", map[string]string{"--k": "v"})
	if err != nil {
		t.Fatalf("StartJobRun: %v", err)
	}

	run, err := m.GetJobRun(ctx, "etl", runID)
	if err != nil {
		t.Fatalf("GetJobRun: %v", err)
	}

	if run.JobRunState != driver.JobRunSucceeded {
		t.Fatalf("run state = %q, want SUCCEEDED", run.JobRunState)
	}
}

func TestCrawlerLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if err := m.CreateCrawler(ctx, driver.Crawler{Name: "c", Role: "r", DatabaseName: "db"}); err != nil {
		t.Fatalf("CreateCrawler: %v", err)
	}

	if err := m.StartCrawler(ctx, "c"); err != nil {
		t.Fatalf("StartCrawler: %v", err)
	}

	c, err := m.GetCrawler(ctx, "c")
	if err != nil || c.LastCrawlStatus != driver.JobRunSucceeded {
		t.Fatalf("GetCrawler after start: %v %+v", err, c)
	}
}

func TestSchemaRegistry(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, err := m.CreateRegistry(ctx, driver.Registry{Name: "reg"}); err != nil {
		t.Fatalf("CreateRegistry: %v", err)
	}

	if _, err := m.CreateSchema(ctx, driver.Schema{RegistryName: "reg", Name: "s", DataFormat: "AVRO"}, `{"a":1}`); err != nil {
		t.Fatalf("CreateSchema: %v", err)
	}

	v, err := m.RegisterSchemaVersion(ctx, "reg", "s", `{"a":2}`)
	if err != nil || v.VersionNumber != 2 {
		t.Fatalf("RegisterSchemaVersion = %+v %v", v, err)
	}
}

func TestTagCapEnforcedBeforeMutation(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	arn := "arn:aws:glue:us-east-1:123456789012:database/db"
	tags := map[string]string{}

	for i := 0; i < 60; i++ {
		tags[fmt.Sprintf("k%d", i)] = "v"
	}

	if err := m.TagResource(ctx, arn, tags); !isException(err, driver.ExResourceNumberLimit) {
		t.Fatalf("over-cap tag = %v, want ResourceNumberLimitExceededException", err)
	}

	// No tag should have been committed.
	got, _ := m.GetTags(ctx, arn)
	if len(got) != 0 {
		t.Fatalf("tags committed despite cap breach: %d", len(got))
	}
}

func TestPaginationBadTokenRejected(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.GetDatabases(ctx, "", driver.TablePagination{NextToken: "!!!not-base64"}); err == nil {
		t.Fatal("bad NextToken accepted, want InvalidInputException")
	}
}

// TestConcurrentCreateNoDuplicate runs many concurrent creates of the same
// database name; exactly one must win. Run under -race to catch data races.
func TestConcurrentCreateNoDuplicate(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	const n = 50

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		success int
	)

	for i := 0; i < n; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if err := m.CreateDatabase(ctx, "", driver.Database{Name: "race"}); err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if success != 1 {
		t.Fatalf("concurrent create winners = %d, want 1", success)
	}
}

// TestGetDoesNotAliasStore mutates the map/slice returned by a Get and confirms
// the stored value is unaffected (deep-copy reads). Run under -race too.
func TestGetDoesNotAliasStore(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if err := m.CreateDatabase(ctx, "", driver.Database{
		Name: "db", Parameters: map[string]string{"a": "1"},
	}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	got, err := m.GetDatabase(ctx, "", "db")
	if err != nil {
		t.Fatalf("GetDatabase: %v", err)
	}

	got.Parameters["a"] = "mutated"
	got.Parameters["b"] = "added"

	again, err := m.GetDatabase(ctx, "", "db")
	if err != nil {
		t.Fatalf("GetDatabase: %v", err)
	}

	if again.Parameters["a"] != "1" || len(again.Parameters) != 1 {
		t.Fatalf("store aliased by returned copy: %+v", again.Parameters)
	}
}

func TestGetTableDoesNotAliasNested(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if err := m.CreateDatabase(ctx, "", driver.Database{Name: "db"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	if err := m.CreateTable(ctx, "", "db", driver.Table{
		Name: "t",
		StorageDescriptor: &driver.StorageDescriptor{
			Columns: []driver.Column{{Name: "id", Type: "string"}},
		},
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	got, err := m.GetTable(ctx, "", "db", "t")
	if err != nil {
		t.Fatalf("GetTable: %v", err)
	}

	// Mutate the returned nested slice; the store must not observe it.
	got.StorageDescriptor.Columns[0].Name = "mutated"
	got.StorageDescriptor.Columns = append(got.StorageDescriptor.Columns, driver.Column{Name: "extra"})

	again, err := m.GetTable(ctx, "", "db", "t")
	if err != nil {
		t.Fatalf("GetTable: %v", err)
	}

	if len(again.StorageDescriptor.Columns) != 1 || again.StorageDescriptor.Columns[0].Name != "id" {
		t.Fatalf("nested StorageDescriptor.Columns aliased by returned copy: %+v", again.StorageDescriptor.Columns)
	}
}

func TestCreateConnectionTypeValidation(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	// Types modeled by the SDK enum must be accepted.
	for _, ct := range []string{"MYSQL", "POSTGRESQL", "ORACLE", "BIGQUERY", "JDBC"} {
		err := m.CreateConnection(ctx, "", driver.Connection{Name: "c-" + ct, ConnectionType: ct})
		if err != nil {
			t.Fatalf("CreateConnection %s = %v, want success", ct, err)
		}
	}

	// A type outside the SDK enum must be rejected.
	err := m.CreateConnection(ctx, "", driver.Connection{Name: "bad", ConnectionType: "SNOWFLAKE"})
	if !isException(err, driver.ExInvalidInput) {
		t.Fatalf("CreateConnection SNOWFLAKE = %v, want InvalidInputException", err)
	}
}

func TestCreateTriggerConditionalRequiresPredicate(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	// CONDITIONAL without a Predicate must be rejected.
	_, err := m.CreateTrigger(ctx, driver.Trigger{Name: "t1", Type: "CONDITIONAL"})
	if !isException(err, driver.ExInvalidInput) {
		t.Fatalf("CONDITIONAL without Predicate = %v, want InvalidInputException", err)
	}

	// CONDITIONAL with a Predicate must succeed.
	_, err = m.CreateTrigger(ctx, driver.Trigger{
		Name: "t2", Type: "CONDITIONAL",
		Predicate: map[string]any{"Logical": "ANY"},
	})
	if err != nil {
		t.Fatalf("CONDITIONAL with Predicate = %v, want success", err)
	}
}

func TestDeleteSchemaVersionsReportsFailingVersion(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, err := m.CreateSchema(ctx, driver.Schema{Name: "s", DataFormat: "AVRO"}, "def-v1"); err != nil {
		t.Fatalf("CreateSchema: %v", err)
	}

	// Version 5 does not exist; the error entry must carry that number in Values.
	errs, err := m.DeleteSchemaVersions(ctx, "", "s", "5")
	if err != nil {
		t.Fatalf("DeleteSchemaVersions: %v", err)
	}

	if len(errs) != 1 || len(errs[0].Values) == 0 || errs[0].Values[0] != "5" {
		t.Fatalf("DeleteSchemaVersions errors = %+v, want one entry with Values[0]==5", errs)
	}
}

func isException(err error, want string) bool {
	var apiErr *driver.APIError
	if err == nil {
		return false
	}

	for e := err; e != nil; {
		if ae, ok := e.(*driver.APIError); ok {
			apiErr = ae

			break
		}

		type unwrapper interface{ Unwrap() error }
		if u, ok := e.(unwrapper); ok {
			e = u.Unwrap()
		} else {
			break
		}
	}

	return apiErr != nil && apiErr.Exception == want
}
