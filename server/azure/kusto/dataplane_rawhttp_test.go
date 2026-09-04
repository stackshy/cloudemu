package kusto_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestKustoDataPlaneRawMgmtFrame posts a control command over raw HTTP (no SDK)
// and asserts the v1 {Tables:[{TableName,Columns,Rows}]} frame shape, including
// that each column carries both DataType and ColumnType.
func TestKustoDataPlaneRawMgmtFrame(t *testing.T) {
	ts := newServer(t)
	token := testUserToken(t)

	post(t, ts, token, ".create table Metrics (host:string, value:real)")

	body := post(t, ts, token, ".show table Metrics cslschema")

	var resp struct {
		Tables []struct {
			TableName string `json:"TableName"`
			Columns   []struct {
				ColumnName string `json:"ColumnName"`
				DataType   string `json:"DataType"`
				ColumnType string `json:"ColumnType"`
			} `json:"Columns"`
			Rows [][]any `json:"Rows"`
		} `json:"Tables"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode v1 frame: %v (body %s)", err, body)
	}

	if len(resp.Tables) != 1 {
		t.Fatalf("Tables = %d, want 1", len(resp.Tables))
	}

	tbl := resp.Tables[0]
	if len(tbl.Columns) == 0 {
		t.Fatal("frame table has no columns")
	}

	for _, c := range tbl.Columns {
		if c.ColumnName == "" || c.DataType == "" || c.ColumnType == "" {
			t.Fatalf("column missing a field: %+v", c)
		}
	}

	if len(tbl.Rows) != 1 {
		t.Fatalf("Rows = %d, want 1 schema row", len(tbl.Rows))
	}
}

// post sends a data-plane mgmt request and returns the response body, asserting
// a 200.
func post(t *testing.T, ts *httptest.Server, token, csl string) []byte {
	t.Helper()

	payload, err := json.Marshal(map[string]string{"db": dataDB, "csl": csl})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		ts.URL+"/v1/rest/mgmt", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, buf.String())
	}

	return buf.Bytes()
}
