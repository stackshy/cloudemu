package cloudwatch_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/fxamacker/cbor/v2"

	"github.com/stackshy/cloudemu/v2/config"
	cwprovider "github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	cwserver "github.com/stackshy/cloudemu/v2/server/aws/cloudwatch"
)

// extWire drives the CW-11 cases over one protocol. Each call returns the
// error code, "" on success.
type extWire struct {
	// putLatency puts the values 1..n of X/App Lat at ts.
	putLatency func(n int, ts time.Time) string
	// stats runs GetMetricStatistics and returns the ExtendedStatistics of
	// each datapoint.
	stats func(statistics, extended []string, start, end time.Time) ([]map[string]float64, string)
	// putAlarm puts an alarm on Lat. Empty fields are left out.
	putAlarm func(a extAlarm) string
	// describe returns the state, ExtendedStatistic and
	// EvaluateLowSampleCountPercentile of the alarm.
	describe func(name string) (state, ext, lowSample string)
	// listMetrics returns the metric names ListMetrics returns.
	listMetrics func(recentlyActive string) ([]string, string)
	// putNamed puts one value of X/App name.
	putNamed func(name string)
}

type extAlarm struct {
	name, stat, ext, lowSample string
	threshold                  float64
}

func extWires() map[string]func(*testing.T, *config.FakeClock) extWire {
	return map[string]func(*testing.T, *config.FakeClock) extWire{
		"cbor":  cborExtWire,
		"query": queryExtWire,
	}
}

func cborExtWire(t *testing.T, fc *config.FakeClock) extWire {
	t.Helper()

	client, ctx := newCWClientClock(t, fc)

	put := func(data []cwtypes.MetricDatum) string {
		_, err := client.PutMetricData(ctx, &awscw.PutMetricDataInput{Namespace: aws.String("X/App"), MetricData: data})

		return sdkErrCode(t, err)
	}

	return extWire{
		putLatency: func(n int, ts time.Time) string {
			values := make([]float64, n)
			for i := range values {
				values[i] = float64(i + 1)
			}

			return put([]cwtypes.MetricDatum{{MetricName: aws.String("Lat"), Values: values, Timestamp: aws.Time(ts)}})
		},
		stats: func(statistics, extended []string, start, end time.Time) ([]map[string]float64, string) {
			in := &awscw.GetMetricStatisticsInput{
				Namespace: aws.String("X/App"), MetricName: aws.String("Lat"), Period: aws.Int32(60),
				StartTime: aws.Time(start), EndTime: aws.Time(end), ExtendedStatistics: extended,
			}
			for _, s := range statistics {
				in.Statistics = append(in.Statistics, cwtypes.Statistic(s))
			}

			out, err := client.GetMetricStatistics(ctx, in)
			if err != nil {
				return nil, sdkErrCode(t, err)
			}

			var maps []map[string]float64
			for _, dp := range out.Datapoints {
				maps = append(maps, dp.ExtendedStatistics)
			}

			return maps, ""
		},
		putAlarm: func(a extAlarm) string {
			in := &awscw.PutMetricAlarmInput{
				AlarmName: aws.String(a.name), Namespace: aws.String("X/App"), MetricName: aws.String("Lat"),
				ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold, Threshold: aws.Float64(a.threshold),
				EvaluationPeriods: aws.Int32(1), Period: aws.Int32(60), Statistic: cwtypes.Statistic(a.stat),
			}
			if a.ext != "" {
				in.ExtendedStatistic = aws.String(a.ext)
			}

			if a.lowSample != "" {
				in.EvaluateLowSampleCountPercentile = aws.String(a.lowSample)
			}

			_, err := client.PutMetricAlarm(ctx, in)

			return sdkErrCode(t, err)
		},
		describe: func(name string) (string, string, string) {
			out, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmNames: []string{name}})
			if err != nil || len(out.MetricAlarms) != 1 {
				t.Fatalf("DescribeAlarms: %v", err)
			}

			a := out.MetricAlarms[0]

			return string(a.StateValue), aws.ToString(a.ExtendedStatistic), aws.ToString(a.EvaluateLowSampleCountPercentile)
		},
		listMetrics: func(recentlyActive string) ([]string, string) {
			out, err := client.ListMetrics(ctx, &awscw.ListMetricsInput{
				Namespace: aws.String("X/App"), RecentlyActive: cwtypes.RecentlyActive(recentlyActive),
			})
			if err != nil {
				return nil, sdkErrCode(t, err)
			}

			var names []string
			for _, m := range out.Metrics {
				names = append(names, aws.ToString(m.MetricName))
			}

			return names, ""
		},
		putNamed: func(name string) {
			if code := put([]cwtypes.MetricDatum{{MetricName: aws.String(name), Value: aws.Float64(1)}}); code != "" {
				t.Fatalf("PutMetricData: %s", code)
			}
		},
	}
}

// newClockQueryPoster posts query-protocol forms to a handler whose provider
// uses fc.
func newClockQueryPoster(t *testing.T, fc *config.FakeClock) func(url.Values) (int, string) {
	t.Helper()

	ts := httptest.NewServer(cwserver.New(cwprovider.New(config.NewOptions(config.WithClock(fc)))))
	t.Cleanup(ts.Close)

	return func(form url.Values) (int, string) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Authorization", monitoringAuth)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()

		b, _ := io.ReadAll(resp.Body)

		return resp.StatusCode, string(b)
	}
}

var (
	xmlCodeRe  = regexp.MustCompile(`<Code>([^<]+)</Code>`)
	xmlEntryRe = regexp.MustCompile(`<entry><key>([^<]+)</key><value>([^<]+)</value></entry>`)
	xmlNameRe  = regexp.MustCompile(`<MetricName>([^<]+)</MetricName>`)
)

// queryCode is the error code of a query response, "" for a 200.
func queryCode(code int, body string) string {
	if code == http.StatusOK {
		return ""
	}

	m := xmlCodeRe.FindStringSubmatch(body)
	if m == nil {
		return "HTTP " + strconv.Itoa(code) + ": " + body
	}

	return m[1]
}

func xmlField(body, name string) string {
	m := regexp.MustCompile(`<` + name + `>([^<]*)</` + name + `>`).FindStringSubmatch(body)
	if m == nil {
		return ""
	}

	return m[1]
}

func queryExtWire(t *testing.T, fc *config.FakeClock) extWire {
	t.Helper()

	post := newClockQueryPoster(t, fc)

	return extWire{
		putLatency: func(n int, ts time.Time) string {
			form := url.Values{
				"Action": {"PutMetricData"}, "Namespace": {"X/App"},
				"MetricData.member.1.MetricName": {"Lat"},
				"MetricData.member.1.Timestamp":  {ts.UTC().Format(time.RFC3339)},
			}
			for i := 1; i <= n; i++ {
				form.Set("MetricData.member.1.Values.member."+strconv.Itoa(i), strconv.Itoa(i))
			}

			return queryCode(post(form))
		},
		stats: func(statistics, extended []string, start, end time.Time) ([]map[string]float64, string) {
			form := url.Values{
				"Action": {"GetMetricStatistics"}, "Namespace": {"X/App"}, "MetricName": {"Lat"}, "Period": {"60"},
				"StartTime": {start.UTC().Format(time.RFC3339)}, "EndTime": {end.UTC().Format(time.RFC3339)},
			}
			for i, s := range statistics {
				form.Set("Statistics.member."+strconv.Itoa(i+1), s)
			}

			for i, s := range extended {
				form.Set("ExtendedStatistics.member."+strconv.Itoa(i+1), s)
			}

			code, body := post(form)
			if c := queryCode(code, body); c != "" {
				return nil, c
			}

			var maps []map[string]float64

			for _, dp := range strings.Split(body, "<member>")[1:] {
				m := map[string]float64{}
				for _, e := range xmlEntryRe.FindAllStringSubmatch(dp, -1) {
					m[e[1]], _ = strconv.ParseFloat(e[2], 64)
				}

				maps = append(maps, m)
			}

			return maps, ""
		},
		putAlarm: func(a extAlarm) string {
			form := url.Values{
				"Action": {"PutMetricAlarm"}, "AlarmName": {a.name}, "Namespace": {"X/App"}, "MetricName": {"Lat"},
				"ComparisonOperator": {"GreaterThanThreshold"}, "EvaluationPeriods": {"1"}, "Period": {"60"},
				"Threshold": {strconv.FormatFloat(a.threshold, 'f', -1, 64)},
			}
			for k, v := range map[string]string{
				"Statistic": a.stat, "ExtendedStatistic": a.ext, "EvaluateLowSampleCountPercentile": a.lowSample,
			} {
				if v != "" {
					form.Set(k, v)
				}
			}

			return queryCode(post(form))
		},
		describe: func(name string) (string, string, string) {
			code, body := post(url.Values{"Action": {"DescribeAlarms"}, "AlarmNames.member.1": {name}})
			if code != http.StatusOK {
				t.Fatalf("DescribeAlarms: %d %s", code, body)
			}

			return xmlField(body, "StateValue"), xmlField(body, "ExtendedStatistic"),
				xmlField(body, "EvaluateLowSampleCountPercentile")
		},
		listMetrics: func(recentlyActive string) ([]string, string) {
			form := url.Values{"Action": {"ListMetrics"}, "Namespace": {"X/App"}}
			if recentlyActive != "" {
				form.Set("RecentlyActive", recentlyActive)
			}

			code, body := post(form)
			if c := queryCode(code, body); c != "" {
				return nil, c
			}

			var names []string
			for _, m := range xmlNameRe.FindAllStringSubmatch(body, -1) {
				names = append(names, m[1])
			}

			return names, ""
		},
		putNamed: func(name string) {
			if c := queryCode(post(url.Values{
				"Action": {"PutMetricData"}, "Namespace": {"X/App"},
				"MetricData.member.1.MetricName": {name}, "MetricData.member.1.Value": {"1"},
			})); c != "" {
				t.Fatalf("PutMetricData: %s", c)
			}
		},
	}
}

var cw11Start = time.Date(2025, 6, 1, 12, 0, 30, 0, time.UTC)

// GetMetricStatistics returns true percentiles in Datapoint.ExtendedStatistics.
// Before CW-11 the field was not read, so the request fell back to Average.
func TestWireGetMetricStatisticsPercentiles(t *testing.T) {
	for name, mk := range extWires() {
		t.Run(name, func(t *testing.T) {
			fc := config.NewFakeClock(cw11Start)
			w := mk(t, fc)

			if code := w.putLatency(100, cw11Start); code != "" {
				t.Fatalf("put: %s", code)
			}

			start, end := cw11Start.Add(-time.Hour), cw11Start.Add(time.Hour)

			got, code := w.stats(nil, []string{"p90", "p99.9", "p50"}, start, end)
			if code != "" {
				t.Fatalf("stats: %s", code)
			}

			if len(got) != 1 {
				t.Fatalf("want 1 datapoint, got %d", len(got))
			}

			want := map[string]float64{"p90": 90, "p99.9": 100, "p50": 50}
			for k, v := range want {
				if got[0][k] != v {
					t.Fatalf("%s = %v, want %v (all %v)", k, got[0][k], v, got[0])
				}
			}
		})
	}
}

// Statistics and ExtendedStatistics are exclusive, one is required, and
// ExtendedStatistics takes percentiles only.
func TestWireGetMetricStatisticsStatisticRules(t *testing.T) {
	cases := []struct {
		name       string
		statistics []string
		extended   []string
		want       string
	}{
		{"both", []string{"Average"}, []string{"p90"}, "InvalidParameterCombination"},
		{"neither", nil, nil, "MissingParameter"},
		{"above p100", nil, []string{"p101"}, "InvalidParameterValue"},
		{"trimmed mean", nil, []string{"tm90"}, "InvalidParameterValue"},
		{"eleven", nil, []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9", "p10", "p11"}, "InvalidParameterValue"},
	}

	for name, mk := range extWires() {
		t.Run(name, func(t *testing.T) {
			w := mk(t, config.NewFakeClock(cw11Start))

			for _, tc := range cases {
				if _, code := w.stats(tc.statistics, tc.extended, cw11Start.Add(-time.Hour), cw11Start); code != tc.want {
					t.Errorf("%s: code = %q, want %q", tc.name, code, tc.want)
				}
			}
		})
	}
}

// A p99 alarm over 1..100 breaches a threshold of 95. Before CW-11 it
// evaluated the average, 50.5, and stayed OK. With
// EvaluateLowSampleCountPercentile=ignore and 50 samples it keeps its state.
func TestWirePercentileAlarm(t *testing.T) {
	for name, mk := range extWires() {
		t.Run(name, func(t *testing.T) {
			fc := config.NewFakeClock(cw11Start)
			w := mk(t, fc)

			if code := w.putLatency(100, cw11Start); code != "" {
				t.Fatalf("put: %s", code)
			}

			if code := w.putAlarm(extAlarm{name: "p99", ext: "p99", threshold: 95}); code != "" {
				t.Fatalf("PutMetricAlarm: %s", code)
			}

			if state, ext, _ := w.describe("p99"); state != "ALARM" || ext != "p99" {
				t.Fatalf("state=%s ext=%s, want ALARM p99", state, ext)
			}

			fc.Advance(2 * time.Minute)

			if code := w.putLatency(50, fc.Now()); code != "" {
				t.Fatalf("put: %s", code)
			}

			if code := w.putAlarm(extAlarm{name: "low", ext: "p99", lowSample: "ignore", threshold: 10}); code != "" {
				t.Fatalf("PutMetricAlarm: %s", code)
			}

			state, _, low := w.describe("low")
			if state != "INSUFFICIENT_DATA" || low != "ignore" {
				t.Fatalf("state=%s lowSample=%s, want INSUFFICIENT_DATA ignore", state, low)
			}
		})
	}
}

func TestWirePercentileAlarmValidation(t *testing.T) {
	cases := []struct {
		alarm extAlarm
		want  string
	}{
		{extAlarm{name: "both", stat: "Average", ext: "p99"}, "InvalidParameterCombination"},
		{extAlarm{name: "neither"}, "ValidationError"},
		{extAlarm{name: "bad", ext: "p101"}, "ValidationError"},
		{extAlarm{name: "badrange", ext: "TM(90%:10%)"}, "ValidationError"},
		{extAlarm{name: "lowsample", ext: "p99", lowSample: "sometimes"}, "ValidationError"},
		{extAlarm{name: "ok-tm", ext: "TM(10%:90%)"}, ""},
		{extAlarm{name: "ok-iqm", ext: "IQM", lowSample: "evaluate"}, ""},
	}

	for name, mk := range extWires() {
		t.Run(name, func(t *testing.T) {
			w := mk(t, config.NewFakeClock(cw11Start))

			for _, tc := range cases {
				if code := w.putAlarm(tc.alarm); code != tc.want {
					t.Errorf("%s: code = %q, want %q", tc.alarm.name, code, tc.want)
				}
			}
		})
	}
}

// RecentlyActive=PT3H lists only series with data in the past three hours,
// and no series older than two weeks is listed at all. Before CW-11 both
// filters were ignored.
func TestWireListMetricsRecency(t *testing.T) {
	for name, mk := range extWires() {
		t.Run(name, func(t *testing.T) {
			fc := config.NewFakeClock(cw11Start)
			w := mk(t, fc)

			w.putNamed("Old")
			fc.Advance(4 * time.Hour)
			w.putNamed("New")

			if got, code := w.listMetrics("PT3H"); code != "" || strings.Join(got, ",") != "New" {
				t.Fatalf("PT3H: %v %s, want [New]", got, code)
			}

			if got, _ := w.listMetrics(""); strings.Join(got, ",") != "New,Old" {
				t.Fatalf("no filter: %v, want [New Old]", got)
			}

			fc.Advance(14*24*time.Hour - 3*time.Hour)

			if got, _ := w.listMetrics(""); strings.Join(got, ",") != "New" {
				t.Fatalf("after two weeks: %v, want [New]", got)
			}

			if _, code := w.listMetrics("PT1H"); code != "InvalidParameterValue" {
				t.Fatalf("PT1H: code = %q, want InvalidParameterValue", code)
			}
		})
	}
}

// windowWire puts and reads back an alarm's EvaluationWindow over one
// protocol. The SDK does not model EvaluationWindow yet, so the CBOR side
// sends raw rpc-v2-cbor.
type windowWire struct {
	put      func(name string, period int, window map[string]any) string
	describe func(name string) map[string]any
}

func cborWindowWire(t *testing.T) windowWire {
	t.Helper()

	ts := httptest.NewServer(cwserver.New(cwprovider.New(config.NewOptions())))
	t.Cleanup(ts.Close)

	call := func(op string, in map[string]any) (int, map[string]any) {
		body, err := cbor.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}

		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
			ts.URL+"/service/GraniteServiceVersion20100801/operation/"+op, bytes.NewReader(body))
		req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
		req.Header.Set("Content-Type", "application/cbor")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		raw, _ := io.ReadAll(resp.Body)

		var out map[string]any
		if err := cbor.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode %s: %v", op, err)
		}

		return resp.StatusCode, out
	}

	return windowWire{
		put: func(name string, period int, window map[string]any) string {
			in := map[string]any{
				"AlarmName": name, "Namespace": "W/App", "MetricName": "M", "Statistic": "Sum",
				"ComparisonOperator": "GreaterThanThreshold", "Threshold": 1.0, "EvaluationPeriods": 1, "Period": period,
			}
			if window != nil {
				in["EvaluationWindow"] = window
			}

			code, out := call("PutMetricAlarm", in)
			if code == http.StatusOK {
				return ""
			}

			typ, _ := out["__type"].(string)

			return typ[strings.LastIndex(typ, "#")+1:]
		},
		describe: func(name string) map[string]any {
			_, out := call("DescribeAlarms", map[string]any{"AlarmNames": []string{name}})

			alarms, _ := out["MetricAlarms"].([]any)
			if len(alarms) != 1 {
				t.Fatalf("want 1 alarm, got %v", out)
			}

			w, _ := alarms[0].(map[any]any)["EvaluationWindow"].(map[any]any)

			return normalizeCBORMap(w)
		},
	}
}

// normalizeCBORMap turns a decoded CBOR map into string keys, recursively.
func normalizeCBORMap(m map[any]any) map[string]any {
	if m == nil {
		return nil
	}

	out := map[string]any{}

	for k, v := range m {
		if inner, ok := v.(map[any]any); ok {
			out[k.(string)] = normalizeCBORMap(inner)
		} else {
			out[k.(string)] = v
		}
	}

	return out
}

func queryWindowWire(t *testing.T) windowWire {
	t.Helper()

	post := newClockQueryPoster(t, config.NewFakeClock(cw11Start))

	return windowWire{
		put: func(name string, period int, window map[string]any) string {
			form := url.Values{
				"Action": {"PutMetricAlarm"}, "AlarmName": {name}, "Namespace": {"W/App"}, "MetricName": {"M"},
				"Statistic": {"Sum"}, "ComparisonOperator": {"GreaterThanThreshold"}, "Threshold": {"1"},
				"EvaluationPeriods": {"1"}, "Period": {strconv.Itoa(period)},
			}

			for member, v := range window {
				fields, _ := v.(map[string]any)
				if len(fields) == 0 {
					form.Set("EvaluationWindow."+member, "")
				}

				for f, fv := range fields {
					form.Set("EvaluationWindow."+member+"."+f, fv.(string))
				}
			}

			return queryCode(post(form))
		},
		describe: func(name string) map[string]any {
			_, body := post(url.Values{"Action": {"DescribeAlarms"}, "AlarmNames.member.1": {name}})

			switch {
			case strings.Contains(body, "<WallClockWindow>"):
				fields := map[string]any{}
				if tz := xmlField(body, "Timezone"); tz != "" {
					fields["Timezone"] = tz
				}

				return map[string]any{"WallClockWindow": fields}
			case strings.Contains(body, "<SlidingWindow>"):
				return map[string]any{"SlidingWindow": map[string]any{}}
			default:
				return nil
			}
		},
	}
}

// PutMetricAlarm stores EvaluationWindow and DescribeAlarms returns it.
// Before CW-11 the field was dropped. A wall clock window needs a supported
// period and time zone, and the union takes exactly one member.
func TestWireEvaluationWindow(t *testing.T) {
	wall := map[string]any{"WallClockWindow": map[string]any{"Timezone": "+05:30"}}

	for name, mk := range map[string]func(*testing.T) windowWire{"cbor": cborWindowWire, "query": queryWindowWire} {
		t.Run(name, func(t *testing.T) {
			w := mk(t)

			if code := w.put("wc", 300, wall); code != "" {
				t.Fatalf("put wall clock: %s", code)
			}

			got := w.describe("wc")
			wc, _ := got["WallClockWindow"].(map[string]any)

			if wc == nil || wc["Timezone"] != "+05:30" {
				t.Fatalf("EvaluationWindow = %v, want WallClockWindow +05:30", got)
			}

			if code := w.put("sl", 120, map[string]any{"SlidingWindow": map[string]any{}}); code != "" {
				t.Fatalf("put sliding: %s", code)
			}

			if got := w.describe("sl"); got["SlidingWindow"] == nil {
				t.Fatalf("EvaluationWindow = %v, want SlidingWindow", got)
			}

			if code := w.put("none", 60, nil); code != "" || w.describe("none") != nil {
				t.Fatalf("omitted window: code %q, echoed %v", code, w.describe("none"))
			}

			for label, tc := range map[string]struct {
				period int
				window map[string]any
			}{
				"period 120": {120, wall},
				"bad zone":   {300, map[string]any{"WallClockWindow": map[string]any{"Timezone": "+01:03"}}},
				"both": {300, map[string]any{
					"SlidingWindow": map[string]any{}, "WallClockWindow": map[string]any{"Timezone": "UTC"},
				}},
			} {
				if code := w.put("bad", tc.period, tc.window); code != "ValidationError" {
					t.Errorf("%s: code = %q, want ValidationError", label, code)
				}
			}
		})
	}
}
