package bigquery_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	provbq "github.com/stackshy/cloudemu/v2/providers/gcp/bigquery"
	srvbq "github.com/stackshy/cloudemu/v2/server/gcp/bigquery"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	h := srvbq.New(provbq.New(&config.Options{}))
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	return ts
}

// do issues a request and decodes the JSON response into a generic map.
func do(t *testing.T, ts *httptest.Server, method, path, body string) (int, map[string]any) {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	}

	req, err := http.NewRequest(method, ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}

	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode %s %s (status %d): %v\nbody: %s", method, path, resp.StatusCode, err, raw)
		}
	}

	return resp.StatusCode, out
}

const wireProject = "proj-1"

func datasetsPath() string { return "/bigquery/v2/projects/" + wireProject + "/datasets" }

// TestDatasetWireShape asserts the dataset id colon format, default location,
// kind, and epoch-millis-STRING timestamps.
func TestDatasetWireShape(t *testing.T) {
	ts := newServer(t)

	status, ds := do(t, ts, http.MethodPost, datasetsPath(), `{
		"datasetReference": {"datasetId": "ds1"},
		"friendlyName": "Friendly",
		"labels": {"env": "test"}
	}`)
	if status != http.StatusOK {
		t.Fatalf("insert status = %d (%v)", status, ds)
	}

	if got := ds["id"]; got != wireProject+":ds1" {
		t.Fatalf("dataset id = %v, want %s:ds1 (colon)", got, wireProject)
	}

	if ds["kind"] != "bigquery#dataset" {
		t.Fatalf("kind = %v", ds["kind"])
	}

	if ds["location"] != "US" {
		t.Fatalf("location = %v, want US", ds["location"])
	}

	assertEpochMillisString(t, "creationTime", ds["creationTime"])
	assertEpochMillisString(t, "lastModifiedTime", ds["lastModifiedTime"])

	// GET round-trips the same id + labels.
	_, got := do(t, ts, http.MethodGet, datasetsPath()+"/ds1", "")
	if got["id"] != wireProject+":ds1" {
		t.Fatalf("get id = %v", got["id"])
	}

	labels, _ := got["labels"].(map[string]any)
	if labels["env"] != "test" {
		t.Fatalf("labels did not round-trip: %v", got["labels"])
	}
}

// TestTableSchemaWireShape asserts the table id colon+dot format, the mode
// NULLABLE echo on a field sent without a mode, the nested RECORD round-trip,
// and the quoted-int64 numRows/numBytes.
func TestTableSchemaWireShape(t *testing.T) {
	ts := newServer(t)

	if status, ds := do(t, ts, http.MethodPost, datasetsPath(),
		`{"datasetReference":{"datasetId":"ds1"}}`); status != http.StatusOK {
		t.Fatalf("insert dataset status = %d (%v)", status, ds)
	}

	tablesPath := datasetsPath() + "/ds1/tables"
	status, _ := do(t, ts, http.MethodPost, tablesPath, `{
		"tableReference": {"tableId": "t1"},
		"schema": {"fields": [
			{"name": "id", "type": "INTEGER", "mode": "REQUIRED"},
			{"name": "name", "type": "STRING"},
			{"name": "addr", "type": "RECORD", "fields": [
				{"name": "city", "type": "STRING"},
				{"name": "zip", "type": "STRING", "mode": "REQUIRED"}
			]}
		]}
	}`)
	if status != http.StatusOK {
		t.Fatalf("insert table status = %d", status)
	}

	_, tbl := do(t, ts, http.MethodGet, tablesPath+"/t1", "")

	if tbl["id"] != wireProject+":ds1.t1" {
		t.Fatalf("table id = %v, want %s:ds1.t1 (colon+dot)", tbl["id"], wireProject)
	}

	if tbl["type"] != "TABLE" {
		t.Fatalf("type = %v", tbl["type"])
	}

	assertQuotedInt(t, "numRows", tbl["numRows"])
	assertQuotedInt(t, "numBytes", tbl["numBytes"])
	assertEpochMillisString(t, "creationTime", tbl["creationTime"])

	fields := schemaFields(t, tbl)
	if len(fields) != 3 {
		t.Fatalf("schema fields = %d", len(fields))
	}

	// Field sent without a mode must echo NULLABLE.
	name := fields[1].(map[string]any)
	if name["name"] != "name" || name["mode"] != "NULLABLE" {
		t.Fatalf("mode NULLABLE not echoed for default field: %v", name)
	}

	// Nested RECORD round-trips with its children and their modes.
	rec := fields[2].(map[string]any)
	nested, _ := rec["fields"].([]any)
	if len(nested) != 2 {
		t.Fatalf("nested RECORD fields = %v", rec["fields"])
	}

	zip := nested[1].(map[string]any)
	if zip["name"] != "zip" || zip["mode"] != "REQUIRED" {
		t.Fatalf("nested REQUIRED field not preserved: %v", zip)
	}

	city := nested[0].(map[string]any)
	if city["mode"] != "NULLABLE" {
		t.Fatalf("nested default field mode not echoed NULLABLE: %v", city)
	}
}

func TestNotFoundEnvelope(t *testing.T) {
	ts := newServer(t)

	status, body := do(t, ts, http.MethodGet, datasetsPath()+"/missing", "")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}

	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error envelope: %v", body)
	}

	if errObj["status"] != "NOT_FOUND" {
		t.Fatalf("error.status = %v, want NOT_FOUND", errObj["status"])
	}

	// No cerrors code prefix should leak into the message.
	if msg, _ := errObj["message"].(string); bytes.Contains([]byte(msg), []byte("NotFound:")) {
		t.Fatalf("cerrors prefix leaked into message: %q", msg)
	}
}

func TestDuplicateDatasetConflict(t *testing.T) {
	ts := newServer(t)

	body := `{"datasetReference":{"datasetId":"dup"}}`
	if status, _ := do(t, ts, http.MethodPost, datasetsPath(), body); status != http.StatusOK {
		t.Fatalf("first insert status = %d", status)
	}

	status, resp := do(t, ts, http.MethodPost, datasetsPath(), body)
	if status != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409 (%v)", status, resp)
	}
}

func schemaFields(t *testing.T, tbl map[string]any) []any {
	t.Helper()

	schema, ok := tbl["schema"].(map[string]any)
	if !ok {
		t.Fatalf("no schema in table: %v", tbl)
	}

	fields, ok := schema["fields"].([]any)
	if !ok {
		t.Fatalf("no schema.fields: %v", schema)
	}

	return fields
}

func assertEpochMillisString(t *testing.T, field string, v any) {
	t.Helper()

	s, ok := v.(string)
	if !ok {
		t.Fatalf("%s = %v (%T), want epoch-millis STRING not number", field, v, v)
	}

	if _, err := strconv.ParseInt(s, 10, 64); err != nil {
		t.Fatalf("%s = %q is not a decimal epoch-millis string: %v", field, s, err)
	}
}

func assertQuotedInt(t *testing.T, field string, v any) {
	t.Helper()

	s, ok := v.(string)
	if !ok {
		t.Fatalf("%s = %v (%T), want quoted int64 STRING", field, v, v)
	}

	if _, err := strconv.ParseInt(s, 10, 64); err != nil {
		t.Fatalf("%s = %q is not a decimal int string", field, s)
	}
}
