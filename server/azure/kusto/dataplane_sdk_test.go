package kusto_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-kusto-go/kusto"
	"github.com/Azure/azure-kusto-go/kusto/data/errors"
	"github.com/Azure/azure-kusto-go/kusto/data/table"
)

const dataDB = "telemetry"

// testUserToken mints an unsigned but well-formed Azure AD JWT whose audience
// and object-id claims satisfy CloudEmu's Azure auth gate (which verifies token
// structure and claims, not the signature). The azure-kusto-go client sends it
// verbatim as the Bearer token via WitAadUserToken.
func testUserToken(t *testing.T) string {
	t.Helper()

	seg := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal token segment: %v", err)
		}

		return base64.RawURLEncoding.EncodeToString(b)
	}

	header := seg(map[string]any{"alg": "none", "typ": "JWT"})
	payload := seg(map[string]any{
		"aud": "https://kusto.kusto.windows.net",
		"oid": "11111111-1111-1111-1111-111111111111",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	return header + "." + payload + "." + base64.RawURLEncoding.EncodeToString([]byte("sig"))
}

// newDataClient builds an azure-kusto-go client pointed at the TLS httptest
// server, trusting its self-signed cert via WithHttpClient(ts.Client()).
func newDataClient(t *testing.T, ts *httptest.Server) *kusto.Client {
	t.Helper()

	kcsb := kusto.NewConnectionStringBuilder(ts.URL).WitAadUserToken(testUserToken(t))

	client, err := kusto.New(kcsb, kusto.WithHttpClient(ts.Client()))
	if err != nil {
		t.Fatalf("kusto.New: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

// TestSDKKustoDataPlaneMgmt drives the Kusto data plane with the real
// azure-kusto-go client's Mgmt() path: create a table, list tables, read its CSL
// schema, then drop it and confirm it is gone.
func TestSDKKustoDataPlaneMgmt(t *testing.T) {
	ts := newServer(t)
	client := newDataClient(t, ts)
	ctx := context.Background()

	createIter, err := client.Mgmt(ctx, dataDB,
		kusto.NewStmt(".create table Events (id:long, name:string, ts:datetime)"))
	cols, rows := collectRows(t, createIter, err)

	if len(rows) != 1 || rows[0][colIndex(cols, "TableName")] != "Events" {
		t.Fatalf(".create table rows = %v (cols %v), want one row for Events", rows, cols)
	}

	if got := rows[0][colIndex(cols, "Schema")]; !strings.Contains(got, "id:long") {
		t.Fatalf(".create table Schema = %q, want it to contain id:long", got)
	}

	assertTableListed(t, client, ctx, "Events")

	schemaIter, err := client.Mgmt(ctx, dataDB, kusto.NewStmt(".show table Events cslschema"))
	cols, rows = collectRows(t, schemaIter, err)

	if len(rows) != 1 {
		t.Fatalf(".show table cslschema rows = %v, want one", rows)
	}

	if got := rows[0][colIndex(cols, "Schema")]; got != "id:long,name:string,ts:datetime" {
		t.Fatalf(".show table cslschema Schema = %q, want id:long,name:string,ts:datetime", got)
	}

	dropIter, err := client.Mgmt(ctx, dataDB, kusto.NewStmt(".drop table Events"))
	collectRows(t, dropIter, err)

	assertNoTables(t, client, ctx)
}

// TestSDKKustoQueryNotImplemented confirms the query endpoints are registered
// and answer with a clean error while the KQL engine is not yet wired.
func TestSDKKustoQueryNotImplemented(t *testing.T) {
	ts := newServer(t)
	client := newDataClient(t, ts)

	if _, err := client.Query(context.Background(), dataDB, kusto.NewStmt("Events | count")); err == nil {
		t.Fatal("Query returned nil error, want a not-implemented error")
	}
}

func assertTableListed(t *testing.T, client *kusto.Client, ctx context.Context, want string) {
	t.Helper()

	iter, err := client.Mgmt(ctx, dataDB, kusto.NewStmt(".show tables"))
	cols, rows := collectRows(t, iter, err)

	idx := colIndex(cols, "TableName")
	for _, r := range rows {
		if r[idx] == want {
			return
		}
	}

	t.Fatalf(".show tables = %v, want it to contain %q", rows, want)
}

func assertNoTables(t *testing.T, client *kusto.Client, ctx context.Context) {
	t.Helper()

	iter, err := client.Mgmt(ctx, dataDB, kusto.NewStmt(".show tables"))
	_, rows := collectRows(t, iter, err)

	if len(rows) != 0 {
		t.Fatalf(".show tables after drop = %v, want empty", rows)
	}
}

// collectRows drains a mgmt RowIterator into column names and stringified rows.
func collectRows(t *testing.T, iter *kusto.RowIterator, err error) ([]string, [][]string) {
	t.Helper()

	if err != nil {
		t.Fatalf("Mgmt: %v", err)
	}

	var (
		cols []string
		rows [][]string
	)

	doErr := iter.DoOnRowOrError(func(r *table.Row, inlineErr *errors.Error) error {
		if inlineErr != nil {
			return inlineErr
		}

		if cols == nil {
			for _, c := range r.ColumnTypes {
				cols = append(cols, c.Name)
			}
		}

		row := make([]string, len(r.Values))
		for i, v := range r.Values {
			row[i] = v.String()
		}

		rows = append(rows, row)

		return nil
	})
	if doErr != nil {
		t.Fatalf("iterate rows: %v", doErr)
	}

	return cols, rows
}

func colIndex(cols []string, name string) int {
	for i, c := range cols {
		if c == name {
			return i
		}
	}

	return -1
}
