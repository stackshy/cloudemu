package nosql_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	nosqlprovider "github.com/stackshy/cloudemu/v2/providers/oci/nosql"
	ocinosql "github.com/stackshy/cloudemu/v2/server/oci/nosql"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

// The OCI-only capability is declared consumer-side, so the compile-time check
// that the mock satisfies it lives here, where the import direction allows it.
var _ ocinosql.Extras = (*nosqlprovider.Mock)(nil)

const (
	compartmentA = "ocid1.compartment.oc1..aaaa"
	compartmentB = "ocid1.compartment.oc1..bbbb"

	usersDDL = "CREATE TABLE users (id INTEGER, email STRING, name STRING, PRIMARY KEY (SHARD(id), email))"
)

func newHandler(t *testing.T) (*ocinosql.Handler, *nosqlprovider.Mock) {
	t.Helper()

	opts := config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC))),
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(compartmentA),
	)
	mock := nosqlprovider.New(opts)

	return ocinosql.New(mock, workrequest.New(opts)), mock
}

func do(t *testing.T, h http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader

	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)

		reader = strings.NewReader(string(raw))
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, reader))

	return rec
}

// createTable puts a users table in compartmentA and returns its OCID.
func createTable(t *testing.T, h http.Handler) string {
	t.Helper()

	rec := do(t, h, http.MethodPost, "/20190828/tables", map[string]any{
		"compartmentId": compartmentA,
		"ddlStatement":  usersDDL,
		"tableLimits": map[string]any{
			"maxReadUnits": 50, "maxWriteUnits": 50, "maxStorageInGBs": 1, "capacityMode": "PROVISIONED",
		},
	})
	require.Equal(t, http.StatusAccepted, rec.Code)

	got := do(t, h, http.MethodGet, "/20190828/tables/users", nil)
	require.Equal(t, http.StatusOK, got.Code)

	var table struct {
		ID string `json:"id"`
	}

	require.NoError(t, json.Unmarshal(got.Body.Bytes(), &table))

	return table.ID
}

// TestMatches pins what the handler claims and, importantly, what it must not:
// handlers are first-match-wins, so a broad predicate swallows another
// service's traffic.
func TestMatches(t *testing.T) {
	h, _ := newHandler(t)

	tests := []struct {
		name   string
		path   string
		expect bool
	}{
		{name: "table collection", path: "/20190828/tables", expect: true},
		{name: "single table", path: "/20190828/tables/users", expect: true},
		{name: "rows", path: "/20190828/tables/users/rows", expect: true},
		{name: "indexes", path: "/20190828/tables/users/indexes", expect: true},
		{name: "single index", path: "/20190828/tables/users/indexes/byName", expect: true},
		{name: "change compartment action", path: "/20190828/tables/users/actions/changeCompartment", expect: true},
		{name: "query", path: "/20190828/query", expect: true},
		{name: "query prepare", path: "/20190828/query/prepare", expect: true},

		{name: "the shared work request poller keeps its own path", path: "/20190828/workRequests", expect: false},
		{name: "a work request by id", path: "/20190828/workRequests/ocid1.workrequest.oc1.iad.a", expect: false},
		{name: "core networking tables are another api version", path: "/20160918/tables", expect: false},
		{name: "monitoring", path: "/20180401/metrics", expect: false},
		{name: "identity", path: "/20160918/users", expect: false},
		{name: "unversioned", path: "/tables", expect: false},
		{name: "root", path: "/", expect: false},
		{name: "too many segments", path: "/20190828/tables/users/indexes/byName/extra", expect: false},
		{name: "another collection under the nosql version", path: "/20190828/configuration", expect: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, h.Matches(httptest.NewRequest(http.MethodGet, tc.path, nil)))
		})
	}
}

// TestDriverWithoutExtrasIsNotImplemented covers the consumer-side capability
// contract: a database driver that is not the OCI mock gets a clean 501.
func TestDriverWithoutExtrasIsNotImplemented(t *testing.T) {
	h := ocinosql.New(plainDriver{}, workrequest.New(config.NewOptions()))

	rec := do(t, h, http.MethodGet, "/20190828/tables?compartmentId="+compartmentA, nil)

	assert.Equal(t, http.StatusNotImplemented, rec.Code)

	var body ocirest.ErrorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "NotImplemented", body.Code)
	assert.Contains(t, body.Message, "does not implement OCI NoSQL tables")
}

// TestCreateTableRecordsWorkRequest covers the asynchronous mutation contract:
// 202 plus an opc-work-request-id an SDK waiter can poll.
func TestCreateTableRecordsWorkRequest(t *testing.T) {
	opts := config.NewOptions(config.WithRegion("us-ashburn-1"), config.WithCompartmentID(compartmentA))
	store := workrequest.New(opts)
	h := ocinosql.New(nosqlprovider.New(opts), store)

	rec := do(t, h, http.MethodPost, "/20190828/tables", map[string]any{
		"compartmentId": compartmentA,
		"ddlStatement":  usersDDL,
		"tableLimits":   map[string]any{"capacityMode": "ON_DEMAND"},
	})

	require.Equal(t, http.StatusAccepted, rec.Code)

	wrID := rec.Header().Get(ocirest.HeaderWorkRequestID)
	require.NotEmpty(t, wrID)

	wr, ok := store.Get(wrID)
	require.True(t, ok)
	assert.Equal(t, "CREATE_TABLE", wr.OperationType)
	assert.Equal(t, compartmentA, wr.CompartmentID)
	require.Len(t, wr.Resources, 1)
	assert.Equal(t, "table", wr.Resources[0].EntityType)
	assert.Equal(t, workrequest.ActionCreated, wr.Resources[0].ActionType)
	assert.Contains(t, wr.Resources[0].Identifier, "ocid1.nosqltable.")
}

func TestAsyncMutationsStampWorkRequests(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	do(t, h, http.MethodPost, "/20190828/tables/users/indexes", map[string]any{
		"name": "byName", "keys": []map[string]string{{"columnName": "name"}},
	})

	tests := []struct {
		name      string
		method    string
		target    string
		body      any
		expectOp  string
		setupSkip bool
	}{
		{
			name:     "update table",
			method:   http.MethodPut,
			target:   "/20190828/tables/users",
			body:     map[string]any{"ddlStatement": "ALTER TABLE users (ADD nickname STRING)"},
			expectOp: "UPDATE_TABLE",
		},
		{
			name:     "change compartment",
			method:   http.MethodPost,
			target:   "/20190828/tables/users/actions/changeCompartment",
			body:     map[string]any{"toCompartmentId": compartmentB},
			expectOp: "CHANGE_TABLE_COMPARTMENT",
		},
		{
			name:     "delete index",
			method:   http.MethodDelete,
			target:   "/20190828/tables/users/indexes/byName",
			expectOp: "DELETE_INDEX",
		},
		{
			name:     "delete table",
			method:   http.MethodDelete,
			target:   "/20190828/tables/users",
			expectOp: "DELETE_TABLE",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, tc.method, tc.target, tc.body)

			require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
			assert.NotEmpty(t, rec.Header().Get(ocirest.HeaderWorkRequestID))
		})
	}
}

func TestCreateTableErrors(t *testing.T) {
	tests := []struct {
		name         string
		body         any
		expectStatus int
		expectWord   string
	}{
		{
			name:         "missing compartment",
			body:         map[string]any{"ddlStatement": usersDDL, "tableLimits": map[string]any{"capacityMode": "ON_DEMAND"}},
			expectStatus: http.StatusBadRequest,
			expectWord:   "compartmentId is required",
		},
		{
			name:         "missing limits",
			body:         map[string]any{"compartmentId": compartmentA, "ddlStatement": usersDDL},
			expectStatus: http.StatusBadRequest,
			expectWord:   "tableLimits is required",
		},
		{
			name: "unsupported ddl",
			body: map[string]any{
				"compartmentId": compartmentA,
				"ddlStatement":  "TRUNCATE TABLE users",
				"tableLimits":   map[string]any{"capacityMode": "ON_DEMAND"},
			},
			expectStatus: http.StatusBadRequest,
			expectWord:   "unsupported DDL statement",
		},
		{
			name: "name disagrees with the ddl",
			body: map[string]any{
				"name":          "other",
				"compartmentId": compartmentA,
				"ddlStatement":  usersDDL,
				"tableLimits":   map[string]any{"capacityMode": "ON_DEMAND"},
			},
			expectStatus: http.StatusBadRequest,
			expectWord:   "does not match the table named by ddlStatement",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newHandler(t)

			rec := do(t, h, http.MethodPost, "/20190828/tables", tc.body)

			require.Equal(t, tc.expectStatus, rec.Code)

			var body ocirest.ErrorBody
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Contains(t, body.Message, tc.expectWord)
		})
	}
}

// TestCreateTableNameMismatchLeavesNoTable checks the half-built table from a
// rejected name is not left behind.
func TestCreateTableNameMismatchLeavesNoTable(t *testing.T) {
	h, _ := newHandler(t)

	rec := do(t, h, http.MethodPost, "/20190828/tables", map[string]any{
		"name":          "other",
		"compartmentId": compartmentA,
		"ddlStatement":  usersDDL,
		"tableLimits":   map[string]any{"capacityMode": "ON_DEMAND"},
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)

	got := do(t, h, http.MethodGet, "/20190828/tables/users", nil)
	assert.Equal(t, http.StatusNotFound, got.Code)
}

func TestListTablesRequiresCompartmentID(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	rec := do(t, h, http.MethodGet, "/20190828/tables", nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var body ocirest.ErrorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body.Message, "compartmentId is required")
}

func TestListTablesFiltersByCompartment(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	tests := []struct {
		name        string
		compartment string
		expectLen   int
	}{
		{name: "owning compartment", compartment: compartmentA, expectLen: 1},
		{name: "another compartment", compartment: compartmentB, expectLen: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, "/20190828/tables?compartmentId="+tc.compartment, nil)

			require.Equal(t, http.StatusOK, rec.Code)

			var coll struct {
				Items []map[string]any `json:"items"`
			}

			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &coll))
			assert.Len(t, coll.Items, tc.expectLen)
		})
	}
}

// TestGetTableAcrossCompartmentsIs404 keeps a caller from probing for a table
// they cannot see: OCI returns the same NotAuthorizedOrNotFound either way.
func TestGetTableAcrossCompartmentsIs404(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	rec := do(t, h, http.MethodGet, "/20190828/tables/users?compartmentId="+compartmentB, nil)

	require.Equal(t, http.StatusNotFound, rec.Code)

	var body ocirest.ErrorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "NotAuthorizedOrNotFound", body.Code)
}

func TestGetTableByOCID(t *testing.T) {
	h, _ := newHandler(t)
	id := createTable(t, h)

	rec := do(t, h, http.MethodGet, "/20190828/tables/"+id, nil)

	require.Equal(t, http.StatusOK, rec.Code)

	var table struct {
		Name   string `json:"name"`
		Schema struct {
			PrimaryKey []string `json:"primaryKey"`
			ShardKey   []string `json:"shardKey"`
			TTL        int      `json:"ttl"`
		} `json:"schema"`
		LifecycleState string `json:"lifecycleState"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &table))
	assert.Equal(t, "users", table.Name)
	assert.Equal(t, []string{"id", "email"}, table.Schema.PrimaryKey)
	assert.Equal(t, []string{"id"}, table.Schema.ShardKey)
	assert.Equal(t, "ACTIVE", table.LifecycleState)
}

func TestRowRoundTripOverTheWire(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	put := do(t, h, http.MethodPut, "/20190828/tables/users/rows", map[string]any{
		"compartmentId": compartmentA,
		"value":         map[string]any{"id": 1, "email": "a@example.com", "name": "Ada"},
	})
	require.Equal(t, http.StatusOK, put.Code, put.Body.String())

	key := "?compartmentId=" + compartmentA + "&key=id:1&key=" + url.QueryEscape("email:a@example.com")

	get := do(t, h, http.MethodGet, "/20190828/tables/users/rows"+key, nil)
	require.Equal(t, http.StatusOK, get.Code)

	var row struct {
		Value            map[string]any `json:"value"`
		TimeOfExpiration string         `json:"timeOfExpiration"`
	}

	require.NoError(t, json.Unmarshal(get.Body.Bytes(), &row))
	assert.Equal(t, "Ada", row.Value["name"])
	assert.Empty(t, row.TimeOfExpiration)

	del := do(t, h, http.MethodDelete, "/20190828/tables/users/rows"+key, nil)
	require.Equal(t, http.StatusOK, del.Code)
	assert.JSONEq(t, `{"isSuccess":true}`, del.Body.String())

	missing := do(t, h, http.MethodGet, "/20190828/tables/users/rows"+key, nil)
	assert.Equal(t, http.StatusNotFound, missing.Code)
}

func TestRowErrors(t *testing.T) {
	tests := []struct {
		name         string
		method       string
		target       string
		body         any
		expectStatus int
		expectWord   string
	}{
		{
			name:         "get without a key",
			method:       http.MethodGet,
			target:       "/20190828/tables/users/rows?compartmentId=" + compartmentA,
			expectStatus: http.StatusBadRequest,
			expectWord:   "at least one key parameter is required",
		},
		{
			name:         "malformed key pair",
			method:       http.MethodGet,
			target:       "/20190828/tables/users/rows?compartmentId=" + compartmentA + "&key=id",
			expectStatus: http.StatusBadRequest,
			expectWord:   "is not a column:value pair",
		},
		{
			name:         "put without a value",
			method:       http.MethodPut,
			target:       "/20190828/tables/users/rows",
			body:         map[string]any{"compartmentId": compartmentA},
			expectStatus: http.StatusBadRequest,
			expectWord:   "value is required",
		},
		{
			name:   "put an undeclared column",
			method: http.MethodPut,
			target: "/20190828/tables/users/rows",
			body: map[string]any{
				"compartmentId": compartmentA,
				"value":         map[string]any{"id": 1, "email": "a@b.c", "bogus": "x"},
			},
			expectStatus: http.StatusBadRequest,
			expectWord:   "is not declared",
		},
		{
			name:   "put into a table in another compartment",
			method: http.MethodPut,
			target: "/20190828/tables/users/rows",
			body: map[string]any{
				"compartmentId": compartmentB,
				"value":         map[string]any{"id": 1, "email": "a@b.c"},
			},
			expectStatus: http.StatusNotFound,
			expectWord:   "not found",
		},
		{
			name:         "get from an unknown table",
			method:       http.MethodGet,
			target:       "/20190828/tables/nope/rows?compartmentId=" + compartmentA + "&key=id:1",
			expectStatus: http.StatusNotFound,
			expectWord:   "not found",
		},
		{
			name:         "unsupported verb",
			method:       http.MethodPost,
			target:       "/20190828/tables/users/rows",
			expectStatus: http.StatusMethodNotAllowed,
			expectWord:   "method not allowed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newHandler(t)
			createTable(t, h)

			rec := do(t, h, tc.method, tc.target, tc.body)

			require.Equal(t, tc.expectStatus, rec.Code, rec.Body.String())

			var body ocirest.ErrorBody
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Contains(t, body.Message, tc.expectWord)
		})
	}
}

func TestIndexLifecycleOverTheWire(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	create := do(t, h, http.MethodPost, "/20190828/tables/users/indexes", map[string]any{
		"compartmentId": compartmentA,
		"name":          "byName",
		"keys":          []map[string]string{{"columnName": "name"}},
	})
	require.Equal(t, http.StatusAccepted, create.Code, create.Body.String())
	assert.NotEmpty(t, create.Header().Get(ocirest.HeaderWorkRequestID))

	list := do(t, h, http.MethodGet, "/20190828/tables/users/indexes?compartmentId="+compartmentA, nil)
	require.Equal(t, http.StatusOK, list.Code)

	var coll struct {
		Items []struct {
			Name string `json:"name"`
			Keys []struct {
				ColumnName string `json:"columnName"`
			} `json:"keys"`
		} `json:"items"`
	}

	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &coll))
	require.Len(t, coll.Items, 1)
	assert.Equal(t, "byName", coll.Items[0].Name)
	require.Len(t, coll.Items[0].Keys, 1)
	assert.Equal(t, "name", coll.Items[0].Keys[0].ColumnName)

	get := do(t, h, http.MethodGet, "/20190828/tables/users/indexes/byName", nil)
	assert.Equal(t, http.StatusOK, get.Code)

	missing := do(t, h, http.MethodGet, "/20190828/tables/users/indexes/nope", nil)
	assert.Equal(t, http.StatusNotFound, missing.Code)

	del := do(t, h, http.MethodDelete, "/20190828/tables/users/indexes/byName", nil)
	assert.Equal(t, http.StatusAccepted, del.Code)

	again := do(t, h, http.MethodDelete, "/20190828/tables/users/indexes/byName", nil)
	assert.Equal(t, http.StatusNotFound, again.Code)

	tolerant := do(t, h, http.MethodDelete, "/20190828/tables/users/indexes/byName?isIfExists=true", nil)
	assert.Equal(t, http.StatusAccepted, tolerant.Code)
}

func TestListIndexesRequiresCompartmentID(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	rec := do(t, h, http.MethodGet, "/20190828/tables/users/indexes", nil)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestQueryOverTheWire(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	for _, email := range []string{"a@x.com", "b@x.com"} {
		put := do(t, h, http.MethodPut, "/20190828/tables/users/rows", map[string]any{
			"compartmentId": compartmentA,
			"value":         map[string]any{"id": 1, "email": email, "name": "Ada"},
		})
		require.Equal(t, http.StatusOK, put.Code)
	}

	sel := do(t, h, http.MethodPost, "/20190828/query", map[string]any{
		"compartmentId": compartmentA, "statement": "SELECT * FROM users",
	})
	require.Equal(t, http.StatusOK, sel.Code)

	var coll struct {
		Items []map[string]any `json:"items"`
	}

	require.NoError(t, json.Unmarshal(sel.Body.Bytes(), &coll))
	assert.Len(t, coll.Items, 2)

	del := do(t, h, http.MethodPost, "/20190828/query", map[string]any{
		"compartmentId": compartmentA, "statement": "DELETE FROM users WHERE id = 1",
	})
	require.Equal(t, http.StatusOK, del.Code)

	require.NoError(t, json.Unmarshal(del.Body.Bytes(), &coll))
	require.Len(t, coll.Items, 1)
	assert.InDelta(t, 2.0, coll.Items[0]["NumRowsDeleted"], 0.001)
}

func TestQueryErrors(t *testing.T) {
	tests := []struct {
		name         string
		method       string
		target       string
		body         any
		expectStatus int
		expectWord   string
	}{
		{
			name:         "no compartment",
			method:       http.MethodPost,
			target:       "/20190828/query",
			body:         map[string]any{"statement": "SELECT * FROM users"},
			expectStatus: http.StatusBadRequest,
			expectWord:   "compartmentId is required",
		},
		{
			name:         "no statement",
			method:       http.MethodPost,
			target:       "/20190828/query",
			body:         map[string]any{"compartmentId": compartmentA},
			expectStatus: http.StatusBadRequest,
			expectWord:   "statement is required",
		},
		{
			name:         "unsupported statement",
			method:       http.MethodPost,
			target:       "/20190828/query",
			body:         map[string]any{"compartmentId": compartmentA, "statement": "SELECT name FROM users"},
			expectStatus: http.StatusBadRequest,
			expectWord:   "only SELECT * is supported",
		},
		{
			name:         "prepare is not emulated",
			method:       http.MethodGet,
			target:       "/20190828/query/prepare?compartmentId=" + compartmentA,
			expectStatus: http.StatusNotImplemented,
			expectWord:   "prepared-statement handle",
		},
		{
			name:         "summarize is not emulated",
			method:       http.MethodPost,
			target:       "/20190828/query/summarize",
			body:         map[string]any{"compartmentId": compartmentA},
			expectStatus: http.StatusNotImplemented,
			expectWord:   "prepared-statement handle",
		},
		{
			name:         "unknown query sub-resource",
			method:       http.MethodPost,
			target:       "/20190828/query/explain",
			expectStatus: http.StatusNotFound,
			expectWord:   "unknown query sub-resource",
		},
		{
			name:         "query is POST only",
			method:       http.MethodGet,
			target:       "/20190828/query",
			expectStatus: http.StatusMethodNotAllowed,
			expectWord:   "method not allowed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newHandler(t)
			createTable(t, h)

			rec := do(t, h, tc.method, tc.target, tc.body)

			require.Equal(t, tc.expectStatus, rec.Code, rec.Body.String())

			var body ocirest.ErrorBody
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Contains(t, body.Message, tc.expectWord)
		})
	}
}

// TestUnemulatedPaths covers the paths the handler claims in order to report
// them, so a caller reaching for one is told why rather than left with a 404.
func TestUnemulatedPaths(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	rec := do(t, h, http.MethodGet, "/20190828/tables/users/usage?compartmentId="+compartmentA, nil)

	require.Equal(t, http.StatusNotImplemented, rec.Code)

	var body ocirest.ErrorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body.Message, "does not meter read, write and storage consumption")
}

func TestRoutingErrors(t *testing.T) {
	tests := []struct {
		name         string
		method       string
		target       string
		expectStatus int
	}{
		{name: "unknown sub-collection", method: http.MethodGet, target: "/20190828/tables/users/columns", expectStatus: http.StatusNotFound},
		{name: "unknown action", method: http.MethodPost, target: "/20190828/tables/users/actions/freeze", expectStatus: http.StatusNotFound},
		{name: "action must be POST", method: http.MethodGet, target: "/20190828/tables/users/actions/changeCompartment", expectStatus: http.StatusMethodNotAllowed},
		{name: "collection verb", method: http.MethodPatch, target: "/20190828/tables", expectStatus: http.StatusMethodNotAllowed},
		{name: "single table verb", method: http.MethodPatch, target: "/20190828/tables/users", expectStatus: http.StatusMethodNotAllowed},
		{name: "index collection verb", method: http.MethodPatch, target: "/20190828/tables/users/indexes", expectStatus: http.StatusMethodNotAllowed},
		{name: "single index verb", method: http.MethodPatch, target: "/20190828/tables/users/indexes/x", expectStatus: http.StatusMethodNotAllowed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newHandler(t)
			createTable(t, h)

			rec := do(t, h, tc.method, tc.target, nil)

			assert.Equal(t, tc.expectStatus, rec.Code, rec.Body.String())
		})
	}
}

func TestChangeCompartmentRequiresDestination(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	rec := do(t, h, http.MethodPost, "/20190828/tables/users/actions/changeCompartment", map[string]any{})

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var body ocirest.ErrorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body.Message, "toCompartmentId is required")
}

func TestWorkRequestsUnconfiguredIsNotImplemented(t *testing.T) {
	h := ocinosql.New(nosqlprovider.New(config.NewOptions()), nil)

	rec := do(t, h, http.MethodPost, "/20190828/tables", map[string]any{
		"compartmentId": compartmentA,
		"ddlStatement":  usersDDL,
		"tableLimits":   map[string]any{"capacityMode": "ON_DEMAND"},
	})

	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

// plainDriver is a database driver that does not implement the OCI capability.
type plainDriver struct {
	dbdriver.Database
}
