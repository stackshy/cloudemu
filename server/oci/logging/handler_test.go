package logging_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	logprovider "github.com/stackshy/cloudemu/v2/providers/oci/logging"
	ocilogging "github.com/stackshy/cloudemu/v2/server/oci/logging"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// The mock must satisfy the handler's OCI-only capability interface.
var _ ocilogging.Extras = (*logprovider.Mock)(nil)

const (
	compartmentA = "ocid1.compartment.oc1..aaaaaaaacompa"
	compartmentB = "ocid1.compartment.oc1..aaaaaaaacompb"
)

func newOptions() *config.Options {
	return config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC))),
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(compartmentA),
	)
}

func newHandler(t *testing.T) (*ocilogging.Handler, *workrequest.Store) {
	t.Helper()

	opts := newOptions()
	work := workrequest.New(opts)

	return ocilogging.New(logprovider.New(opts), work), work
}

// do runs one request through the handler.
func do(t *testing.T, h *ocilogging.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader

	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, reader))

	return rec
}

// createGroup creates a log group over the wire and returns its OCID.
func createGroup(t *testing.T, h *ocilogging.Handler, work *workrequest.Store, name string) string {
	t.Helper()

	rec := do(t, h, http.MethodPost, "/20200531/logGroups", map[string]any{
		"compartmentId": compartmentA,
		"displayName":   name,
	})
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

	return resourceOf(t, work, rec, "loggroup")
}

// createLog creates a custom log over the wire and returns its OCID.
func createLog(t *testing.T, h *ocilogging.Handler, work *workrequest.Store, groupID, name string) string {
	t.Helper()

	rec := do(t, h, http.MethodPost, "/20200531/logGroups/"+groupID+"/logs", map[string]any{
		"displayName": name,
		"logType":     "CUSTOM",
	})
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

	return resourceOf(t, work, rec, "log")
}

// resourceOf reads the created resource's OCID out of the work request a 202
// stamps, which is the only place an asynchronous create reports it.
func resourceOf(
	t *testing.T, work *workrequest.Store, rec *httptest.ResponseRecorder, entityType string,
) string {
	t.Helper()

	id := rec.Header().Get(ocirest.HeaderWorkRequestID)
	require.NotEmpty(t, id, "an asynchronous mutation must stamp opc-work-request-id")

	wr, ok := work.Get(id)
	require.True(t, ok, "the stamped work request must be pollable")
	require.Len(t, wr.Resources, 1)
	assert.Equal(t, entityType, wr.Resources[0].EntityType)
	assert.Equal(t, workrequest.StatusSucceeded, wr.Status)

	return wr.Resources[0].Identifier
}

// TestMatches is the core of this handler: three OCI API surfaces collapse
// onto one server, so each prefix must be claimed exactly and nothing else.
func TestMatches(t *testing.T) {
	h, _ := newHandler(t)

	tests := []struct {
		name   string
		method string
		path   string
		expect bool
	}{
		// Control plane, /20200531.
		{name: "log group collection", method: http.MethodPost, path: "/20200531/logGroups", expect: true},
		{name: "log group list", method: http.MethodGet, path: "/20200531/logGroups?compartmentId=c", expect: true},
		{name: "single log group", method: http.MethodGet, path: "/20200531/logGroups/ocid1.loggroup.oc1.iad.a", expect: true},
		{name: "nested log collection", method: http.MethodGet, path: "/20200531/logGroups/g/logs", expect: true},
		{name: "single nested log", method: http.MethodGet, path: "/20200531/logGroups/g/logs/l", expect: true},
		{name: "change compartment action", method: http.MethodPost, path: "/20200531/logGroups/g/actions/changeCompartment", expect: true},
		{name: "unified agent configurations are claimed to be reported unemulated", method: http.MethodGet, path: "/20200531/unifiedAgentConfigurations", expect: true},
		{name: "saved searches are claimed to be reported unemulated", method: http.MethodGet, path: "/20200531/logSavedSearches", expect: true},

		// Ingestion plane, /20200601.
		{name: "ingestion push", method: http.MethodPost, path: "/20200601/logs/ocid1.log.oc1.iad.a/actions/push", expect: true},

		// Search plane, /20190909.
		{name: "search", method: http.MethodPost, path: "/20190909/search", expect: true},

		// A collection claimed under one prefix must not be claimed under another.
		{name: "top-level logs is the ingestion plane's, not the control plane's", method: http.MethodGet, path: "/20200531/logs", expect: false},
		{name: "log groups are not on the ingestion prefix", method: http.MethodPost, path: "/20200601/logGroups", expect: false},
		{name: "search is not on the ingestion prefix", method: http.MethodPost, path: "/20200601/search", expect: false},
		{name: "log groups are not on the search prefix", method: http.MethodGet, path: "/20190909/logGroups", expect: false},
		{name: "logs are not on the search prefix", method: http.MethodPost, path: "/20190909/logs/a/actions/push", expect: false},
		{name: "search is not on the control prefix", method: http.MethodPost, path: "/20200531/search", expect: false},
		{name: "search takes no id", method: http.MethodPost, path: "/20190909/search/abc", expect: false},

		// Other services keep their traffic.
		{name: "core networking", method: http.MethodGet, path: "/20160918/vcns", expect: false},
		{name: "monitoring", method: http.MethodPost, path: "/20180401/metrics", expect: false},
		{name: "object storage", method: http.MethodGet, path: "/n/tenancy/b/bucket/o/key", expect: false},
		{name: "work requests belong to the shared poller", method: http.MethodGet, path: "/20200531/workRequests/abc", expect: false},

		// Malformed shapes.
		{name: "version alone", method: http.MethodGet, path: "/20200531", expect: false},
		{name: "root", method: http.MethodGet, path: "/", expect: false},
		{name: "unknown version", method: http.MethodGet, path: "/19990101/logGroups", expect: false},
		{name: "too many segments", method: http.MethodGet, path: "/20200531/logGroups/g/logs/l/extra", expect: false},
		{name: "empty segment", method: http.MethodGet, path: "/20200531//logGroups", expect: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			assert.Equal(t, tc.expect, h.Matches(req))
		})
	}
}

func TestLogGroupLifecycle(t *testing.T) {
	h, work := newHandler(t)

	groupID := createGroup(t, h, work, "app-logs")

	t.Run("get", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/20200531/logGroups/"+groupID, nil)
		require.Equal(t, http.StatusOK, rec.Code)

		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, groupID, body["id"])
		assert.Equal(t, "app-logs", body["displayName"])
		assert.Equal(t, compartmentA, body["compartmentId"])
		assert.Equal(t, "ACTIVE", body["lifecycleState"])
	})

	t.Run("list", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/20200531/logGroups?compartmentId="+compartmentA, nil)
		require.Equal(t, http.StatusOK, rec.Code)

		var body []map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Len(t, body, 1)
		assert.Equal(t, groupID, body[0]["id"])
	})

	t.Run("list requires compartmentId", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/20200531/logGroups", nil)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, "InvalidParameter", codeOf(t, rec))
	})

	t.Run("list in another compartment is empty", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/20200531/logGroups?compartmentId=ocid1.compartment.oc1..other", nil)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `[]`, rec.Body.String())
	})

	t.Run("update is asynchronous", func(t *testing.T) {
		rec := do(t, h, http.MethodPut, "/20200531/logGroups/"+groupID, map[string]any{"description": "the app"})
		require.Equal(t, http.StatusAccepted, rec.Code)
		assert.NotEmpty(t, rec.Header().Get(ocirest.HeaderWorkRequestID))
	})

	t.Run("change compartment", func(t *testing.T) {
		rec := do(t, h, http.MethodPost, "/20200531/logGroups/"+groupID+"/actions/changeCompartment",
			map[string]any{"targetCompartmentId": "ocid1.compartment.oc1..moved"})
		require.Equal(t, http.StatusAccepted, rec.Code)
		assert.NotEmpty(t, rec.Header().Get(ocirest.HeaderWorkRequestID))
	})

	t.Run("change compartment needs a target", func(t *testing.T) {
		rec := do(t, h, http.MethodPost, "/20200531/logGroups/"+groupID+"/actions/changeCompartment",
			map[string]any{})
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("delete is asynchronous", func(t *testing.T) {
		rec := do(t, h, http.MethodDelete, "/20200531/logGroups/"+groupID, nil)
		require.Equal(t, http.StatusAccepted, rec.Code)
		assert.NotEmpty(t, rec.Header().Get(ocirest.HeaderWorkRequestID))
	})

	t.Run("get after delete is 404", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/20200531/logGroups/"+groupID, nil)
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Equal(t, "NotAuthorizedOrNotFound", codeOf(t, rec))
	})
}

func TestLogGroupErrors(t *testing.T) {
	h, work := newHandler(t)
	createGroup(t, h, work, "taken")

	tests := []struct {
		name       string
		method     string
		path       string
		body       any
		expectCode int
	}{
		{
			name: "create without a compartment", method: http.MethodPost, path: "/20200531/logGroups",
			body: map[string]any{"displayName": "x"}, expectCode: http.StatusBadRequest,
		},
		{
			name: "create a duplicate", method: http.MethodPost, path: "/20200531/logGroups",
			body:       map[string]any{"compartmentId": compartmentA, "displayName": "taken"},
			expectCode: http.StatusConflict,
		},
		{
			name: "create without a display name", method: http.MethodPost, path: "/20200531/logGroups",
			body: map[string]any{"compartmentId": compartmentA}, expectCode: http.StatusBadRequest,
		},
		{
			name: "get an unknown group", method: http.MethodGet,
			path: "/20200531/logGroups/ocid1.loggroup.oc1.iad.missing", expectCode: http.StatusNotFound,
		},
		{
			name: "unsupported method on the collection", method: http.MethodDelete,
			path: "/20200531/logGroups", expectCode: http.StatusMethodNotAllowed,
		},
		{
			name: "unknown action", method: http.MethodPost,
			path: "/20200531/logGroups/g/actions/teleport", expectCode: http.StatusNotFound,
		},
		{
			name: "unknown sub-collection", method: http.MethodGet,
			path: "/20200531/logGroups/g/entries", expectCode: http.StatusNotFound,
		},
		{
			name: "unified agent configurations are reported unemulated", method: http.MethodGet,
			path: "/20200531/unifiedAgentConfigurations", expectCode: http.StatusNotImplemented,
		},
		{
			name: "saved searches are reported unemulated", method: http.MethodGet,
			path: "/20200531/logSavedSearches", expectCode: http.StatusNotImplemented,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, tc.method, tc.path, tc.body)
			assert.Equal(t, tc.expectCode, rec.Code, rec.Body.String())
			assert.NotEmpty(t, codeOf(t, rec))
		})
	}
}

func TestLogLifecycle(t *testing.T) {
	h, work := newHandler(t)
	groupID := createGroup(t, h, work, "app-logs")
	logID := createLog(t, h, work, groupID, "stdout")

	t.Run("get", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/20200531/logGroups/"+groupID+"/logs/"+logID, nil)
		require.Equal(t, http.StatusOK, rec.Code)

		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, logID, body["id"])
		assert.Equal(t, groupID, body["logGroupId"])
		assert.Equal(t, "CUSTOM", body["logType"])
		assert.Equal(t, true, body["isEnabled"], "an absent isEnabled must default to true")
	})

	t.Run("list", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/20200531/logGroups/"+groupID+"/logs", nil)
		require.Equal(t, http.StatusOK, rec.Code)

		var body []map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Len(t, body, 1)
	})

	t.Run("list narrows by log type", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/20200531/logGroups/"+groupID+"/logs?logType=SERVICE", nil)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `[]`, rec.Body.String())
	})

	t.Run("update is asynchronous", func(t *testing.T) {
		rec := do(t, h, http.MethodPut, "/20200531/logGroups/"+groupID+"/logs/"+logID,
			map[string]any{"retentionDuration": 90})
		require.Equal(t, http.StatusAccepted, rec.Code)
		assert.NotEmpty(t, rec.Header().Get(ocirest.HeaderWorkRequestID))
	})

	t.Run("delete is asynchronous", func(t *testing.T) {
		rec := do(t, h, http.MethodDelete, "/20200531/logGroups/"+groupID+"/logs/"+logID, nil)
		require.Equal(t, http.StatusAccepted, rec.Code)
		assert.NotEmpty(t, rec.Header().Get(ocirest.HeaderWorkRequestID))

		rec = do(t, h, http.MethodGet, "/20200531/logGroups/"+groupID+"/logs/"+logID, nil)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func TestLogErrors(t *testing.T) {
	h, work := newHandler(t)
	groupID := createGroup(t, h, work, "app-logs")

	tests := []struct {
		name       string
		method     string
		path       string
		body       any
		expectCode int
	}{
		{
			name: "create in an unknown group", method: http.MethodPost,
			path: "/20200531/logGroups/ocid1.loggroup.oc1.iad.missing/logs",
			body: map[string]any{"displayName": "x"}, expectCode: http.StatusNotFound,
		},
		{
			name: "create a service log without a source", method: http.MethodPost,
			path: "/20200531/logGroups/" + groupID + "/logs",
			body: map[string]any{"displayName": "flow", "logType": "SERVICE"}, expectCode: http.StatusBadRequest,
		},
		{
			name: "create with an unknown log type", method: http.MethodPost,
			path: "/20200531/logGroups/" + groupID + "/logs",
			body: map[string]any{"displayName": "x", "logType": "WEIRD"}, expectCode: http.StatusBadRequest,
		},
		{
			name: "list in an unknown group", method: http.MethodGet,
			path: "/20200531/logGroups/ocid1.loggroup.oc1.iad.missing/logs", expectCode: http.StatusNotFound,
		},
		{
			name: "unsupported method on a log", method: http.MethodPatch,
			path: "/20200531/logGroups/" + groupID + "/logs/x", expectCode: http.StatusMethodNotAllowed,
		},
		{
			name: "malformed body", method: http.MethodPost,
			path: "/20200531/logGroups/" + groupID + "/logs", body: nil, expectCode: http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, tc.method, tc.path, tc.body)
			assert.Equal(t, tc.expectCode, rec.Code, rec.Body.String())
		})
	}
}

func TestPutLogs(t *testing.T) {
	h, work := newHandler(t)
	groupID := createGroup(t, h, work, "app-logs")
	logID := createLog(t, h, work, groupID, "stdout")

	push := func(body any) *httptest.ResponseRecorder {
		return do(t, h, http.MethodPost, "/20200601/logs/"+logID+"/actions/push", body)
	}

	t.Run("success", func(t *testing.T) {
		rec := push(map[string]any{
			"specversion": "1.0",
			"logEntryBatches": []any{map[string]any{
				"source": "host-a",
				"type":   "custom",
				"entries": []any{
					map[string]any{"data": "hello", "id": "e1", "time": "2026-08-08T10:00:00Z"},
				},
			}},
		})
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})

	tests := []struct {
		name       string
		path       string
		method     string
		body       any
		expectCode int
	}{
		{
			name: "specversion is required", method: http.MethodPost,
			path: "/20200601/logs/" + logID + "/actions/push",
			body: map[string]any{"logEntryBatches": []any{}}, expectCode: http.StatusBadRequest,
		},
		{
			name: "unreadable timestamp", method: http.MethodPost,
			path: "/20200601/logs/" + logID + "/actions/push",
			body: map[string]any{
				"specversion":     "1.0",
				"logEntryBatches": []any{map[string]any{"entries": []any{map[string]any{"data": "x", "time": "yesterday"}}}},
			},
			expectCode: http.StatusBadRequest,
		},
		{
			name: "unknown log", method: http.MethodPost,
			path: "/20200601/logs/ocid1.log.oc1.iad.missing/actions/push",
			body: map[string]any{"specversion": "1.0"}, expectCode: http.StatusNotFound,
		},
		{
			name: "the ingestion plane publishes only push", method: http.MethodPost,
			path: "/20200601/logs/" + logID + "/actions/pull",
			body: map[string]any{"specversion": "1.0"}, expectCode: http.StatusNotFound,
		},
		{
			name: "the ingestion plane has no collection", method: http.MethodGet,
			path: "/20200601/logs", expectCode: http.StatusNotFound,
		},
		{
			name: "push is POST only", method: http.MethodGet,
			path: "/20200601/logs/" + logID + "/actions/push", expectCode: http.StatusMethodNotAllowed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, tc.method, tc.path, tc.body)
			assert.Equal(t, tc.expectCode, rec.Code, rec.Body.String())
		})
	}
}

func TestSearchLogs(t *testing.T) {
	h, work := newHandler(t)
	groupID := createGroup(t, h, work, "app-logs")
	logID := createLog(t, h, work, groupID, "stdout")

	rec := do(t, h, http.MethodPost, "/20200601/logs/"+logID+"/actions/push", map[string]any{
		"specversion": "1.0",
		"logEntryBatches": []any{map[string]any{
			"source": "host-a",
			"type":   "custom",
			"entries": []any{
				map[string]any{"data": `{"level":"ERROR","msg":"boom"}`, "id": "e1", "time": "2026-08-08T10:00:00Z"},
				map[string]any{"data": `{"level":"INFO","msg":"fine"}`, "id": "e2", "time": "2026-08-08T10:05:00Z"},
			},
		}},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	search := func(query string) *httptest.ResponseRecorder {
		return do(t, h, http.MethodPost, "/20190909/search", map[string]any{
			"searchQuery": query,
			"timeStart":   "2026-08-08T09:00:00Z",
			"timeEnd":     "2026-08-08T11:00:00Z",
		})
	}

	t.Run("whole compartment", func(t *testing.T) {
		res := search(`search "` + compartmentA + `"`)
		require.Equal(t, http.StatusOK, res.Code, res.Body.String())

		var body map[string]any
		require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
		assert.Len(t, body["results"], 2)
	})

	t.Run("narrowed to one log", func(t *testing.T) {
		res := search(`search "` + compartmentA + "/" + groupID + "/" + logID + `"`)
		require.Equal(t, http.StatusOK, res.Code, res.Body.String())

		var body map[string]any
		require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
		assert.Len(t, body["results"], 2)
	})

	t.Run("where on a JSON payload field", func(t *testing.T) {
		res := search(`search "` + compartmentA + `" | where data.level = 'ERROR'`)
		require.Equal(t, http.StatusOK, res.Code, res.Body.String())

		var body map[string]any
		require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
		require.Len(t, body["results"], 1)

		content := body["results"].([]any)[0].(map[string]any)["data"].(map[string]any)["logContent"].(map[string]any)
		assert.Equal(t, "e1", content["id"])
		assert.Equal(t, "ERROR", content["data"].(map[string]any)["level"])
		assert.Equal(t, logID, content["oracle"].(map[string]any)["logid"])
	})

	t.Run("wildcards", func(t *testing.T) {
		res := search(`search "` + compartmentA + `" | where data = '*boom*'`)
		require.Equal(t, http.StatusOK, res.Code, res.Body.String())

		var body map[string]any
		require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
		assert.Len(t, body["results"], 1)
	})

	t.Run("a search outside the time range returns nothing", func(t *testing.T) {
		res := do(t, h, http.MethodPost, "/20190909/search", map[string]any{
			"searchQuery": `search "` + compartmentA + `"`,
			"timeStart":   "2026-08-07T00:00:00Z",
			"timeEnd":     "2026-08-07T23:59:59Z",
		})
		require.Equal(t, http.StatusOK, res.Code)

		var body map[string]any
		require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
		assert.Empty(t, body["results"])
	})

	t.Run("field info on request", func(t *testing.T) {
		res := do(t, h, http.MethodPost, "/20190909/search", map[string]any{
			"searchQuery":       `search "` + compartmentA + `"`,
			"timeStart":         "2026-08-08T09:00:00Z",
			"timeEnd":           "2026-08-08T11:00:00Z",
			"isReturnFieldInfo": true,
		})
		require.Equal(t, http.StatusOK, res.Code)

		var body map[string]any
		require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
		assert.NotEmpty(t, body["fields"])
	})
}

// TestSearchRejectsWhatItDoesNotModel is the guard against the accept-and-
// return-nothing failure mode: every unmodelled query names what it tripped on.
func TestSearchRejectsWhatItDoesNotModel(t *testing.T) {
	h, work := newHandler(t)
	groupID := createGroup(t, h, work, "app-logs")
	createLog(t, h, work, groupID, "stdout")

	tests := []struct {
		name        string
		query       string
		expectNamed string
	}{
		{name: "summarize", query: `search "` + compartmentA + `" | summarize count() by data.level`, expectNamed: "summarize"},
		{name: "stats", query: `search "` + compartmentA + `" | stats count()`, expectNamed: "stats"},
		{name: "topN", query: `search "` + compartmentA + `" | topN 5 by data.level`, expectNamed: "topn"},
		{name: "extract", query: `search "` + compartmentA + `" | extract '(\d+)'`, expectNamed: "extract"},
		{name: "or in a where clause", query: `search "` + compartmentA + `" | where data = 'a' or data = 'b'`, expectNamed: `the \"or\" operator`},
		{name: "not in a where clause", query: `search "` + compartmentA + `" | where not data = 'a'`, expectNamed: `the \"not\" operator`},
		{name: "parenthesized where", query: `search "` + compartmentA + `" | where (data = 'a')`, expectNamed: "parenthesized"},
		{name: "greater than", query: `search "` + compartmentA + `" | where data.count > 3`, expectNamed: "operator is not modeled"},
		{name: "regex match operator", query: `search "` + compartmentA + `" | where data =~ 'a.*'`, expectNamed: "=~"},
		{name: "unknown field", query: `search "` + compartmentA + `" | where data.level.nested.deep = 'a'`, expectNamed: "unsupported search field"},
		{name: "sort by another field", query: `search "` + compartmentA + `" | sort by data.level desc`, expectNamed: "sorts by datetime only"},
		{name: "sort with a bad direction", query: `search "` + compartmentA + `" | sort by datetime sideways`, expectNamed: "asc or desc"},
		{name: "no search clause", query: `where data = 'a'`, expectNamed: "must begin with the search clause"},
		{name: "an unquoted target", query: `search ` + compartmentA, expectNamed: "must be quoted"},
		{name: "a name where an OCID belongs", query: `search "app-logs"`, expectNamed: "compartment OCID"},
		{name: "a log group name in the second segment", query: `search "` + compartmentA + `/app-logs"`, expectNamed: "log group OCID"},
		{name: "too many scope segments", query: `search "` + compartmentA + `/g/l/x"`, expectNamed: "segments"},
		{name: "a comparison with no operator", query: `search "` + compartmentA + `" | where data`, expectNamed: "no operator"},
		{name: "an empty query", query: ``, expectNamed: "required"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/20190909/search", map[string]any{
				"searchQuery": tc.query,
				"timeStart":   "2026-08-08T09:00:00Z",
				"timeEnd":     "2026-08-08T11:00:00Z",
			})

			require.Equal(t, http.StatusBadRequest, rec.Code, "an unmodelled query must be rejected, not answered empty: %s", rec.Body.String())
			assert.Contains(t, strings.ToLower(rec.Body.String()), strings.ToLower(tc.expectNamed))
		})
	}
}

func TestSearchRequestErrors(t *testing.T) {
	h, _ := newHandler(t)

	tests := []struct {
		name       string
		method     string
		body       any
		expectCode int
	}{
		{
			name: "missing time range", method: http.MethodPost,
			body:       map[string]any{"searchQuery": `search "` + compartmentA + `"`},
			expectCode: http.StatusBadRequest,
		},
		{
			name: "end before start", method: http.MethodPost,
			body: map[string]any{
				"searchQuery": `search "` + compartmentA + `"`,
				"timeStart":   "2026-08-08T11:00:00Z",
				"timeEnd":     "2026-08-08T09:00:00Z",
			},
			expectCode: http.StatusBadRequest,
		},
		{
			name: "unreadable timestamp", method: http.MethodPost,
			body: map[string]any{
				"searchQuery": `search "` + compartmentA + `"`,
				"timeStart":   "yesterday",
				"timeEnd":     "2026-08-08T09:00:00Z",
			},
			expectCode: http.StatusBadRequest,
		},
		{name: "search is POST only", method: http.MethodGet, expectCode: http.StatusMethodNotAllowed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, tc.method, "/20190909/search", tc.body)
			assert.Equal(t, tc.expectCode, rec.Code, rec.Body.String())
		})
	}
}

// portableOnly implements the portable driver without the OCI capability, so
// the handler has nothing to serve OCI's operations with.
type portableOnly struct {
	logdriver.Logging
}

func TestDriverWithoutOCICapabilityIs501(t *testing.T) {
	h := ocilogging.New(portableOnly{}, workrequest.New(newOptions()))

	for _, path := range []string{
		"/20200531/logGroups",
		"/20200601/logs/l/actions/push",
		"/20190909/search",
	} {
		t.Run(path, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, path, map[string]any{})
			assert.Equal(t, http.StatusNotImplemented, rec.Code)
			assert.Equal(t, "NotImplemented", codeOf(t, rec))
		})
	}
}

func TestWorkRequestsUnconfigured(t *testing.T) {
	h := ocilogging.New(logprovider.New(newOptions()), nil)

	rec := do(t, h, http.MethodPost, "/20200531/logGroups",
		map[string]any{"compartmentId": compartmentA, "displayName": "x"})
	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

// codeOf reads the OCI error code out of a response body.
func codeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var body ocirest.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		return ""
	}

	return body.Code
}

// resultIDs reads the entry ids out of a search response, in result order.
func resultIDs(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()

	var body struct {
		Results []struct {
			Data struct {
				LogContent struct {
					ID     string `json:"id"`
					Oracle struct {
						CompartmentID string `json:"compartmentid"`
						LogGroupID    string `json:"loggroupid"`
						LogID         string `json:"logid"`
						IngestedTime  string `json:"ingestedtime"`
					} `json:"oracle"`
				} `json:"logContent"`
			} `json:"data"`
		} `json:"results"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	ids := make([]string, 0, len(body.Results))
	for i := range body.Results {
		ids = append(ids, body.Results[i].Data.LogContent.ID)
	}

	return ids
}

// TestSearchSortAndProvenance is the positive counterpart to the rejection
// table: a successful sort must come back in the asked-for order, and an
// oracle.* comparison must resolve against the log an entry came from.
func TestSearchSortAndProvenance(t *testing.T) {
	h, work := newHandler(t)
	groupID := createGroup(t, h, work, "app-logs")
	stdout := createLog(t, h, work, groupID, "stdout")
	stderr := createLog(t, h, work, groupID, "stderr")

	push := func(logID string, entries ...any) {
		t.Helper()

		rec := do(t, h, http.MethodPost, "/20200601/logs/"+logID+"/actions/push", map[string]any{
			"specversion":     "1.0",
			"logEntryBatches": []any{map[string]any{"source": "host-a", "type": "custom", "entries": entries}},
		})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}

	// Pushed out of time order, so a sorted result cannot pass by accident.
	push(stdout,
		map[string]any{"data": "third", "id": "e3", "time": "2026-08-08T10:30:00Z"},
		map[string]any{"data": "first", "id": "e1", "time": "2026-08-08T10:10:00Z"},
		map[string]any{"data": "second", "id": "e2", "time": "2026-08-08T10:20:00Z"},
	)
	push(stderr, map[string]any{"data": "fourth", "id": "e4", "time": "2026-08-08T10:40:00Z"})

	search := func(query string) *httptest.ResponseRecorder {
		rec := do(t, h, http.MethodPost, "/20190909/search", map[string]any{
			"searchQuery": query,
			"timeStart":   "2026-08-08T09:00:00Z",
			"timeEnd":     "2026-08-08T11:00:00Z",
		})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		return rec
	}

	all := `search "` + compartmentA + `"`

	tests := []struct {
		name   string
		query  string
		expect []string
	}{
		{name: "no sort clause is ascending", query: all, expect: []string{"e1", "e2", "e3", "e4"}},
		{name: "sort by datetime asc", query: all + ` | sort by datetime asc`, expect: []string{"e1", "e2", "e3", "e4"}},
		{name: "sort by datetime desc", query: all + ` | sort by datetime desc`, expect: []string{"e4", "e3", "e2", "e1"}},
		{name: "sort by time desc", query: all + ` | sort by time desc`, expect: []string{"e4", "e3", "e2", "e1"}},
		{
			name:   "where oracle.logid",
			query:  all + ` | where oracle.logid = '` + stderr + `'`,
			expect: []string{"e4"},
		},
		{
			name:   "where oracle.logid negated",
			query:  all + ` | where oracle.logid != '` + stderr + `'`,
			expect: []string{"e1", "e2", "e3"},
		},
		{
			name:   "where oracle.loggroupid",
			query:  all + ` | where oracle.loggroupid = '` + groupID + `'`,
			expect: []string{"e1", "e2", "e3", "e4"},
		},
		{
			name:   "where oracle.compartmentid",
			query:  all + ` | where oracle.compartmentid = '` + compartmentA + `'`,
			expect: []string{"e1", "e2", "e3", "e4"},
		},
		{
			name:   "where oracle.ingestedtime",
			query:  all + ` | where oracle.ingestedtime = '2026-08-08T12:00:00Z'`,
			expect: []string{"e1", "e2", "e3", "e4"},
		},
		{
			name:   "where and sort together",
			query:  all + ` | where oracle.loggroupid = '` + groupID + `' | sort by datetime desc`,
			expect: []string{"e4", "e3", "e2", "e1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, resultIDs(t, search(tc.query)))
		})
	}

	t.Run("the oracle block carries the log an entry came from", func(t *testing.T) {
		rec := search(all + ` | where oracle.logid = '` + stderr + `'`)

		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Len(t, body["results"], 1)

		content := body["results"].([]any)[0].(map[string]any)["data"].(map[string]any)["logContent"].(map[string]any)
		oracle := content["oracle"].(map[string]any)
		assert.Equal(t, compartmentA, oracle["compartmentid"])
		assert.Equal(t, groupID, oracle["loggroupid"])
		assert.Equal(t, stderr, oracle["logid"])
		assert.Equal(t, "2026-08-08T12:00:00Z", oracle["ingestedtime"])
	})

	t.Run("a non-JSON payload comes back as the raw string", func(t *testing.T) {
		rec := search(all + ` | where id = 'e1'`)

		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Len(t, body["results"], 1)

		content := body["results"].([]any)[0].(map[string]any)["data"].(map[string]any)["logContent"].(map[string]any)
		assert.Equal(t, "first", content["data"])
	})

	t.Run("the limit truncates after sorting", func(t *testing.T) {
		rec := do(t, h, http.MethodPost, "/20190909/search?limit=2", map[string]any{
			"searchQuery": all + ` | sort by datetime desc`,
			"timeStart":   "2026-08-08T09:00:00Z",
			"timeEnd":     "2026-08-08T11:00:00Z",
		})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, []string{"e4", "e3"}, resultIDs(t, rec))
	})
}

func TestLogGroupMutations(t *testing.T) {
	h, work := newHandler(t)
	groupID := createGroup(t, h, work, "app-logs")

	t.Run("update", func(t *testing.T) {
		rec := do(t, h, http.MethodPut, "/20200531/logGroups/"+groupID, map[string]any{
			"displayName":  "renamed",
			"description":  "the app's logs",
			"freeformTags": map[string]string{"env": "dev"},
		})
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

		got := do(t, h, http.MethodGet, "/20200531/logGroups/"+groupID, nil)
		require.Equal(t, http.StatusOK, got.Code)

		var body map[string]any
		require.NoError(t, json.Unmarshal(got.Body.Bytes(), &body))
		assert.Equal(t, "renamed", body["displayName"])
		assert.Equal(t, "the app's logs", body["description"])
		assert.Equal(t, map[string]any{"env": "dev"}, body["freeformTags"])
	})

	t.Run("move between compartments", func(t *testing.T) {
		rec := do(t, h, http.MethodPost,
			"/20200531/logGroups/"+groupID+"/actions/changeCompartment",
			map[string]any{"targetCompartmentId": compartmentB})
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

		got := do(t, h, http.MethodGet, "/20200531/logGroups/"+groupID, nil)

		var body map[string]any
		require.NoError(t, json.Unmarshal(got.Body.Bytes(), &body))
		assert.Equal(t, compartmentB, body["compartmentId"])
	})

	t.Run("delete", func(t *testing.T) {
		rec := do(t, h, http.MethodDelete, "/20200531/logGroups/"+groupID, nil)
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

		got := do(t, h, http.MethodGet, "/20200531/logGroups/"+groupID, nil)
		assert.Equal(t, http.StatusNotFound, got.Code)
	})
}

func TestLogGroupMutationErrors(t *testing.T) {
	h, work := newHandler(t)
	groupID := createGroup(t, h, work, "app-logs")
	missing := "ocid1.loggroup.oc1.iad.missing"

	tests := []struct {
		name       string
		method     string
		path       string
		body       any
		expectCode int
	}{
		{
			name: "update an unknown group", method: http.MethodPut,
			path: "/20200531/logGroups/" + missing, body: map[string]any{"description": "x"},
			expectCode: http.StatusNotFound,
		},
		{
			name: "delete an unknown group", method: http.MethodDelete,
			path: "/20200531/logGroups/" + missing, expectCode: http.StatusNotFound,
		},
		{
			name: "move an unknown group", method: http.MethodPost,
			path: "/20200531/logGroups/" + missing + "/actions/changeCompartment",
			body: map[string]any{"targetCompartmentId": compartmentB}, expectCode: http.StatusNotFound,
		},
		{
			name: "move needs a target compartment", method: http.MethodPost,
			path: "/20200531/logGroups/" + groupID + "/actions/changeCompartment",
			body: map[string]any{}, expectCode: http.StatusBadRequest,
		},
		{
			name: "an unknown group action", method: http.MethodPost,
			path: "/20200531/logGroups/" + groupID + "/actions/archive",
			body: map[string]any{}, expectCode: http.StatusNotFound,
		},
		{
			name: "a group action is POST only", method: http.MethodGet,
			path:       "/20200531/logGroups/" + groupID + "/actions/changeCompartment",
			expectCode: http.StatusMethodNotAllowed,
		},
		{
			name: "the collection takes no PUT", method: http.MethodPut,
			path: "/20200531/logGroups", body: map[string]any{}, expectCode: http.StatusMethodNotAllowed,
		},
		{
			name: "a group takes no PATCH", method: http.MethodPatch,
			path: "/20200531/logGroups/" + groupID, body: map[string]any{},
			expectCode: http.StatusMethodNotAllowed,
		},
		{
			name: "listing needs a compartment", method: http.MethodGet,
			path: "/20200531/logGroups", expectCode: http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, tc.method, tc.path, tc.body)
			assert.Equal(t, tc.expectCode, rec.Code, rec.Body.String())
		})
	}
}

func TestListLogGroupsPaginates(t *testing.T) {
	h, work := newHandler(t)

	for _, name := range []string{"a", "b", "c"} {
		createGroup(t, h, work, name)
	}

	first := do(t, h, http.MethodGet, "/20200531/logGroups?compartmentId="+compartmentA+"&limit=2", nil)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())

	var page []map[string]any

	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &page))
	assert.Len(t, page, 2)

	next := first.Header().Get(ocirest.HeaderNextPage)
	require.NotEmpty(t, next, "a truncated page must stamp opc-next-page")

	second := do(t, h, http.MethodGet,
		"/20200531/logGroups?compartmentId="+compartmentA+"&limit=2&page="+next, nil)
	require.Equal(t, http.StatusOK, second.Code)

	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &page))
	assert.Len(t, page, 1)
	assert.Empty(t, second.Header().Get(ocirest.HeaderNextPage), "the last page stamps no cursor")

	byName := do(t, h, http.MethodGet,
		"/20200531/logGroups?compartmentId="+compartmentA+"&displayName=b", nil)
	require.Equal(t, http.StatusOK, byName.Code)

	require.NoError(t, json.Unmarshal(byName.Body.Bytes(), &page))
	require.Len(t, page, 1)
	assert.Equal(t, "b", page[0]["displayName"])
}

func TestLogMutations(t *testing.T) {
	h, work := newHandler(t)
	groupID := createGroup(t, h, work, "app-logs")
	logID := createLog(t, h, work, groupID, "stdout")

	t.Run("update", func(t *testing.T) {
		rec := do(t, h, http.MethodPut, "/20200531/logGroups/"+groupID+"/logs/"+logID, map[string]any{
			"displayName":       "renamed",
			"isEnabled":         false,
			"retentionDuration": 90,
			"freeformTags":      map[string]string{"env": "dev"},
			"configuration": map[string]any{
				"source":    map[string]any{"parameters": map[string]string{"k": "v"}},
				"archiving": map[string]any{"isEnabled": true},
			},
		})
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

		got := do(t, h, http.MethodGet, "/20200531/logGroups/"+groupID+"/logs/"+logID, nil)
		require.Equal(t, http.StatusOK, got.Code)

		var body map[string]any
		require.NoError(t, json.Unmarshal(got.Body.Bytes(), &body))
		assert.Equal(t, "renamed", body["displayName"])
		assert.Equal(t, false, body["isEnabled"])
		assert.InDelta(t, 90, body["retentionDuration"], 0)

		cfg := body["configuration"].(map[string]any)
		assert.Equal(t, "OCISERVICE", cfg["source"].(map[string]any)["sourceType"])
		assert.Equal(t, true, cfg["archiving"].(map[string]any)["isEnabled"])
	})

	t.Run("delete", func(t *testing.T) {
		rec := do(t, h, http.MethodDelete, "/20200531/logGroups/"+groupID+"/logs/"+logID, nil)
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

		got := do(t, h, http.MethodGet, "/20200531/logGroups/"+groupID+"/logs/"+logID, nil)
		assert.Equal(t, http.StatusNotFound, got.Code)
	})
}

func TestLogMutationErrors(t *testing.T) {
	h, work := newHandler(t)
	groupID := createGroup(t, h, work, "app-logs")
	logID := createLog(t, h, work, groupID, "stdout")
	missing := "ocid1.log.oc1.iad.missing"

	tests := []struct {
		name       string
		method     string
		path       string
		body       any
		expectCode int
	}{
		{
			name: "update an unknown log", method: http.MethodPut,
			path: "/20200531/logGroups/" + groupID + "/logs/" + missing,
			body: map[string]any{"isEnabled": true}, expectCode: http.StatusNotFound,
		},
		{
			name: "delete an unknown log", method: http.MethodDelete,
			path: "/20200531/logGroups/" + groupID + "/logs/" + missing, expectCode: http.StatusNotFound,
		},
		{
			name: "a log takes no PATCH", method: http.MethodPatch,
			path: "/20200531/logGroups/" + groupID + "/logs/" + logID, body: map[string]any{},
			expectCode: http.StatusMethodNotAllowed,
		},
		{
			name: "the log collection takes no PUT", method: http.MethodPut,
			path: "/20200531/logGroups/" + groupID + "/logs", body: map[string]any{},
			expectCode: http.StatusMethodNotAllowed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, tc.method, tc.path, tc.body)
			assert.Equal(t, tc.expectCode, rec.Code, rec.Body.String())
		})
	}
}

func TestRoutingEdges(t *testing.T) {
	h, work := newHandler(t)
	groupID := createGroup(t, h, work, "app-logs")

	tests := []struct {
		name       string
		method     string
		path       string
		expectCode int
	}{
		{name: "malformed path", method: http.MethodGet, path: "/20200531", expectCode: http.StatusBadRequest},
		{
			name: "too many segments", method: http.MethodGet,
			path: "/20200531/logGroups/" + groupID + "/logs/l/extra/more", expectCode: http.StatusBadRequest,
		},
		{
			name: "unknown API version", method: http.MethodGet,
			path: "/19990101/logGroups", expectCode: http.StatusNotFound,
		},
		{
			name: "unknown control-plane collection", method: http.MethodGet,
			path: "/20200531/somethingElse", expectCode: http.StatusNotFound,
		},
		{
			name: "unemulated unified agent", method: http.MethodGet,
			path: "/20200531/unifiedAgentConfigurations", expectCode: http.StatusNotImplemented,
		},
		{
			name: "unemulated saved searches", method: http.MethodGet,
			path: "/20200531/logSavedSearches", expectCode: http.StatusNotImplemented,
		},
		{
			name: "unknown sub-collection", method: http.MethodGet,
			path: "/20200531/logGroups/" + groupID + "/exports", expectCode: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, tc.method, tc.path, nil)
			assert.Equal(t, tc.expectCode, rec.Code, rec.Body.String())
		})
	}
}

// TestMalformedBodies covers the decode failure of every operation that reads
// one, since a bad body must be a 400 rather than a panic.
func TestMalformedBodies(t *testing.T) {
	h, work := newHandler(t)
	groupID := createGroup(t, h, work, "app-logs")
	logID := createLog(t, h, work, groupID, "stdout")

	paths := map[string]struct {
		method string
		path   string
	}{
		"create group": {http.MethodPost, "/20200531/logGroups"},
		"update group": {http.MethodPut, "/20200531/logGroups/" + groupID},
		"move group": {
			http.MethodPost, "/20200531/logGroups/" + groupID + "/actions/changeCompartment",
		},
		"create log": {http.MethodPost, "/20200531/logGroups/" + groupID + "/logs"},
		"update log": {http.MethodPut, "/20200531/logGroups/" + groupID + "/logs/" + logID},
		"push":       {http.MethodPost, "/20200601/logs/" + logID + "/actions/push"},
		"search":     {http.MethodPost, "/20190909/search"},
	}

	for name, tc := range paths {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader("{not json")))
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}
}

func TestCreateLogErrors(t *testing.T) {
	h, work := newHandler(t)
	groupID := createGroup(t, h, work, "app-logs")

	tests := []struct {
		name       string
		path       string
		body       any
		expectCode int
	}{
		{
			name: "an unknown group", path: "/20200531/logGroups/ocid1.loggroup.oc1.iad.missing/logs",
			body: map[string]any{"displayName": "stdout", "logType": "CUSTOM"}, expectCode: http.StatusNotFound,
		},
		{
			name: "no display name", path: "/20200531/logGroups/" + groupID + "/logs",
			body: map[string]any{"logType": "CUSTOM"}, expectCode: http.StatusBadRequest,
		},
		{
			name: "a SERVICE log with no source", path: "/20200531/logGroups/" + groupID + "/logs",
			body: map[string]any{"displayName": "flow", "logType": "SERVICE"}, expectCode: http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, tc.path, tc.body)
			assert.Equal(t, tc.expectCode, rec.Code, rec.Body.String())
		})
	}
}
