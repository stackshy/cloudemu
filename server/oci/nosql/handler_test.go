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
		"name":          "users",
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
		"name":          "users",
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
			name: "missing name",
			body: map[string]any{
				"compartmentId": compartmentA,
				"ddlStatement":  usersDDL,
				"tableLimits":   map[string]any{"capacityMode": "ON_DEMAND"},
			},
			expectStatus: http.StatusBadRequest,
			expectWord:   "name is required",
		},
		{
			name: "unsupported ddl",
			body: map[string]any{
				"name":          "users",
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

// TestCreateTableNameMismatchLeavesNoTable checks a rejected name builds no
// table.
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

// A create whose name disagrees with its DDL never touches a table that
// already exists, IF NOT EXISTS or not, and a case-only difference is a match.
func TestRejectedCreateKeepsExistingTable(t *testing.T) {
	tests := []struct {
		name         string
		tableName    string
		ddl          string
		expectStatus int
	}{
		{
			name:         "if not exists with a mismatched name",
			tableName:    "orders",
			ddl:          "CREATE TABLE IF NOT EXISTS users (id INTEGER, email STRING, PRIMARY KEY (SHARD(id), email))",
			expectStatus: http.StatusBadRequest,
		},
		{
			name:         "mismatched name",
			tableName:    "orders",
			ddl:          usersDDL,
			expectStatus: http.StatusBadRequest,
		},
		{
			name:         "name differing only in case",
			tableName:    "Users",
			ddl:          "CREATE TABLE IF NOT EXISTS users (id INTEGER, email STRING, PRIMARY KEY (SHARD(id), email))",
			expectStatus: http.StatusAccepted,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newHandler(t)
			createTable(t, h)

			rec := do(t, h, http.MethodPut, "/20190828/tables/users/rows", map[string]any{
				"compartmentId": compartmentA,
				"value":         map[string]any{"id": 1, "email": "a@example.com", "name": "Ada"},
			})
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			rec = do(t, h, http.MethodPost, "/20190828/tables", map[string]any{
				"name":          tc.tableName,
				"compartmentId": compartmentA,
				"ddlStatement":  tc.ddl,
				"tableLimits":   map[string]any{"capacityMode": "ON_DEMAND"},
			})
			require.Equal(t, tc.expectStatus, rec.Code, rec.Body.String())

			rec = do(t, h, http.MethodGet,
				"/20190828/tables/users/rows?compartmentId="+compartmentA+"&key=id:1&key=email:a@example.com", nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "Ada")
		})
	}
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
			name:   "put an undeclared column with isExactMatch",
			method: http.MethodPut,
			target: "/20190828/tables/users/rows",
			body: map[string]any{
				"compartmentId": compartmentA,
				"value":         map[string]any{"id": 1, "email": "a@b.c", "bogus": "x"},
				"isExactMatch":  true,
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
		"name":          "users",
		"ddlStatement":  usersDDL,
		"tableLimits":   map[string]any{"capacityMode": "ON_DEMAND"},
	})

	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

// plainDriver is a database driver that does not implement the OCI capability.
type plainDriver struct {
	dbdriver.Database
}

// TestSameTableNameInTwoCompartments is the per-compartment naming contract on
// the wire: an SDK that reuses a table name across compartments creates two
// tables, and each compartmentId addresses its own.
func TestSameTableNameInTwoCompartments(t *testing.T) {
	h, _ := newHandler(t)

	idA := createTable(t, h)

	rec := do(t, h, http.MethodPost, "/20190828/tables", map[string]any{
		"compartmentId": compartmentB,
		"name":          "users",
		"ddlStatement":  usersDDL,
		"tableLimits": map[string]any{
			"maxReadUnits": 50, "maxWriteUnits": 50, "maxStorageInGBs": 1, "capacityMode": "PROVISIONED",
		},
	})
	require.Equal(t, http.StatusAccepted, rec.Code)

	var inB struct {
		ID            string `json:"id"`
		CompartmentID string `json:"compartmentId"`
	}

	got := do(t, h, http.MethodGet, "/20190828/tables/users?compartmentId="+compartmentB, nil)
	require.Equal(t, http.StatusOK, got.Code)
	require.NoError(t, json.Unmarshal(got.Body.Bytes(), &inB))

	assert.NotEqual(t, idA, inB.ID)
	assert.Equal(t, compartmentB, inB.CompartmentID)

	// A row written to one is invisible to the other.
	rec = do(t, h, http.MethodPut, "/20190828/tables/users/rows", map[string]any{
		"compartmentId": compartmentB,
		"value":         map[string]any{"id": 1, "email": "b@example.com", "name": "Bea"},
	})
	require.Equal(t, http.StatusOK, rec.Code)

	rec = do(t, h, http.MethodGet,
		"/20190828/tables/users/rows?compartmentId="+compartmentA+"&key=id:1&key=email:b@example.com", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// Each listing sees one table.
	for _, c := range []string{compartmentA, compartmentB} {
		rec = do(t, h, http.MethodGet, "/20190828/tables?compartmentId="+c, nil)
		require.Equal(t, http.StatusOK, rec.Code)

		var list struct {
			Items []struct {
				Name string `json:"name"`
			} `json:"items"`
		}

		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
		require.Len(t, list.Items, 1)
		assert.Equal(t, "users", list.Items[0].Name)
	}

	// Deleting one leaves the other.
	rec = do(t, h, http.MethodDelete, "/20190828/tables/users?compartmentId="+compartmentB, nil)
	require.Equal(t, http.StatusAccepted, rec.Code)

	rec = do(t, h, http.MethodGet, "/20190828/tables/users?compartmentId="+compartmentA, nil)
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = do(t, h, http.MethodGet, "/20190828/tables/users?compartmentId="+compartmentB, nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestIndexesScopePerCompartment pins that an index is created on the table in
// the compartment the request names, not on a same-named one elsewhere.
func TestIndexesScopePerCompartment(t *testing.T) {
	h, _ := newHandler(t)

	createTable(t, h)

	rec := do(t, h, http.MethodPost, "/20190828/tables", map[string]any{
		"compartmentId": compartmentB,
		"name":          "users",
		"ddlStatement":  usersDDL,
		"tableLimits": map[string]any{
			"maxReadUnits": 50, "maxWriteUnits": 50, "maxStorageInGBs": 1, "capacityMode": "PROVISIONED",
		},
	})
	require.Equal(t, http.StatusAccepted, rec.Code)

	rec = do(t, h, http.MethodPost, "/20190828/tables/users/indexes", map[string]any{
		"compartmentId": compartmentB,
		"name":          "byName",
		"keys":          []map[string]any{{"columnName": "name"}},
	})
	require.Equal(t, http.StatusAccepted, rec.Code)

	rec = do(t, h, http.MethodGet, "/20190828/tables/users/indexes?compartmentId="+compartmentB, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "byName")

	rec = do(t, h, http.MethodGet, "/20190828/tables/users/indexes?compartmentId="+compartmentA, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "byName")
}

// TestChangeCompartmentScopesFromCompartment moves the table the request's
// fromCompartmentId names, leaving a same-named table elsewhere alone.
func TestChangeCompartmentScopesFromCompartment(t *testing.T) {
	h, _ := newHandler(t)

	idA := createTable(t, h)
	toC := "ocid1.compartment.oc1..cccc"

	rec := do(t, h, http.MethodPost, "/20190828/tables/users/actions/changeCompartment", map[string]any{
		"fromCompartmentId": compartmentA,
		"toCompartmentId":   toC,
	})
	require.Equal(t, http.StatusAccepted, rec.Code)

	got := do(t, h, http.MethodGet, "/20190828/tables/users?compartmentId="+toC, nil)
	require.Equal(t, http.StatusOK, got.Code)
	assert.Contains(t, got.Body.String(), idA)

	got = do(t, h, http.MethodGet, "/20190828/tables/users?compartmentId="+compartmentA, nil)
	assert.Equal(t, http.StatusNotFound, got.Code)
}

// TestAsyncPathsNeedWorkRequests: every mutation real OCI runs asynchronously
// reports the missing work request store rather than half-serving the request.
func TestAsyncPathsNeedWorkRequests(t *testing.T) {
	h := ocinosql.New(nosqlprovider.New(config.NewOptions()), nil)

	tests := []struct {
		name   string
		method string
		target string
	}{
		{name: "update table", method: http.MethodPut, target: "/20190828/tables/users"},
		{name: "delete table", method: http.MethodDelete, target: "/20190828/tables/users"},
		{name: "create index", method: http.MethodPost, target: "/20190828/tables/users/indexes"},
		{name: "delete index", method: http.MethodDelete, target: "/20190828/tables/users/indexes/byName"},
		{
			name: "change compartment", method: http.MethodPost,
			target: "/20190828/tables/users/actions/changeCompartment",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, tc.method, tc.target, map[string]any{})
			assert.Equal(t, http.StatusNotImplemented, rec.Code)
		})
	}
}

// TestMalformedBodies refuses a body that is not the JSON the route models.
func TestMalformedBodies(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	tests := []struct {
		name   string
		method string
		target string
	}{
		{name: "create table", method: http.MethodPost, target: "/20190828/tables"},
		{name: "update table", method: http.MethodPut, target: "/20190828/tables/users"},
		{name: "create index", method: http.MethodPost, target: "/20190828/tables/users/indexes"},
		{name: "update row", method: http.MethodPut, target: "/20190828/tables/users/rows"},
		{name: "query", method: http.MethodPost, target: "/20190828/query"},
		{
			name: "change compartment", method: http.MethodPost,
			target: "/20190828/tables/users/actions/changeCompartment",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.target, strings.NewReader("{")))

			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

// TestTableMutationErrors drives the failure branches of the mutating table
// routes: a statement of the wrong kind, and a table that is not there.
func TestTableMutationErrors(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	rec := do(t, h, http.MethodPut, "/20190828/tables/users", map[string]any{
		"compartmentId": compartmentA,
		"name":          "users",
		"ddlStatement":  usersDDL,
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = do(t, h, http.MethodPut, "/20190828/tables/missing", map[string]any{
		"compartmentId": compartmentA,
		"ddlStatement":  "ALTER TABLE missing (ADD nickname STRING)",
	})
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = do(t, h, http.MethodDelete, "/20190828/tables/missing?compartmentId="+compartmentA, nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = do(t, h, http.MethodPost, "/20190828/tables/missing/actions/changeCompartment", map[string]any{
		"fromCompartmentId": compartmentA,
		"toCompartmentId":   compartmentB,
	})
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// A move into a compartment already holding that name is a conflict.
	rec = do(t, h, http.MethodPost, "/20190828/tables", map[string]any{
		"compartmentId": compartmentB,
		"name":          "users",
		"ddlStatement":  usersDDL,
		"tableLimits":   map[string]any{"capacityMode": "ON_DEMAND"},
	})
	require.Equal(t, http.StatusAccepted, rec.Code)

	rec = do(t, h, http.MethodPost, "/20190828/tables/users/actions/changeCompartment", map[string]any{
		"fromCompartmentId": compartmentA,
		"toCompartmentId":   compartmentB,
	})
	assert.Equal(t, http.StatusConflict, rec.Code)
}

// TestIndexAndRowErrors drives the failure branches of the index and row
// routes: a table that is not there, and an index that is not.
func TestIndexAndRowErrors(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	tests := []struct {
		name   string
		method string
		target string
		body   any
		expect int
	}{
		{
			name: "create index on a missing table", method: http.MethodPost,
			target: "/20190828/tables/missing/indexes",
			body:   map[string]any{"compartmentId": compartmentA, "name": "byName"},
			expect: http.StatusNotFound,
		},
		{
			name: "create index with no key column", method: http.MethodPost,
			target: "/20190828/tables/users/indexes",
			body:   map[string]any{"compartmentId": compartmentA, "name": "byNothing"},
			expect: http.StatusBadRequest,
		},
		{
			name: "list indexes of a missing table", method: http.MethodGet,
			target: "/20190828/tables/missing/indexes?compartmentId=" + compartmentA,
			expect: http.StatusNotFound,
		},
		{
			name: "get a missing index", method: http.MethodGet,
			target: "/20190828/tables/users/indexes/byNothing?compartmentId=" + compartmentA,
			expect: http.StatusNotFound,
		},
		{
			name: "get an index of a missing table", method: http.MethodGet,
			target: "/20190828/tables/missing/indexes/byName?compartmentId=" + compartmentA,
			expect: http.StatusNotFound,
		},
		{
			name: "delete a missing index", method: http.MethodDelete,
			target: "/20190828/tables/users/indexes/byNothing?compartmentId=" + compartmentA,
			expect: http.StatusNotFound,
		},
		{
			name: "delete an index of a missing table", method: http.MethodDelete,
			target: "/20190828/tables/missing/indexes/byName?compartmentId=" + compartmentA,
			expect: http.StatusNotFound,
		},
		{
			name: "get a row of a missing table", method: http.MethodGet,
			target: "/20190828/tables/missing/rows?compartmentId=" + compartmentA + "&key=id:1",
			expect: http.StatusNotFound,
		},
		{
			name: "delete a row of a missing table", method: http.MethodDelete,
			target: "/20190828/tables/missing/rows?compartmentId=" + compartmentA + "&key=id:1",
			expect: http.StatusNotFound,
		},
		{
			name: "put a row to a missing table", method: http.MethodPut,
			target: "/20190828/tables/missing/rows",
			body: map[string]any{
				"compartmentId": compartmentA,
				"value":         map[string]any{"id": 1, "email": "a@example.com"},
			},
			expect: http.StatusNotFound,
		},
		{
			name: "delete a row named by no key", method: http.MethodDelete,
			target: "/20190828/tables/users/rows?compartmentId=" + compartmentA,
			expect: http.StatusBadRequest,
		},
		{
			name: "delete a row whose key is not a pair", method: http.MethodDelete,
			target: "/20190828/tables/users/rows?compartmentId=" + compartmentA + "&key=id",
			expect: http.StatusBadRequest,
		},
		{
			name: "delete a row whose key is not a primary key column", method: http.MethodDelete,
			target: "/20190828/tables/users/rows?compartmentId=" + compartmentA + "&key=name:Ada",
			expect: http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, tc.method, tc.target, tc.body)
			assert.Equal(t, tc.expect, rec.Code)
		})
	}
}

// TestIndexIfNotExistsAndIfExists takes the idempotent branches OCI's
// isIfNotExists and isIfExists parameters select.
func TestIndexIfNotExistsAndIfExists(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	body := map[string]any{
		"compartmentId": compartmentA,
		"name":          "byName",
		"keys":          []map[string]any{{"columnName": "name"}},
		"isIfNotExists": true,
	}

	for range 2 {
		rec := do(t, h, http.MethodPost, "/20190828/tables/users/indexes", body)
		require.Equal(t, http.StatusAccepted, rec.Code)
	}

	rec := do(t, h, http.MethodDelete,
		"/20190828/tables/users/indexes/byName?compartmentId="+compartmentA+"&isIfExists=true", nil)
	require.Equal(t, http.StatusAccepted, rec.Code)

	rec = do(t, h, http.MethodDelete,
		"/20190828/tables/users/indexes/byName?compartmentId="+compartmentA+"&isIfExists=true", nil)
	assert.Equal(t, http.StatusAccepted, rec.Code)
}

// TestListPaginates walks a listing a page at a time with OCI's limit and the
// opaque page cursor the previous response stamped.
func TestListPaginates(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	for _, column := range []string{"name", "email"} {
		rec := do(t, h, http.MethodPost, "/20190828/tables/users/indexes", map[string]any{
			"compartmentId": compartmentA,
			"name":          "by" + column,
			"keys":          []map[string]any{{"columnName": column}},
		})
		require.Equal(t, http.StatusAccepted, rec.Code)
	}

	seen := make([]string, 0, 2)
	page := ""

	for range 2 {
		target := "/20190828/tables/users/indexes?compartmentId=" + compartmentA + "&limit=1"
		if page != "" {
			target += "&page=" + page
		}

		rec := do(t, h, http.MethodGet, target, nil)
		require.Equal(t, http.StatusOK, rec.Code)

		var list struct {
			Items []struct {
				Name string `json:"name"`
			} `json:"items"`
		}

		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
		require.Len(t, list.Items, 1)

		seen = append(seen, list.Items[0].Name)
		page = rec.Header().Get(ocirest.HeaderNextPage)
	}

	assert.ElementsMatch(t, []string{"byname", "byemail"}, seen)

	// A cursor past the end is an empty page, not an error.
	rec := do(t, h, http.MethodGet,
		"/20190828/tables/users/indexes?compartmentId="+compartmentA+"&page=99", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"items":[]`)
}

// The SDK percent-encodes each key value once and the query is decoded once,
// so a value holding '+' or '%' reads back as itself.
func TestRowKeysAreDecodedOnce(t *testing.T) {
	h, _ := newHandler(t)

	rec := do(t, h, http.MethodPost, "/20190828/tables", map[string]any{
		"compartmentId": compartmentA,
		"name":          "p",
		"ddlStatement":  "CREATE TABLE p (k STRING, v STRING, PRIMARY KEY (k))",
		"tableLimits":   map[string]any{"capacityMode": "ON_DEMAND"},
	})
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

	for value, encoded := range map[string]string{"a+b": "a%2Bb", "50%": "50%25"} {
		rec = do(t, h, http.MethodPut, "/20190828/tables/p/rows", map[string]any{
			"compartmentId": compartmentA, "value": map[string]any{"k": value, "v": "x"},
		})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		target := "/20190828/tables/p/rows?compartmentId=" + compartmentA + "&key=k:" + encoded

		rec = do(t, h, http.MethodGet, target, nil)
		assert.Equal(t, http.StatusOK, rec.Code, value+": "+rec.Body.String())

		rec = do(t, h, http.MethodDelete, target, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{"isSuccess":true}`, rec.Body.String())
	}
}

// A conditional write its option refuses is a 200 with a null version, as
// OCI's UpdateRowResult reports it; isGetReturnRow returns the row in the way.
func TestUpdateRowOptionsReturnAResult(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	row := map[string]any{"id": 1, "email": "a@example.com", "name": "Ada"}

	rec := do(t, h, http.MethodPut, "/20190828/tables/users/rows", map[string]any{
		"compartmentId": compartmentA, "value": row, "option": "IF_ABSENT",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var written struct {
		Version *string `json:"version"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &written))
	require.NotNil(t, written.Version)

	rec = do(t, h, http.MethodPut, "/20190828/tables/users/rows", map[string]any{
		"compartmentId": compartmentA, "value": row, "option": "IF_ABSENT", "isGetReturnRow": true,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var refused map[string]any

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &refused))
	assert.Contains(t, refused, "version")
	assert.Nil(t, refused["version"])
	assert.Equal(t, *written.Version, refused["existingVersion"])
	assert.Equal(t, "Ada", refused["existingValue"].(map[string]any)["name"])

	rec = do(t, h, http.MethodPut, "/20190828/tables/users/rows", map[string]any{
		"compartmentId": compartmentA, "option": "IF_PRESENT",
		"value": map[string]any{"id": 2, "email": "b@example.com", "name": "Bob"},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"version":null}`, rec.Body.String())
}

// UpdateTable by OCID goes through the same compartment check as the other
// table routes.
func TestUpdateTableByOCIDFromAnotherCompartment(t *testing.T) {
	h, _ := newHandler(t)
	id := createTable(t, h)

	rec := do(t, h, http.MethodPut, "/20190828/tables/"+id, map[string]any{
		"compartmentId": compartmentB, "freeformTags": map[string]string{"x": "y"},
	})
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())

	rec = do(t, h, http.MethodGet, "/20190828/tables/"+id+"?compartmentId="+compartmentA, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), `"x":"y"`)
}

func TestDeleteTableIsIfExists(t *testing.T) {
	h, _ := newHandler(t)

	rec := do(t, h, http.MethodDelete, "/20190828/tables/missing?compartmentId="+compartmentA, nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = do(t, h, http.MethodDelete, "/20190828/tables/missing?compartmentId="+compartmentA+"&isIfExists=true", nil)
	assert.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	assert.NotEmpty(t, rec.Header().Get("opc-work-request-id"))
}

// NoSQL's OperationTypeEnum is CREATE_TABLE, UPDATE_TABLE, DELETE_TABLE and
// UPDATE_CONFIGURATION; index and compartment changes report UPDATE_TABLE.
func TestWorkRequestOperationTypesAreInTheNoSQLEnum(t *testing.T) {
	opts := config.NewOptions(config.WithRegion("us-ashburn-1"), config.WithCompartmentID(compartmentA))
	work := workrequest.New(opts)
	h := ocinosql.New(nosqlprovider.New(opts), work)

	createTable(t, h)

	opOf := func(rec *httptest.ResponseRecorder) string {
		t.Helper()
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

		wr, ok := work.Get(rec.Header().Get("opc-work-request-id"))
		require.True(t, ok)

		return wr.OperationType
	}

	assert.Equal(t, "UPDATE_TABLE", opOf(do(t, h, http.MethodPost, "/20190828/tables/users/indexes", map[string]any{
		"name": "by_name", "compartmentId": compartmentA, "keys": []map[string]any{{"columnName": "name"}},
	})))
	assert.Equal(t, "UPDATE_TABLE", opOf(do(t, h, http.MethodDelete,
		"/20190828/tables/users/indexes/by_name?compartmentId="+compartmentA, nil)))
	assert.Equal(t, "UPDATE_TABLE", opOf(do(t, h, http.MethodPost, "/20190828/tables/users/actions/changeCompartment",
		map[string]any{"fromCompartmentId": compartmentA, "toCompartmentId": compartmentB})))
}

func TestCompartmentMustExist(t *testing.T) {
	h, _ := newHandler(t)
	h.SetCompartmentChecker(func(id string) bool { return id == compartmentA })

	rec := do(t, h, http.MethodPost, "/20190828/tables", map[string]any{
		"compartmentId": "ocid1.compartment.oc1..doesnotexist",
		"name":          "users",
		"ddlStatement":  usersDDL,
		"tableLimits":   map[string]any{"capacityMode": "ON_DEMAND"},
	})
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())

	createTable(t, h)

	rec = do(t, h, http.MethodPost, "/20190828/tables/users/actions/changeCompartment", map[string]any{
		"fromCompartmentId": compartmentA, "toCompartmentId": "ocid1.compartment.oc1..doesnotexist",
	})
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}

func TestPreparedQueriesAreRefused(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	rec := do(t, h, http.MethodPost, "/20190828/query", map[string]any{
		"compartmentId": compartmentA, "statement": "SELECT * FROM users", "isPrepared": true,
	})
	assert.Equal(t, http.StatusNotImplemented, rec.Code, rec.Body.String())
}

func TestTableNamesAreCaseInsensitive(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	rec := do(t, h, http.MethodGet, "/20190828/tables/USERS?compartmentId="+compartmentA, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"name":"users"`, "the declared spelling is kept")

	rec = do(t, h, http.MethodPost, "/20190828/query", map[string]any{
		"compartmentId": compartmentA, "statement": "SELECT * FROM Users",
	})
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// UpdateRow's ttl replaces the table TTL unless isTtlUseTableDefault is set.
func TestUpdateRowTTL(t *testing.T) {
	h, _ := newHandler(t)

	rec := do(t, h, http.MethodPost, "/20190828/tables", map[string]any{
		"name":          "sessions",
		"compartmentId": compartmentA,
		"ddlStatement":  "CREATE TABLE sessions (id STRING, PRIMARY KEY (id)) USING TTL 2 DAYS",
		"tableLimits":   map[string]any{"capacityMode": "ON_DEMAND"},
	})
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		id     string
		extra  map[string]any
		expect time.Time
	}{
		{id: "a", extra: map[string]any{"ttl": 5, "isTtlUseTableDefault": false}, expect: now.Add(120 * time.Hour)},
		{id: "b", extra: map[string]any{"ttl": 5, "isTtlUseTableDefault": true}, expect: now.Add(48 * time.Hour)},
		{id: "c", extra: map[string]any{}, expect: now.Add(48 * time.Hour)},
	}

	for _, tc := range tests {
		body := map[string]any{"compartmentId": compartmentA, "value": map[string]any{"id": tc.id}}
		for k, v := range tc.extra {
			body[k] = v
		}

		rec = do(t, h, http.MethodPut, "/20190828/tables/sessions/rows", body)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		rec = do(t, h, http.MethodGet, "/20190828/tables/sessions/rows?compartmentId="+compartmentA+"&key=id:"+tc.id, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var row struct {
			TimeOfExpiration string `json:"timeOfExpiration"`
		}

		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &row))
		assert.Equal(t, tc.expect.Format(time.RFC3339), row.TimeOfExpiration, tc.id)
	}
}

// Without isExactMatch, OCI writes a row carrying fields the table does not
// declare and drops them.
func TestUpdateRowDropsUndeclaredFields(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	rec := do(t, h, http.MethodPut, "/20190828/tables/users/rows", map[string]any{
		"compartmentId": compartmentA,
		"value":         map[string]any{"id": 1, "email": "a@b.c", "extra": "x"},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = do(t, h, http.MethodGet, "/20190828/tables/users/rows?compartmentId="+compartmentA+"&key=id:1&key=email:a@b.c", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "extra")
}

func TestJSONPathIndexKeysAreRefused(t *testing.T) {
	h, _ := newHandler(t)
	createTable(t, h)

	for _, key := range []map[string]any{
		{"columnName": "name", "jsonPath": "city"},
		{"columnName": "name", "jsonFieldType": "STRING"},
	} {
		rec := do(t, h, http.MethodPost, "/20190828/tables/users/indexes", map[string]any{
			"compartmentId": compartmentA, "name": "byCity", "keys": []map[string]any{key},
		})
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "JSON-path index keys")
	}

	rec := do(t, h, http.MethodGet, "/20190828/tables/users/indexes/byCity?compartmentId="+compartmentA, nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// A LONG above 2^53 is written and addressed exactly over the wire.
func TestLongValuesAreExactOverTheWire(t *testing.T) {
	h, _ := newHandler(t)

	rec := do(t, h, http.MethodPost, "/20190828/tables", map[string]any{
		"name":          "ids",
		"compartmentId": compartmentA,
		"ddlStatement":  "CREATE TABLE ids (id LONG, v STRING, PRIMARY KEY (id))",
		"tableLimits":   map[string]any{"capacityMode": "ON_DEMAND"},
	})
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

	put := httptest.NewRecorder()
	h.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/20190828/tables/ids/rows", strings.NewReader(
		`{"compartmentId":"`+compartmentA+`","value":{"id":9007199254740993,"v":"x"}}`)))
	require.Equal(t, http.StatusOK, put.Code, put.Body.String())

	rec = do(t, h, http.MethodGet, "/20190828/tables/ids/rows?compartmentId="+compartmentA+"&key=id:9007199254740993", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"id":9007199254740993`)

	rec = do(t, h, http.MethodGet, "/20190828/tables/ids/rows?compartmentId="+compartmentA+"&key=id:9007199254740992", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// OCI's Index model names the table it belongs to.
func TestIndexCarriesItsTable(t *testing.T) {
	h, _ := newHandler(t)
	id := createTable(t, h)

	rec := do(t, h, http.MethodPost, "/20190828/tables/users/indexes", map[string]any{
		"compartmentId": compartmentA, "name": "byName", "keys": []map[string]any{{"columnName": "name"}},
	})
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

	type index struct {
		CompartmentID string `json:"compartmentId"`
		TableName     string `json:"tableName"`
		TableID       string `json:"tableId"`
	}

	expect := index{CompartmentID: compartmentA, TableName: "users", TableID: id}

	rec = do(t, h, http.MethodGet, "/20190828/tables/users/indexes/byName?compartmentId="+compartmentA, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var got index
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, expect, got)

	rec = do(t, h, http.MethodGet, "/20190828/tables/users/indexes?compartmentId="+compartmentA, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var list struct {
		Items []index `json:"items"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Items, 1)
	assert.Equal(t, expect, list.Items[0])
}

// ListTables sorts by timeCreated DESC by default, honours sortBy and
// sortOrder, filters on lifecycleState and refuses values outside the enums.
func TestListTablesSortAndLifecycleState(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clock), config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(compartmentA))
	h := ocinosql.New(nosqlprovider.New(opts), workrequest.New(opts))

	for _, name := range []string{"b", "c", "a"} {
		rec := do(t, h, http.MethodPost, "/20190828/tables", map[string]any{
			"name":          name,
			"compartmentId": compartmentA,
			"ddlStatement":  "CREATE TABLE " + name + " (k STRING, PRIMARY KEY (k))",
			"tableLimits":   map[string]any{"capacityMode": "ON_DEMAND"},
		})
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		clock.Advance(time.Minute)
	}

	names := func(query string) (int, []string) {
		rec := do(t, h, http.MethodGet, "/20190828/tables?compartmentId="+compartmentA+query, nil)

		var list struct {
			Items []struct {
				Name string `json:"name"`
			} `json:"items"`
		}

		_ = json.Unmarshal(rec.Body.Bytes(), &list)

		out := make([]string, 0, len(list.Items))
		for _, it := range list.Items {
			out = append(out, it.Name)
		}

		return rec.Code, out
	}

	tests := []struct {
		name         string
		query        string
		expectStatus int
		expect       []string
	}{
		{name: "default is timeCreated descending", expectStatus: http.StatusOK, expect: []string{"a", "c", "b"}},
		{name: "timeCreated ascending", query: "&sortBy=timeCreated&sortOrder=ASC", expectStatus: http.StatusOK, expect: []string{"b", "c", "a"}},
		{name: "name ascending", query: "&sortBy=name&sortOrder=ASC", expectStatus: http.StatusOK, expect: []string{"a", "b", "c"}},
		{name: "name descending", query: "&sortBy=name", expectStatus: http.StatusOK, expect: []string{"c", "b", "a"}},
		{name: "active", query: "&lifecycleState=ACTIVE&sortBy=name&sortOrder=ASC", expectStatus: http.StatusOK, expect: []string{"a", "b", "c"}},
		{name: "all", query: "&lifecycleState=ALL&sortBy=name&sortOrder=ASC", expectStatus: http.StatusOK, expect: []string{"a", "b", "c"}},
		{name: "deleted matches no active table", query: "&lifecycleState=DELETED", expectStatus: http.StatusOK, expect: []string{}},
		{name: "unknown sortBy", query: "&sortBy=size", expectStatus: http.StatusBadRequest, expect: []string{}},
		{name: "unknown sortOrder", query: "&sortOrder=UP", expectStatus: http.StatusBadRequest, expect: []string{}},
		{name: "unknown lifecycleState", query: "&lifecycleState=GONE", expectStatus: http.StatusBadRequest, expect: []string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, got := names(tc.query)
			assert.Equal(t, tc.expectStatus, status)
			assert.Equal(t, tc.expect, got)
		})
	}
}

func TestProvisionedTableNeedsStorage(t *testing.T) {
	h, _ := newHandler(t)

	rec := do(t, h, http.MethodPost, "/20190828/tables", map[string]any{
		"name":          "users",
		"compartmentId": compartmentA,
		"ddlStatement":  usersDDL,
		"tableLimits": map[string]any{
			"maxReadUnits": 50, "maxWriteUnits": 50, "maxStorageInGBs": 0, "capacityMode": "PROVISIONED",
		},
	})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "maxStorageInGBs")
}

// DEFAULT ” is reported as a default and fills a missing value; ALTER ADD
// fills existing rows; isExactMatch refuses missing columns; out-of-range
// integers are refused.
func TestRowDefaultsAndRangesOverTheWire(t *testing.T) {
	h, _ := newHandler(t)

	rec := do(t, h, http.MethodPost, "/20190828/tables", map[string]any{
		"name":          "notes",
		"compartmentId": compartmentA,
		"ddlStatement":  "CREATE TABLE notes (id LONG, s STRING NOT NULL DEFAULT '', PRIMARY KEY (id))",
		"tableLimits":   map[string]any{"capacityMode": "ON_DEMAND"},
	})
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

	rec = do(t, h, http.MethodGet, "/20190828/tables/notes?compartmentId="+compartmentA, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"defaultValue":""`)

	rec = do(t, h, http.MethodPut, "/20190828/tables/notes/rows", map[string]any{
		"compartmentId": compartmentA, "value": map[string]any{"id": 1},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = do(t, h, http.MethodPut, "/20190828/tables/notes", map[string]any{
		"ddlStatement": "ALTER TABLE notes (ADD k INTEGER NOT NULL DEFAULT 3)",
	})
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

	rec = do(t, h, http.MethodGet, "/20190828/tables/notes/rows?compartmentId="+compartmentA+"&key=id:1", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"s":""`)
	assert.Contains(t, rec.Body.String(), `"k":3`)

	rec = do(t, h, http.MethodPut, "/20190828/tables/notes/rows", map[string]any{
		"compartmentId": compartmentA, "value": map[string]any{"id": 2, "s": "x"}, "isExactMatch": true,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `column \"k\" is missing`)

	put := httptest.NewRecorder()
	h.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/20190828/tables/notes/rows", strings.NewReader(
		`{"compartmentId":"`+compartmentA+`","value":{"id":9223372036854775808}}`)))
	require.Equal(t, http.StatusBadRequest, put.Code, put.Body.String())
}
