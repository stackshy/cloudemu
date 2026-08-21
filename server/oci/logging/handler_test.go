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

const compartmentA = "ocid1.compartment.oc1..aaaaaaaacompa"

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
