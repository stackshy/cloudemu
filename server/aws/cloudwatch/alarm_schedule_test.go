package cloudwatch_test

import (
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

	"github.com/stackshy/cloudemu/v2/config"
	cwprovider "github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	cwserver "github.com/stackshy/cloudemu/v2/server/aws/cloudwatch"
)

// scheduleWire drives the alarm-schedule cases over one protocol.
type scheduleWire struct {
	putValue  func(v float64)
	putAlarm  func()
	setState  func(state string)
	state     func() string
	histories func() []string
}

const schedAlarm = "sched"

func cborScheduleWire(t *testing.T, fc *config.FakeClock) scheduleWire {
	t.Helper()

	client, ctx := newCWClientClock(t, fc)

	must := func(err error) {
		t.Helper()

		if err != nil {
			t.Fatal(err)
		}
	}

	return scheduleWire{
		putValue: func(v float64) {
			_, err := client.PutMetricData(ctx, &awscw.PutMetricDataInput{
				Namespace: aws.String("Sched/App"),
				MetricData: []cwtypes.MetricDatum{{
					MetricName: aws.String("Err"), Value: aws.Float64(v), Timestamp: aws.Time(fc.Now()),
				}},
			})
			must(err)
		},
		putAlarm: func() {
			_, err := client.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
				AlarmName: aws.String(schedAlarm), Namespace: aws.String("Sched/App"), MetricName: aws.String("Err"),
				ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold, Threshold: aws.Float64(5),
				EvaluationPeriods: aws.Int32(1), Period: aws.Int32(300), Statistic: cwtypes.StatisticSum,
			})
			must(err)
		},
		setState: func(state string) {
			_, err := client.SetAlarmState(ctx, &awscw.SetAlarmStateInput{
				AlarmName: aws.String(schedAlarm), StateValue: cwtypes.StateValue(state), StateReason: aws.String("test"),
			})
			must(err)
		},
		state: func() string {
			out, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmNames: []string{schedAlarm}})
			must(err)

			if len(out.MetricAlarms) != 1 {
				t.Fatalf("want 1 alarm, got %d", len(out.MetricAlarms))
			}

			return string(out.MetricAlarms[0].StateValue)
		},
		histories: func() []string {
			out, err := client.DescribeAlarmHistory(ctx, &awscw.DescribeAlarmHistoryInput{AlarmName: aws.String(schedAlarm)})
			must(err)

			var s []string
			for _, h := range out.AlarmHistoryItems {
				s = append(s, aws.ToString(h.HistorySummary))
			}

			return s
		},
	}
}

func queryScheduleWire(t *testing.T, fc *config.FakeClock) scheduleWire {
	t.Helper()

	ts := httptest.NewServer(cwserver.New(cwprovider.New(config.NewOptions(config.WithClock(fc)))))
	t.Cleanup(ts.Close)

	post := func(form url.Values) string {
		t.Helper()

		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Authorization", monitoringAuth)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()

		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", form.Get("Action"), resp.StatusCode, b)
		}

		return string(b)
	}

	stateRe := regexp.MustCompile(`<StateValue>([A-Z_]+)</StateValue>`)
	summaryRe := regexp.MustCompile(`<HistorySummary>([^<]*)</HistorySummary>`)

	return scheduleWire{
		putValue: func(v float64) {
			post(url.Values{
				"Action": {"PutMetricData"}, "Namespace": {"Sched/App"},
				"MetricData.member.1.MetricName": {"Err"},
				"MetricData.member.1.Value":      {strconv.FormatFloat(v, 'f', -1, 64)},
				"MetricData.member.1.Timestamp":  {fc.Now().UTC().Format(time.RFC3339)},
			})
		},
		putAlarm: func() {
			post(url.Values{
				"Action": {"PutMetricAlarm"}, "AlarmName": {schedAlarm}, "Namespace": {"Sched/App"}, "MetricName": {"Err"},
				"ComparisonOperator": {"GreaterThanThreshold"}, "Threshold": {"5"}, "EvaluationPeriods": {"1"},
				"Period": {"300"}, "Statistic": {"Sum"},
			})
		},
		setState: func(state string) {
			post(url.Values{"Action": {"SetAlarmState"}, "AlarmName": {schedAlarm}, "StateValue": {state}, "StateReason": {"test"}})
		},
		state: func() string {
			m := stateRe.FindStringSubmatch(post(url.Values{"Action": {"DescribeAlarms"}, "AlarmNames.member.1": {schedAlarm}}))
			if m == nil {
				t.Fatal("no StateValue in DescribeAlarms")
			}

			return m[1]
		},
		histories: func() []string {
			var s []string
			for _, m := range summaryRe.FindAllStringSubmatch(post(url.Values{"Action": {"DescribeAlarmHistory"}, "AlarmName": {schedAlarm}}), -1) {
				s = append(s, m[1])
			}

			return s
		},
	}
}

func scheduleWires() map[string]func(*testing.T, *config.FakeClock) scheduleWire {
	return map[string]func(*testing.T, *config.FakeClock) scheduleWire{
		"cbor":  cborScheduleWire,
		"query": queryScheduleWire,
	}
}

// Data first, then PutMetricAlarm: the new alarm is evaluated at once and is
// ALARM on the next read. Before this fix it stayed INSUFFICIENT_DATA.
func TestWireAlarmCreatedAfterData(t *testing.T) {
	for name, mk := range scheduleWires() {
		t.Run(name, func(t *testing.T) {
			fc := config.NewFakeClock(time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC))
			w := mk(t, fc)

			w.putValue(10)
			w.putAlarm()

			if got := w.state(); got != "ALARM" {
				t.Fatalf("state = %s, want ALARM", got)
			}
		})
	}
}

// SetAlarmState holds for one interval, then the next read puts the real
// state back. DescribeAlarmHistory shows both changes.
func TestWireSetAlarmStateReverts(t *testing.T) {
	for name, mk := range scheduleWires() {
		t.Run(name, func(t *testing.T) {
			fc := config.NewFakeClock(time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC))
			w := mk(t, fc)

			w.putValue(1)
			w.putAlarm()
			w.setState("ALARM")

			if got := w.state(); got != "ALARM" {
				t.Fatalf("forced state = %s, want ALARM", got)
			}

			fc.Advance(time.Minute)

			h := w.histories()
			if len(h) != 3 {
				t.Fatalf("history = %q, want 3 entries", h)
			}

			if !strings.Contains(h[0], "from ALARM to OK") {
				t.Fatalf("newest history = %q, want the ALARM to OK revert", h[0])
			}

			if got := w.state(); got != "OK" {
				t.Fatalf("state after revert = %s, want OK", got)
			}
		})
	}
}
