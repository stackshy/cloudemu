package cloudwatch_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cwprovider "github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	cwserver "github.com/stackshy/cloudemu/v2/server/aws/cloudwatch"
)

// periodRequestTimeout bounds each request, so a Period that sends the server
// into an endless loop fails the test instead of hanging it.
const periodRequestTimeout = 10 * time.Second

// newPeriodPoster is newQueryPoster with a client timeout. It stores one
// datum of App/Latency so GetMetricData has points to bucket.
func newPeriodPoster(t *testing.T) func(url.Values) (int, string) {
	t.Helper()

	ts := httptest.NewServer(cwserver.New(cwprovider.New(config.NewOptions())))
	t.Cleanup(ts.Close)

	client := &http.Client{Timeout: periodRequestTimeout}

	post := func(form url.Values) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Authorization", monitoringAuth)

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("post %s: %v", form.Get("Action"), err)
		}
		defer resp.Body.Close()

		b, _ := io.ReadAll(resp.Body)

		return resp.StatusCode, string(b)
	}

	if code, body := post(url.Values{
		"Action": {"PutMetricData"}, "Namespace": {"App"},
		"MetricData.member.1.MetricName": {"Latency"}, "MetricData.member.1.Value": {"5"},
		"MetricData.member.1.Timestamp": {time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)},
	}); code != http.StatusOK {
		t.Fatalf("PutMetricData: code=%d body=%s", code, body)
	}

	return post
}

func periodMetricDataForm(statPeriod, exprPeriod string) url.Values {
	form := url.Values{
		"Action":                        {"GetMetricData"},
		"StartTime":                     {time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)},
		"EndTime":                       {time.Now().UTC().Format(time.RFC3339)},
		"MetricDataQueries.member.1.Id": {"m1"},
		"MetricDataQueries.member.1.MetricStat.Metric.Namespace":  {"App"},
		"MetricDataQueries.member.1.MetricStat.Metric.MetricName": {"Latency"},
		"MetricDataQueries.member.1.MetricStat.Period":            {statPeriod},
		"MetricDataQueries.member.1.MetricStat.Stat":              {"Sum"},
	}

	if exprPeriod != "" {
		form.Set("MetricDataQueries.member.2.Id", "e1")
		form.Set("MetricDataQueries.member.2.Expression", "m1 * 2")
		form.Set("MetricDataQueries.member.2.Period", exprPeriod)
	}

	return form
}

// TestGetMetricDataPeriodValidation checks the Period rule of MetricStat and
// MetricDataQuery: at least 1, and 1, 5, 10, 20, 30, 60 or a multiple of 60.
// A huge Period used to overflow the bucket step and crash or hang the server.
func TestGetMetricDataPeriodValidation(t *testing.T) {
	post := newPeriodPoster(t)

	bad := []struct {
		name, statPeriod, exprPeriod, param string
	}{
		{"huge multiple of 60 overflowing to a zero step", "540431955284459520", "", "MetricDataQueries.member.1.MetricStat.Period"},
		{"huge near int64 max", "9223372036854775800", "", "MetricDataQueries.member.1.MetricStat.Period"},
		{"above int32", "2147483700", "", "MetricDataQueries.member.1.MetricStat.Period"},
		{"zero", "0", "", "MetricDataQueries.member.1.MetricStat.Period"},
		{"negative", "-60", "", "MetricDataQueries.member.1.MetricStat.Period"},
		{"not a multiple of 60", "61", "", "MetricDataQueries.member.1.MetricStat.Period"},
		{"high resolution value not allowed", "15", "", "MetricDataQueries.member.1.MetricStat.Period"},
		{"negative expression period", "60", "-60", "MetricDataQueries.member.2.Period"},
		{"huge expression period", "60", "540431955284459520", "MetricDataQueries.member.2.Period"},
	}

	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			code, body := post(periodMetricDataForm(tc.statPeriod, tc.exprPeriod))
			if code != http.StatusBadRequest || !strings.Contains(body, "<Code>InvalidParameterValue</Code>") {
				t.Fatalf("want 400 InvalidParameterValue, got code=%d body=%s", code, body)
			}

			if !strings.Contains(body, "parameter "+tc.param+" is not a valid period") {
				t.Fatalf("message should name %s: %s", tc.param, body)
			}
		})
	}

	for _, p := range []string{"1", "5", "10", "20", "30", "60", "300", "86400"} {
		t.Run("valid "+p, func(t *testing.T) {
			code, body := post(periodMetricDataForm(p, p))
			if code != http.StatusOK {
				t.Fatalf("period %s: code=%d body=%s", p, code, body)
			}

			if !strings.Contains(body, "<Id>m1</Id>") || !strings.Contains(body, "<member>5</member>") {
				t.Fatalf("period %s: want the stored datum back: %s", p, body)
			}
		})
	}
}

// TestGetMetricStatisticsPeriodValidation applies the same Period rule to
// GetMetricStatistics, where Period is required.
func TestGetMetricStatisticsPeriodValidation(t *testing.T) {
	post := newPeriodPoster(t)

	form := func(period string) url.Values {
		return url.Values{
			"Action": {"GetMetricStatistics"}, "Namespace": {"App"}, "MetricName": {"Latency"},
			"StartTime":           {time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)},
			"EndTime":             {time.Now().UTC().Format(time.RFC3339)},
			"Period":              {period},
			"Statistics.member.1": {"Sum"},
		}
	}

	for _, p := range []string{"0", "-60", "61", "540431955284459520"} {
		t.Run("invalid "+p, func(t *testing.T) {
			code, body := post(form(p))
			if code != http.StatusBadRequest || !strings.Contains(body, "<Code>InvalidParameterValue</Code>") ||
				!strings.Contains(body, "parameter Period is not a valid period") {
				t.Fatalf("want 400 InvalidParameterValue, got code=%d body=%s", code, body)
			}
		})
	}

	code, body := post(form("300"))
	if code != http.StatusOK || !strings.Contains(body, "<Sum>5</Sum>") {
		t.Fatalf("valid period: code=%d body=%s", code, body)
	}
}

// TestPutMetricAlarmMathPeriodLookback checks that a metric math alarm's
// MetricStat Period obeys the EvaluationPeriods * Period cap, so a huge
// Period cannot reach the evaluator.
func TestPutMetricAlarmMathPeriodLookback(t *testing.T) {
	post := newPeriodPoster(t)

	form := func(period, evalPeriods string) url.Values {
		return url.Values{
			"Action": {"PutMetricAlarm"}, "AlarmName": {"math"}, "ComparisonOperator": {"GreaterThanThreshold"},
			"EvaluationPeriods": {evalPeriods}, "Threshold": {"1"},
			"Metrics.member.1.Id":                           {"m1"},
			"Metrics.member.1.ReturnData":                   {"false"},
			"Metrics.member.1.MetricStat.Metric.Namespace":  {"App"},
			"Metrics.member.1.MetricStat.Metric.MetricName": {"Latency"},
			"Metrics.member.1.MetricStat.Period":            {period},
			"Metrics.member.1.MetricStat.Stat":              {"Sum"},
			"Metrics.member.2.Id":                           {"e1"},
			"Metrics.member.2.Expression":                   {"m1 * 2"},
			"Metrics.member.2.ReturnData":                   {"true"},
		}
	}

	for _, tc := range []struct{ period, evalPeriods string }{
		{"540431955284459520", "1"},
		{"60", "1537228672809129302"},
		{"3600", "169"},
	} {
		code, body := post(form(tc.period, tc.evalPeriods))
		if code != http.StatusBadRequest || !strings.Contains(body, "<Code>ValidationError</Code>") {
			t.Fatalf("period %s x %s: want 400 ValidationError, got code=%d body=%s", tc.period, tc.evalPeriods, code, body)
		}
	}

	if code, body := post(form("3600", "168")); code != http.StatusOK {
		t.Fatalf("a week of hourly periods: code=%d body=%s", code, body)
	}
}
