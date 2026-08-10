package monitoring_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	ocimon "github.com/stackshy/cloudemu/v2/providers/oci/monitoring"
	"github.com/stackshy/cloudemu/v2/server/oci/monitoring"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	compartmentA = "ocid1.compartment.oc1..aaaaaaaacompartmenta"
	compartmentB = "ocid1.compartment.oc1..aaaaaaaacompartmentb"
	namespace    = "cloudemu_app"
	alarmsPath   = "/20180401/alarms"
	metricsPath  = "/20180401/metrics"
)

// stubMonitoring implements only the portable driver, so the handler must
// report the OCI capability as missing.
type stubMonitoring struct{ mondriver.Monitoring }

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	// Fixed clock: the metric timestamps the helpers post are literals.
	now := time.Date(2026, time.August, 1, 12, 30, 0, 0, time.UTC)
	mock := ocimon.New(config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(compartmentA),
		config.WithClock(config.NewFakeClock(now)),
	))

	ts := httptest.NewServer(monitoring.New(mock))
	t.Cleanup(ts.Close)

	return ts
}

func do(t *testing.T, ts *httptest.Server, method, path, body string) (*http.Response, []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, ts.URL+path, strings.NewReader(body))
	require.NoError(t, err)

	req.Header.Set(ocirest.HeaderRequestID, "test-request-id")

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp, raw
}

func decode[T any](t *testing.T, raw []byte) T {
	t.Helper()

	var v T

	require.NoError(t, json.Unmarshal(raw, &v))

	return v
}

func postMetric(t *testing.T, ts *httptest.Server, compartmentID, name string, value float64) {
	t.Helper()

	body := `{"metricData":[{"namespace":"` + namespace + `","compartmentId":"` + compartmentID +
		`","name":"` + name + `","dimensions":{"resourceId":"vm-1"},` +
		`"datapoints":[{"timestamp":"2026-08-01T12:00:00Z","value":` + jsonNumber(value) + `}]}]}`

	resp, _ := do(t, ts, http.MethodPost, metricsPath, body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func jsonNumber(v float64) string {
	raw, _ := json.Marshal(v)

	return string(raw)
}

func createAlarm(t *testing.T, ts *httptest.Server, compartmentID, name, query string) map[string]any {
	t.Helper()

	body := `{"displayName":"` + name + `","compartmentId":"` + compartmentID +
		`","namespace":"` + namespace + `","query":"` + query +
		`","severity":"CRITICAL","destinations":["ocid1.onstopic.oc1.iad.aaaa"],"isEnabled":true}`

	resp, raw := do(t, ts, http.MethodPost, alarmsPath, body)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))

	return decode[map[string]any](t, raw)
}

func TestMatches(t *testing.T) {
	h := monitoring.New(ocimon.New(config.NewOptions()))

	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"post metric data", http.MethodPost, "/20180401/metrics", true},
		{"list metrics action", http.MethodPost, "/20180401/metrics/actions/listMetrics", true},
		{"summarize action", http.MethodPost, "/20180401/metrics/actions/summarizeMetricsData", true},
		{"alarm collection", http.MethodGet, "/20180401/alarms", true},
		{"single alarm", http.MethodGet, "/20180401/alarms/ocid1.alarm.oc1.iad.aaaa", true},
		{"alarm history", http.MethodGet, "/20180401/alarms/ocid1.alarm.oc1.iad.aaaa/history", true},
		{"alarm status", http.MethodGet, "/20180401/alarms/status", true},

		{"work requests", http.MethodGet, "/20180401/workRequests", false},
		{"notifications topics", http.MethodGet, "/20181201/topics", false},
		{"another service's alarms", http.MethodGet, "/20160918/alarms", false},
		{"core instances", http.MethodGet, "/20160918/instances", false},
		{"object storage", http.MethodGet, "/n/tenancy/b/bucket/o/key", false},
		{"version only", http.MethodGet, "/20180401", false},
		{"root", http.MethodGet, "/", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			assert.Equal(t, tc.want, h.Matches(req))
		})
	}
}

func TestPostMetricData(t *testing.T) {
	ts := newServer(t)

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name: "accepted",
			body: `{"metricData":[{"namespace":"` + namespace + `","compartmentId":"` + compartmentA +
				`","name":"CpuUtilization","datapoints":[{"timestamp":"2026-08-01T12:00:00Z","value":42}]}]}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "empty batch",
			body:       `{"metricData":[]}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "InvalidParameter",
		},
		{
			name: "entry without compartment",
			body: `{"metricData":[{"namespace":"` + namespace +
				`","name":"CpuUtilization","datapoints":[{"timestamp":"2026-08-01T12:00:00Z","value":42}]}]}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "InvalidParameter",
		},
		{
			name:       "malformed json",
			body:       `{`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "InvalidParameter",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, raw := do(t, ts, http.MethodPost, metricsPath, tc.body)
			require.Equal(t, tc.wantStatus, resp.StatusCode, string(raw))
			assert.Equal(t, "test-request-id", resp.Header.Get(ocirest.HeaderRequestID))

			if tc.wantCode == "" {
				body := decode[map[string]any](t, raw)
				assert.InDelta(t, 0.0, body["failedMetricsCount"], 0.001)

				return
			}

			assert.Equal(t, tc.wantCode, decode[ocirest.ErrorBody](t, raw).Code)
		})
	}
}

func TestListMetrics(t *testing.T) {
	ts := newServer(t)
	postMetric(t, ts, compartmentA, "CpuUtilization", 42)
	postMetric(t, ts, compartmentB, "MemoryUtilization", 7)

	t.Run("scoped to the compartment", func(t *testing.T) {
		resp, raw := do(t, ts, http.MethodPost, metricsPath+"/actions/listMetrics?compartmentId="+compartmentA, `{}`)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		metrics := decode[[]map[string]any](t, raw)
		require.Len(t, metrics, 1)
		assert.Equal(t, "CpuUtilization", metrics[0]["name"])
		assert.Equal(t, compartmentA, metrics[0]["compartmentId"])
	})

	t.Run("namespace filter", func(t *testing.T) {
		resp, raw := do(t, ts, http.MethodPost,
			metricsPath+"/actions/listMetrics?compartmentId="+compartmentA, `{"namespace":"other"}`)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Empty(t, decode[[]map[string]any](t, raw))
	})

	t.Run("compartmentId is required", func(t *testing.T) {
		resp, raw := do(t, ts, http.MethodPost, metricsPath+"/actions/listMetrics", `{}`)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, "InvalidParameter", decode[ocirest.ErrorBody](t, raw).Code)
	})
}

func TestSummarizeMetricsData(t *testing.T) {
	ts := newServer(t)
	postMetric(t, ts, compartmentA, "CpuUtilization", 42)

	path := metricsPath + "/actions/summarizeMetricsData?compartmentId=" + compartmentA

	t.Run("aggregates the series", func(t *testing.T) {
		body := `{"namespace":"` + namespace + `","query":"CpuUtilization[1m].mean()",` +
			`"startTime":"2026-08-01T11:00:00Z","endTime":"2026-08-01T13:00:00Z"}`

		resp, raw := do(t, ts, http.MethodPost, path, body)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))

		series := decode[[]map[string]any](t, raw)
		require.Len(t, series, 1)
		assert.Equal(t, "CpuUtilization", series[0]["name"])

		points, ok := series[0]["aggregatedDatapoints"].([]any)
		require.True(t, ok)
		require.Len(t, points, 1)
	})

	t.Run("malformed query", func(t *testing.T) {
		resp, raw := do(t, ts, http.MethodPost, path, `{"namespace":"`+namespace+`","query":"nonsense"}`)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, "InvalidParameter", decode[ocirest.ErrorBody](t, raw).Code)
	})

	t.Run("query is required", func(t *testing.T) {
		resp, raw := do(t, ts, http.MethodPost, path, `{"namespace":"`+namespace+`"}`)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, "InvalidParameter", decode[ocirest.ErrorBody](t, raw).Code)
	})
}

func TestAlarmLifecycle(t *testing.T) {
	ts := newServer(t)

	created := createAlarm(t, ts, compartmentA, "high-cpu", "CpuUtilization[1m].mean() > 80")
	id, ok := created["id"].(string)
	require.True(t, ok)
	assert.Equal(t, "ACTIVE", created["lifecycleState"])
	assert.Equal(t, compartmentA, created["metricCompartmentId"])

	t.Run("get", func(t *testing.T) {
		resp, raw := do(t, ts, http.MethodGet, alarmsPath+"/"+id, "")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "high-cpu", decode[map[string]any](t, raw)["displayName"])
	})

	t.Run("get missing", func(t *testing.T) {
		resp, raw := do(t, ts, http.MethodGet, alarmsPath+"/ocid1.alarm.oc1.iad.missing", "")
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		assert.Equal(t, "NotAuthorizedOrNotFound", decode[ocirest.ErrorBody](t, raw).Code)
	})

	t.Run("update", func(t *testing.T) {
		resp, raw := do(t, ts, http.MethodPut, alarmsPath+"/"+id, `{"severity":"WARNING","isEnabled":false}`)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))

		updated := decode[map[string]any](t, raw)
		assert.Equal(t, "WARNING", updated["severity"])
		assert.Equal(t, false, updated["isEnabled"])
		assert.Equal(t, "high-cpu", updated["displayName"], "an omitted field is left alone")
	})

	t.Run("update missing", func(t *testing.T) {
		resp, _ := do(t, ts, http.MethodPut, alarmsPath+"/ocid1.alarm.oc1.iad.missing", `{"severity":"WARNING"}`)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("history", func(t *testing.T) {
		resp, raw := do(t, ts, http.MethodGet, alarmsPath+"/"+id+"/history", "")
		require.Equal(t, http.StatusOK, resp.StatusCode)

		history := decode[map[string]any](t, raw)
		assert.Equal(t, id, history["alarmId"])
		assert.NotNil(t, history["entries"])
	})

	t.Run("history of missing", func(t *testing.T) {
		resp, _ := do(t, ts, http.MethodGet, alarmsPath+"/ocid1.alarm.oc1.iad.missing/history", "")
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("delete", func(t *testing.T) {
		resp, _ := do(t, ts, http.MethodDelete, alarmsPath+"/"+id, "")
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})

	t.Run("delete missing", func(t *testing.T) {
		resp, _ := do(t, ts, http.MethodDelete, alarmsPath+"/"+id, "")
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestCreateAlarmErrors(t *testing.T) {
	ts := newServer(t)
	createAlarm(t, ts, compartmentA, "dupe", "CpuUtilization[1m].mean() > 80")

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name: "duplicate display name",
			body: `{"displayName":"dupe","compartmentId":"` + compartmentA +
				`","namespace":"` + namespace + `","query":"CpuUtilization[1m].mean() > 80"}`,
			wantStatus: http.StatusConflict,
			wantCode:   "Conflict",
		},
		{
			name:       "missing query",
			body:       `{"displayName":"no-query","compartmentId":"` + compartmentA + `"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "InvalidParameter",
		},
		{
			name:       "missing compartment",
			body:       `{"displayName":"no-compartment","query":"CpuUtilization[1m].mean() > 80"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "InvalidParameter",
		},
		{
			name: "unparseable query",
			body: `{"displayName":"inert","compartmentId":"` + compartmentA +
				`","namespace":"` + namespace + `","query":"CpuUtilization.mean() > 80"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "InvalidParameter",
		},
		{
			name: "unknown severity",
			body: `{"displayName":"urgent","compartmentId":"` + compartmentA + `","namespace":"` + namespace +
				`","query":"CpuUtilization[1m].mean() > 80","severity":"URGENT"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "InvalidParameter",
		},
		{
			name: "pendingDuration that is not ISO-8601",
			body: `{"displayName":"pending","compartmentId":"` + compartmentA + `","namespace":"` + namespace +
				`","query":"CpuUtilization[1m].mean() > 80","pendingDuration":"5m"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "InvalidParameter",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, raw := do(t, ts, http.MethodPost, alarmsPath, tc.body)
			require.Equal(t, tc.wantStatus, resp.StatusCode, string(raw))
			assert.Equal(t, tc.wantCode, decode[ocirest.ErrorBody](t, raw).Code)
		})
	}

	resp, raw := do(t, ts, http.MethodGet, alarmsPath+"?compartmentId="+compartmentA, "")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	listed := decode[[]map[string]any](t, raw)
	require.Len(t, listed, 1, "a refused alarm must not be stored")
	assert.Equal(t, "dupe", listed[0]["displayName"])
}

func TestListAlarmsIsCompartmentScoped(t *testing.T) {
	ts := newServer(t)
	createAlarm(t, ts, compartmentA, "in-a", "CpuUtilization[1m].mean() > 80")
	createAlarm(t, ts, compartmentB, "in-b", "CpuUtilization[1m].mean() > 80")

	t.Run("lists only its own compartment", func(t *testing.T) {
		resp, raw := do(t, ts, http.MethodGet, alarmsPath+"?compartmentId="+compartmentA, "")
		require.Equal(t, http.StatusOK, resp.StatusCode)

		alarms := decode[[]map[string]any](t, raw)
		require.Len(t, alarms, 1)
		assert.Equal(t, "in-a", alarms[0]["displayName"])
	})

	t.Run("displayName filter", func(t *testing.T) {
		resp, raw := do(t, ts, http.MethodGet, alarmsPath+"?compartmentId="+compartmentA+"&displayName=in-b", "")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Empty(t, decode[[]map[string]any](t, raw))
	})

	t.Run("compartmentId is required", func(t *testing.T) {
		resp, raw := do(t, ts, http.MethodGet, alarmsPath, "")
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, "InvalidParameter", decode[ocirest.ErrorBody](t, raw).Code)
	})
}

func TestListAlarmsPaginates(t *testing.T) {
	ts := newServer(t)
	for _, name := range []string{"alarm-1", "alarm-2", "alarm-3"} {
		createAlarm(t, ts, compartmentA, name, "CpuUtilization[1m].mean() > 80")
	}

	seen := make([]string, 0, 3)
	page := ""

	for range 3 {
		url := alarmsPath + "?compartmentId=" + compartmentA + "&limit=2&page=" + page

		resp, raw := do(t, ts, http.MethodGet, url, "")
		require.Equal(t, http.StatusOK, resp.StatusCode)

		for _, a := range decode[[]map[string]any](t, raw) {
			name, _ := a["displayName"].(string)
			seen = append(seen, name)
		}

		page = resp.Header.Get(ocirest.HeaderNextPage)
		if page == "" {
			break
		}
	}

	assert.Empty(t, page, "the last page must carry no cursor")
	assert.ElementsMatch(t, []string{"alarm-1", "alarm-2", "alarm-3"}, seen)
}

// TestSummarizeRejectsSubSecondResolution covers the wire path that fed a
// resolution straight into a bucket loop that could not advance. The server is
// left unclosed on purpose: a regression leaves its handler spinning, and Close
// would block on it instead of letting the test fail.
func TestSummarizeRejectsSubSecondResolution(t *testing.T) {
	ts := httptest.NewServer(monitoring.New(ocimon.New(config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(compartmentA),
	))))

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	body := `{"namespace":"` + namespace + `","query":"CpuUtilization[500ms].mean()"}`
	url := ts.URL + metricsPath + "/actions/summarizeMetricsData?compartmentId=" + compartmentA

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)

	resp, err := ts.Client().Do(req)
	require.NoError(t, err, "sub-second resolution hung the serving goroutine")

	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestListAlarmsStatus(t *testing.T) {
	ts := newServer(t)
	createAlarm(t, ts, compartmentA, "high-cpu", "CpuUtilization[1m].mean() > 80")

	t.Run("reports each alarm's status", func(t *testing.T) {
		resp, raw := do(t, ts, http.MethodGet, alarmsPath+"/status?compartmentId="+compartmentA, "")
		require.Equal(t, http.StatusOK, resp.StatusCode)

		statuses := decode[[]map[string]any](t, raw)
		require.Len(t, statuses, 1)
		assert.Equal(t, "high-cpu", statuses[0]["displayName"])
		assert.Equal(t, "OK", statuses[0]["status"])
	})

	t.Run("compartmentId is required", func(t *testing.T) {
		resp, raw := do(t, ts, http.MethodGet, alarmsPath+"/status", "")
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, "InvalidParameter", decode[ocirest.ErrorBody](t, raw).Code)
	})
}

func TestAlarmFiresFromPostedMetrics(t *testing.T) {
	ts := newServer(t)
	createAlarm(t, ts, compartmentA, "high-cpu", "CpuUtilization[3h].mean() > 80")
	postMetric(t, ts, compartmentA, "CpuUtilization", 95)

	resp, raw := do(t, ts, http.MethodGet, alarmsPath+"/status?compartmentId="+compartmentA, "")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	statuses := decode[[]map[string]any](t, raw)
	require.Len(t, statuses, 1)
	assert.Equal(t, "FIRING", statuses[0]["status"])
	assert.NotEmpty(t, statuses[0]["timestampTriggered"])
}

func TestRoutingRejections(t *testing.T) {
	ts := newServer(t)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"unknown metrics action", http.MethodPost, metricsPath + "/actions/nope", http.StatusNotFound},
		{"metrics is post only", http.MethodGet, metricsPath, http.StatusMethodNotAllowed},
		{"alarm collection patch", http.MethodPatch, alarmsPath, http.StatusMethodNotAllowed},
		{"alarm status is get only", http.MethodDelete, alarmsPath + "/status", http.StatusMethodNotAllowed},
		{"unknown alarm sub-resource", http.MethodGet, alarmsPath + "/id/nope", http.StatusNotFound},
		{"too many segments", http.MethodGet, alarmsPath + "/id/history/extra", http.StatusNotFound},
		{"unknown alarm action", http.MethodPost, alarmsPath + "/id/actions/nope", http.StatusNotFound},
		{"deepest path", http.MethodPost, alarmsPath + "/id/actions/retrieveDimensionStates/extra", http.StatusNotFound},
		{"dimension states is post only", http.MethodGet,
			alarmsPath + "/id/actions/retrieveDimensionStates", http.StatusMethodNotAllowed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := do(t, ts, tc.method, tc.path, "")
			assert.Equal(t, tc.wantStatus, resp.StatusCode)
		})
	}
}

func TestDriverWithoutOCICapability(t *testing.T) {
	ts := httptest.NewServer(monitoring.New(stubMonitoring{}))
	defer ts.Close()

	resp, raw := do(t, ts, http.MethodGet, alarmsPath+"?compartmentId="+compartmentA, "")
	require.Equal(t, http.StatusNotImplemented, resp.StatusCode)
	assert.Equal(t, "NotImplemented", decode[ocirest.ErrorBody](t, raw).Code)
}

// TestCreateAlarmRejectsUnsupportedFields covers the alarm fields the wire
// shape carries that this emulator does not act on: they are refused rather
// than accepted and dropped.
func TestCreateAlarmRejectsUnsupportedFields(t *testing.T) {
	ts := newServer(t)
	created := createAlarm(t, ts, compartmentA, "patchable", "CpuUtilization[1m].mean() > 80")

	id, ok := created["id"].(string)
	require.True(t, ok)

	base := `"displayName":"suppressed","compartmentId":"` + compartmentA +
		`","namespace":"` + namespace + `","query":"CpuUtilization[1m].mean() > 80"`

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create with suppression", http.MethodPost, alarmsPath,
			`{` + base + `,"suppression":{"timeSuppressFrom":"2026-08-01T00:00:00Z"}}`},
		{"create with overrides", http.MethodPost, alarmsPath,
			`{` + base + `,"overrides":[{"severity":"INFO"}]}`},
		{"update with suppression", http.MethodPut, alarmsPath + "/" + id,
			`{"suppression":{"timeSuppressFrom":"2026-08-01T00:00:00Z"}}`},
		{"update with overrides", http.MethodPut, alarmsPath + "/" + id, `{"overrides":[{"severity":"INFO"}]}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, raw := do(t, ts, tc.method, tc.path, tc.body)
			require.Equal(t, http.StatusBadRequest, resp.StatusCode, string(raw))

			body := decode[ocirest.ErrorBody](t, raw)
			assert.Equal(t, "InvalidParameter", body.Code)
			assert.Contains(t, body.Message, "not supported")
		})
	}
}

// TestPostMetricDataLimits covers the batch and shape limits real OCI enforces
// on ingestion.
func TestPostMetricDataLimits(t *testing.T) {
	ts := newServer(t)

	entries := make([]string, 0, 51)
	for i := range 51 {
		entries = append(entries, `{"namespace":"`+namespace+`","compartmentId":"`+compartmentA+
			`","name":"Cpu`+strconv.Itoa(i)+`","datapoints":[{"timestamp":"2026-08-01T12:00:00Z","value":1}]}`)
	}

	tests := []struct {
		name string
		body string
	}{
		{"batch over the limit", `{"metricData":[` + strings.Join(entries, ",") + `]}`},
		{"reserved namespace", `{"metricData":[{"namespace":"oci_computeagent","compartmentId":"` + compartmentA +
			`","name":"Cpu","datapoints":[{"timestamp":"2026-08-01T12:00:00Z","value":1}]}]}`},
		{"dimension key with a period", `{"metricData":[{"namespace":"` + namespace + `","compartmentId":"` +
			compartmentA + `","name":"Cpu","dimensions":{"resource.id":"vm-1"},` +
			`"datapoints":[{"timestamp":"2026-08-01T12:00:00Z","value":1}]}]}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, raw := do(t, ts, http.MethodPost, metricsPath, tc.body)
			require.Equal(t, http.StatusBadRequest, resp.StatusCode, string(raw))
			assert.Equal(t, "InvalidParameter", decode[ocirest.ErrorBody](t, raw).Code)
		})
	}

	t.Run("the limit itself is accepted", func(t *testing.T) {
		body := `{"metricData":[` + strings.Join(entries[:50], ",") + `]}`

		resp, raw := do(t, ts, http.MethodPost, metricsPath, body)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	})
}

// TestRetrieveDimensionStatesIsDisclosed covers the alarm action this emulator
// does not implement: it names itself rather than reading as a path OCI has and
// this handler lacks.
func TestRetrieveDimensionStatesIsDisclosed(t *testing.T) {
	ts := newServer(t)
	created := createAlarm(t, ts, compartmentA, "high-cpu", "CpuUtilization[1m].mean() > 80")

	id, ok := created["id"].(string)
	require.True(t, ok)

	resp, raw := do(t, ts, http.MethodPost, alarmsPath+"/"+id+"/actions/retrieveDimensionStates", "")
	require.Equal(t, http.StatusNotImplemented, resp.StatusCode, string(raw))

	body := decode[ocirest.ErrorBody](t, raw)
	assert.Equal(t, "NotImplemented", body.Code)
	assert.Contains(t, body.Message, "retrieveDimensionStates")
}
