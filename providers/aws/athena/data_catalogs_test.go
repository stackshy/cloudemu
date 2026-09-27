package athena

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/athena/driver"
	gluedriver "github.com/stackshy/cloudemu/v2/services/glue/driver"
)

const testLambdaARN = "arn:aws:lambda:us-east-1:123456789012:function:conn"

func TestCreateDataCatalogEachType(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cases := []driver.CreateDataCatalogInput{
		{Name: "lam", Type: "LAMBDA", Parameters: map[string]string{"function": testLambdaARN}},
		{Name: "lam_pair", Type: "LAMBDA", Parameters: map[string]string{
			"metadata-function": testLambdaARN, "record-function": testLambdaARN,
		}},
		{Name: "glue-own", Type: "GLUE", Parameters: map[string]string{"catalog-id": m.opts.AccountID}},
		{Name: "hive@x", Type: "HIVE", Parameters: map[string]string{"metadata-function": testLambdaARN, "sdk-version": "1.0"}},
		{Name: "fed", Type: "FEDERATED", Parameters: map[string]string{"connection-type": "MYSQL", "connection-properties": "{}"}},
	}

	for _, in := range cases {
		dc, err := m.CreateDataCatalog(ctx, in)
		requireNoError(t, err, "create "+in.Name)

		if dc.Status != driver.DataCatalogStatusCreateComplete {
			t.Fatalf("%s status = %q", in.Name, dc.Status)
		}

		got, err := m.GetDataCatalog(ctx, in.Name)
		requireNoError(t, err, "get "+in.Name)

		if got.Type != in.Type || len(got.Parameters) != len(in.Parameters) {
			t.Fatalf("%s = %+v", in.Name, got)
		}
	}

	fed, _ := m.GetDataCatalog(ctx, "fed")
	if fed.ConnectionType != "MYSQL" {
		t.Fatalf("fed ConnectionType = %q", fed.ConnectionType)
	}

	tags, _ := m.ListTagsForResource(ctx, m.dataCatalogARN("fed"))
	if tags[federatedTagKey] != "true" {
		t.Fatalf("fed tags = %v", tags)
	}

	list, _, err := m.ListDataCatalogs(ctx, driver.Pagination{})
	requireNoError(t, err, "list")

	if len(list) != len(cases)+1 || list[0].CatalogName != driver.DefaultDataCatalog {
		t.Fatalf("list = %+v", list)
	}
}

func TestCreateDataCatalogValidation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	fn := map[string]string{"function": testLambdaARN}
	cases := map[string]driver.CreateDataCatalogInput{
		"lambda both forms": {Name: "a", Type: "LAMBDA", Parameters: map[string]string{
			"function": testLambdaARN, "metadata-function": testLambdaARN, "record-function": testLambdaARN,
		}},
		"lambda half pair": {Name: "a", Type: "LAMBDA", Parameters: map[string]string{"metadata-function": testLambdaARN}},
		"lambda none":      {Name: "a", Type: "LAMBDA"},
		"hive no metadata": {Name: "a", Type: "HIVE", Parameters: map[string]string{"record-function": testLambdaARN}},
		"glue no id":       {Name: "a", Type: "GLUE"},
		"federated both": {Name: "a", Type: "FEDERATED", Parameters: map[string]string{
			"connection-arn": "arn:aws:glue:us-east-1:123456789012:connection/c", "connection-type": "MYSQL",
		}},
		"federated none":     {Name: "a", Type: "FEDERATED"},
		"federated bad type": {Name: "a", Type: "FEDERATED", Parameters: map[string]string{"connection-type": "NOPE"}},
		"federated long": {Name: strings.Repeat("f", 42), Type: "FEDERATED", Parameters: map[string]string{
			"connection-type": "MYSQL", "connection-properties": "{}",
		}},
		"federated no properties": {Name: "a", Type: "FEDERATED", Parameters: map[string]string{"connection-type": "MYSQL"}},
		"backslash not federated": {Name: `a\b`, Type: "LAMBDA", Parameters: fn},
		"bad type":                {Name: "a", Type: "S3", Parameters: fn},
		"no name":                 {Type: "LAMBDA", Parameters: fn},
		"bad name":                {Name: "a b", Type: "LAMBDA", Parameters: fn},
		"long name":               {Name: strings.Repeat("n", 128), Type: "LAMBDA", Parameters: fn},
		"long description":        {Name: "a", Type: "LAMBDA", Parameters: fn, Description: strings.Repeat("d", 1025)},
		"default name":            {Name: "awsdatacatalog", Type: "GLUE", Parameters: map[string]string{"catalog-id": "1"}},
	}

	for name, in := range cases {
		_, err := m.CreateDataCatalog(ctx, in)
		requireException(t, err, driver.ExInvalidRequest)

		if _, err := m.GetDataCatalog(ctx, "a"); err == nil {
			t.Fatalf("%s: catalog was stored", name)
		}
	}

	_, err := m.CreateDataCatalog(ctx, driver.CreateDataCatalogInput{Name: "dup", Type: "LAMBDA", Parameters: fn})
	requireNoError(t, err, "create dup")

	_, err = m.CreateDataCatalog(ctx, driver.CreateDataCatalogInput{Name: "dup", Type: "LAMBDA", Parameters: fn})
	requireException(t, err, driver.ExInvalidRequest)
}

func TestGetDataCatalogMissingWasNotFound(t *testing.T) {
	m := newMock(t)

	_, err := m.GetDataCatalog(context.Background(), "ghost")
	requireException(t, err, driver.ExInvalidRequest)

	if !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("error = %v", err)
	}

	dc, err := m.GetDataCatalog(context.Background(), driver.DefaultDataCatalog)
	requireNoError(t, err, "get default")

	if dc.Parameters == nil {
		t.Fatal("AwsDataCatalog Parameters is nil")
	}
}

func TestUpdateDataCatalogKeepsAndReplaces(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	_, err := m.CreateDataCatalog(ctx, driver.CreateDataCatalogInput{
		Name: "lam", Type: "LAMBDA", Description: "one", Parameters: map[string]string{"function": testLambdaARN},
	})
	requireNoError(t, err, "create")

	err = m.UpdateDataCatalog(ctx, driver.UpdateDataCatalogInput{Name: "lam", Type: "LAMBDA", Description: ptr("two")})
	requireNoError(t, err, "update description")

	dc, _ := m.GetDataCatalog(ctx, "lam")
	if dc.Description != "two" || dc.Parameters["function"] != testLambdaARN {
		t.Fatalf("after description update = %+v", dc)
	}

	err = m.UpdateDataCatalog(ctx, driver.UpdateDataCatalogInput{
		Name: "lam", Type: "LAMBDA", Parameters: map[string]string{"metadata-function": "m", "record-function": "r"},
	})
	requireNoError(t, err, "update parameters")

	dc, _ = m.GetDataCatalog(ctx, "lam")
	if dc.Description != "two" || dc.Parameters["function"] != "" || dc.Parameters["record-function"] != "r" {
		t.Fatalf("after parameter update = %+v", dc)
	}

	// Switching to GLUE without a catalog-id fails and changes nothing.
	err = m.UpdateDataCatalog(ctx, driver.UpdateDataCatalogInput{Name: "lam", Type: "GLUE"})
	requireException(t, err, driver.ExInvalidRequest)

	dc, _ = m.GetDataCatalog(ctx, "lam")
	if dc.Type != "LAMBDA" {
		t.Fatalf("type changed to %q", dc.Type)
	}

	err = m.UpdateDataCatalog(ctx, driver.UpdateDataCatalogInput{
		Name: "lam", Type: "GLUE", Parameters: map[string]string{"catalog-id": m.opts.AccountID},
	})
	requireNoError(t, err, "switch to GLUE")

	for _, in := range []driver.UpdateDataCatalogInput{
		{Name: driver.DefaultDataCatalog, Type: "GLUE", Parameters: map[string]string{"catalog-id": "1"}},
		{Name: "ghost", Type: "LAMBDA", Parameters: map[string]string{"function": "f"}},
		{Name: "lam", Type: "FEDERATED", Parameters: map[string]string{"connection-type": "MYSQL"}},
	} {
		requireException(t, m.UpdateDataCatalog(ctx, in), driver.ExInvalidRequest)
	}
}

func TestDeleteDataCatalogRules(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	_, err := m.DeleteDataCatalog(ctx, driver.DefaultDataCatalog, false)
	requireException(t, err, driver.ExInvalidRequest)

	_, err = m.DeleteDataCatalog(ctx, "ghost", false)
	requireException(t, err, driver.ExInvalidRequest)

	_, err = m.CreateDataCatalog(ctx, driver.CreateDataCatalogInput{
		Name: "lam", Type: "LAMBDA", Parameters: map[string]string{"function": testLambdaARN},
		Tags: map[string]string{"env": "dev"},
	})
	requireNoError(t, err, "create")

	_, err = m.DeleteDataCatalog(ctx, "lam", true)
	requireException(t, err, driver.ExInvalidRequest)

	dc, err := m.DeleteDataCatalog(ctx, "lam", false)
	requireNoError(t, err, "delete")

	if dc.Name != "lam" {
		t.Fatalf("deleted = %+v", dc)
	}

	if tags, _ := m.ListTagsForResource(ctx, m.dataCatalogARN("lam")); len(tags) != 0 {
		t.Fatalf("tags left behind: %v", tags)
	}

	_, err = m.CreateDataCatalog(ctx, driver.CreateDataCatalogInput{
		Name: "fed", Type: "FEDERATED", Parameters: map[string]string{"connection-type": "MYSQL", "connection-properties": "{}"},
	})
	requireNoError(t, err, "create fed")

	dc, err = m.DeleteDataCatalog(ctx, "fed", true)
	requireNoError(t, err, "delete fed")

	if dc.Status != driver.DataCatalogStatusDeleteComplete {
		t.Fatalf("fed delete status = %q", dc.Status)
	}
}

func TestFederatedConnectionARNResolvesViaGlue(t *testing.T) {
	m, g := newGlueMock(t)
	ctx := context.Background()

	requireNoError(t, g.CreateConnection(ctx, "", gluedriver.Connection{Name: "ddb", ConnectionType: "DYNAMODB"}), "glue connection")

	ok, err := m.CreateDataCatalog(ctx, driver.CreateDataCatalogInput{
		Name: "fed_ok", Type: "FEDERATED",
		Parameters: map[string]string{"connection-arn": "arn:aws:glue:us-east-1:123456789012:connection/ddb"},
	})
	requireNoError(t, err, "create fed_ok")

	if ok.Status != driver.DataCatalogStatusCreateComplete || ok.ConnectionType != "DYNAMODB" || ok.Error != "" {
		t.Fatalf("fed_ok = %+v", ok)
	}

	bad, err := m.CreateDataCatalog(ctx, driver.CreateDataCatalogInput{
		Name: "fed_bad", Type: "FEDERATED",
		Parameters: map[string]string{"connection-arn": "arn:aws:glue:us-east-1:123456789012:connection/ghost"},
	})
	requireNoError(t, err, "create fed_bad")

	if bad.Status != driver.DataCatalogStatusCreateFailed || !strings.Contains(bad.Error, "ghost") {
		t.Fatalf("fed_bad = %+v", bad)
	}

	list, _, _ := m.ListDataCatalogs(ctx, driver.Pagination{})
	for _, s := range list {
		if s.CatalogName == "fed_bad" && s.Status != driver.DataCatalogStatusCreateFailed {
			t.Fatalf("summary = %+v", s)
		}
	}
}

func TestQueryOnConnectorCatalogFailsLoudly(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	_, err := m.CreateDataCatalog(ctx, driver.CreateDataCatalogInput{
		Name: "lam", Type: "LAMBDA", Parameters: map[string]string{"function": testLambdaARN},
	})
	requireNoError(t, err, "create")

	qe := runQuery(t, m, driver.StartQueryExecutionInput{
		QueryString:           "SELECT 1",
		QueryExecutionContext: &driver.QueryExecutionContext{Catalog: "lam"},
	})

	if qe.Status.State != driver.QueryStateFailed || qe.Status.StateChangeReason != connectorNotSupported {
		t.Fatalf("status = %+v", qe.Status)
	}

	_, _, err = m.ListDatabases(ctx, "lam", driver.Pagination{})
	requireException(t, err, driver.ExInvalidRequest)
}

func TestGlueTypeCatalogResolvesViaGlue(t *testing.T) {
	m, g := newGlueMock(t)
	ctx := context.Background()

	requireNoError(t, g.CreateDatabase(ctx, "", gluedriver.Database{Name: "lake"}), "glue db")

	_, err := m.CreateDataCatalog(ctx, driver.CreateDataCatalogInput{
		Name: "mirror", Type: "GLUE", Parameters: map[string]string{"catalog-id": m.opts.AccountID},
	})
	requireNoError(t, err, "create mirror")

	dbs, _, err := m.ListDatabases(ctx, "mirror", driver.Pagination{})
	requireNoError(t, err, "list mirror")

	if len(dbs) != 1 || dbs[0].Name != "lake" {
		t.Fatalf("mirror dbs = %+v", dbs)
	}

	_, err = m.CreateDataCatalog(ctx, driver.CreateDataCatalogInput{
		Name: "other", Type: "GLUE", Parameters: map[string]string{"catalog-id": "999999999999"},
	})
	requireNoError(t, err, "create other")

	_, _, err = m.ListDatabases(ctx, "other", driver.Pagination{})
	requireException(t, err, driver.ExInvalidRequest)
}

func TestListDataCatalogsMaxResults(t *testing.T) {
	m := newMock(t)

	for _, n := range []int32{1, 51} {
		_, _, err := m.ListDataCatalogs(context.Background(), driver.Pagination{MaxResults: n})
		requireException(t, err, driver.ExInvalidRequest)
	}
}

func TestDataCatalogSnapshotRoundTrip(t *testing.T) {
	m, _ := newGlueMock(t)
	ctx := context.Background()

	_, err := m.CreateDataCatalog(ctx, driver.CreateDataCatalogInput{
		Name: "fed", Type: "FEDERATED",
		Parameters: map[string]string{"connection-arn": "arn:aws:glue:us-east-1:123456789012:connection/ghost"},
		Tags:       map[string]string{"env": "dev"},
	})
	requireNoError(t, err, "create")

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err, "snapshot")

	fresh := newMock(t)
	requireNoError(t, fresh.Restore(ctx, data), "restore")

	dc, err := fresh.GetDataCatalog(ctx, "fed")
	requireNoError(t, err, "get restored")

	if dc.Status != driver.DataCatalogStatusCreateFailed || dc.Error == "" {
		t.Fatalf("restored = %+v", dc)
	}

	tags, _ := fresh.ListTagsForResource(ctx, fresh.dataCatalogARN("fed"))
	if tags["env"] != "dev" {
		t.Fatalf("restored tags = %v", tags)
	}
}

func TestFederatedNameAllowsBackslash(t *testing.T) {
	m := newMock(t)

	dc, err := m.CreateDataCatalog(context.Background(), driver.CreateDataCatalogInput{
		Name: `fed\x`, Type: "FEDERATED",
		Parameters: map[string]string{"connection-type": "MYSQL", "connection-properties": "{}"},
	})
	requireNoError(t, err, "create")

	if dc.Name != `fed\x` {
		t.Fatalf("name = %q", dc.Name)
	}
}

func TestCatalogOpsMissingCatalogWasNotFound(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	_, _, err := m.ListDatabases(ctx, "ghost", driver.Pagination{})
	requireException(t, err, driver.ExInvalidRequest)

	if !strings.Contains(err.Error(), "Catalog ghost was not found") {
		t.Fatalf("ListDatabases error = %v", err)
	}

	_, err = m.GetTableMetadata(ctx, "ghost", "db", "t")
	if err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("GetTableMetadata error = %v", err)
	}
}

// TestCreateDeleteDataCatalogRaceLeavesNoTags runs Create and Delete of one
// name side by side. Once both finish, tags may exist only if the catalog does.
func TestCreateDeleteDataCatalogRaceLeavesNoTags(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	for i := 0; i < 200; i++ {
		done := make(chan struct{})

		go func() {
			defer close(done)

			_, _ = m.CreateDataCatalog(ctx, driver.CreateDataCatalogInput{
				Name: "race", Type: "LAMBDA", Parameters: map[string]string{"function": testLambdaARN},
				Tags: map[string]string{"k": "v"},
			})
		}()

		_, _ = m.DeleteDataCatalog(ctx, "race", false)
		<-done

		_, getErr := m.GetDataCatalog(ctx, "race")
		tags, _ := m.ListTagsForResource(ctx, m.dataCatalogARN("race"))

		if getErr != nil && len(tags) != 0 {
			t.Fatalf("iteration %d: orphan tags %v", i, tags)
		}

		_, _ = m.DeleteDataCatalog(ctx, "race", false)
	}
}
