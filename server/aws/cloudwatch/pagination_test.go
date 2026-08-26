package cloudwatch_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"github.com/stackshy/cloudemu/v2/config"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"

	cwprovider "github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	cwserver "github.com/stackshy/cloudemu/v2/server/aws/cloudwatch"
)

// TestSDKDescribeAlarmHistoryPaginatorRetrievesAll is the data-loss regression:
// with a history larger than one page, the paginator must retrieve every entry
// across pages. Before the fix the handler truncated to MaxRecords and emitted no
// NextToken, so page-2+ entries were silently unreachable.
func TestSDKDescribeAlarmHistoryPaginatorRetrievesAll(t *testing.T) {
	client, ctx := newCWClient(t)

	const alarmName = "hist-alarm"
	if _, err := client.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
		AlarmName:          aws.String(alarmName),
		Namespace:          aws.String("MyApp"),
		MetricName:         aws.String("Latency"),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		EvaluationPeriods:  aws.Int32(1),
		Period:             aws.Int32(60),
		Threshold:          aws.Float64(10),
		Statistic:          cwtypes.StatisticAverage,
	}); err != nil {
		t.Fatalf("PutMetricAlarm: %v", err)
	}

	// Alternate the alarm state so each SetAlarmState records one history entry.
	states := []cwtypes.StateValue{
		cwtypes.StateValueAlarm, cwtypes.StateValueOk, cwtypes.StateValueAlarm,
		cwtypes.StateValueOk, cwtypes.StateValueAlarm,
	}
	for i, s := range states {
		if _, err := client.SetAlarmState(ctx, &awscw.SetAlarmStateInput{
			AlarmName:   aws.String(alarmName),
			StateValue:  s,
			StateReason: aws.String(fmt.Sprintf("transition %d", i)),
		}); err != nil {
			t.Fatalf("SetAlarmState[%d]: %v", i, err)
		}
	}

	// Ground truth: a single unpaged read (history < the default page cap) returns
	// every recorded entry, so its count is the total we must retrieve by paging.
	full, err := client.DescribeAlarmHistory(ctx, &awscw.DescribeAlarmHistoryInput{
		AlarmName: aws.String(alarmName),
	})
	if err != nil {
		t.Fatalf("DescribeAlarmHistory: %v", err)
	}

	total := len(full.AlarmHistoryItems)
	if total != len(states) {
		t.Fatalf("recorded history = %d, want %d", total, len(states))
	}

	// Page two at a time and collect every entry the paginator yields.
	seen := map[string]int{}
	pages := 0
	p := awscw.NewDescribeAlarmHistoryPaginator(client, &awscw.DescribeAlarmHistoryInput{
		AlarmName:  aws.String(alarmName),
		MaxRecords: aws.Int32(2),
	})

	for p.HasMorePages() {
		out, perr := p.NextPage(ctx)
		if perr != nil {
			t.Fatalf("NextPage: %v", perr)
		}

		pages++
		for _, it := range out.AlarmHistoryItems {
			seen[aws.ToTime(it.Timestamp).Format(time.RFC3339Nano)+"|"+aws.ToString(it.HistorySummary)]++
		}
	}

	if pages < 2 {
		t.Fatalf("pages = %d, want > 1 (history should span multiple pages)", pages)
	}

	if len(seen) != total {
		t.Fatalf("distinct entries retrieved = %d, want %d (data lost across pages)", len(seen), total)
	}

	for k, n := range seen {
		if n != 1 {
			t.Fatalf("entry %q retrieved %d times, want exactly once", k, n)
		}
	}
}

// TestSDKGetMetricDataPaginatorWalksAllResults verifies GetMetricData paginates:
// with MaxDatapoints forcing one result row per page, the paginator must walk
// every query result once and terminate.
func TestSDKGetMetricDataPaginatorWalksAllResults(t *testing.T) {
	client, ctx := newCWClient(t)

	base := time.Now().UTC().Truncate(time.Minute)
	names := []string{"M1", "M2", "M3"}
	for _, name := range names {
		if _, err := client.PutMetricData(ctx, &awscw.PutMetricDataInput{
			Namespace: aws.String("MyApp"),
			MetricData: []cwtypes.MetricDatum{{
				MetricName: aws.String(name),
				Value:      aws.Float64(1),
				Timestamp:  aws.Time(base),
			}},
		}); err != nil {
			t.Fatalf("PutMetricData(%s): %v", name, err)
		}
	}

	queries := make([]cwtypes.MetricDataQuery, 0, len(names))
	for i, name := range names {
		queries = append(queries, cwtypes.MetricDataQuery{
			Id: aws.String(fmt.Sprintf("q%d", i)),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{Namespace: aws.String("MyApp"), MetricName: aws.String(name)},
				Period: aws.Int32(60),
				Stat:   aws.String("Sum"),
			},
		})
	}

	p := awscw.NewGetMetricDataPaginator(client, &awscw.GetMetricDataInput{
		StartTime:         aws.Time(base.Add(-time.Minute)),
		EndTime:           aws.Time(base.Add(time.Minute)),
		MetricDataQueries: queries,
		MaxDatapoints:     aws.Int32(1),
	})

	seen := map[string]int{}
	pages := 0

	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		pages++
		for _, res := range out.MetricDataResults {
			seen[aws.ToString(res.Id)]++
		}
	}

	if pages < 2 {
		t.Fatalf("pages = %d, want > 1", pages)
	}

	if len(seen) != len(names) {
		t.Fatalf("distinct results = %d, want %d", len(seen), len(names))
	}

	for id, n := range seen {
		if n != 1 {
			t.Fatalf("result %q seen %d times, want once", id, n)
		}
	}
}

// TestSDKDescribeAlarmsCborPaginates covers the removed early-return: an unpaged
// DescribeAlarms once returned everything without a NextToken. It must now cap a
// page (100) and hand back a token so the paginator walks all alarms.
func TestSDKDescribeAlarmsCborPaginates(t *testing.T) {
	client, ctx := newCWClient(t)

	const total = 101
	for i := 0; i < total; i++ {
		if _, err := client.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
			AlarmName:          aws.String(fmt.Sprintf("alarm-%03d", i)),
			Namespace:          aws.String("MyApp"),
			MetricName:         aws.String("Latency"),
			ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
			EvaluationPeriods:  aws.Int32(1),
			Period:             aws.Int32(60),
			Threshold:          aws.Float64(10),
			Statistic:          cwtypes.StatisticAverage,
		}); err != nil {
			t.Fatalf("PutMetricAlarm[%d]: %v", i, err)
		}
	}

	// No paging inputs: the default path must still paginate, not dump everything.
	seen := map[string]int{}
	pages := 0
	p := awscw.NewDescribeAlarmsPaginator(client, &awscw.DescribeAlarmsInput{})

	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		pages++
		for _, a := range out.MetricAlarms {
			seen[aws.ToString(a.AlarmName)]++
		}
	}

	if pages < 2 {
		t.Fatalf("pages = %d, want > 1 (default page must cap at 100)", pages)
	}

	if len(seen) != total {
		t.Fatalf("distinct alarms = %d, want %d", len(seen), total)
	}
}

// newSeededQueryServer builds a CloudWatch handler over a directly-seeded
// provider and returns the provider, server URL, and a query-protocol POST
// helper (form-encoded, monitoring-scoped, XML back).
func newSeededQueryServer(t *testing.T) (*cwprovider.Mock, func(url.Values) (int, string)) {
	t.Helper()

	prov := cwprovider.New(config.NewOptions())
	ts := httptest.NewServer(cwserver.New(prov))
	t.Cleanup(ts.Close)

	post := func(form url.Values) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(form.Encode()))
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

	return prov, post
}

// xmlToken extracts a <NextToken> value from a query-protocol XML body, or "".
func xmlToken(body string) string {
	const openTag, closeTag = "<NextToken>", "</NextToken>"

	i := strings.Index(body, openTag)
	if i < 0 {
		return ""
	}

	j := strings.Index(body[i:], closeTag)
	if j < 0 {
		return ""
	}

	return body[i+len(openTag) : i+j]
}

// TestQueryListMetricsPaginates walks the legacy XML ListMetrics path across more
// than one 500-metric page and asserts every metric is returned exactly once.
func TestQueryListMetricsPaginates(t *testing.T) {
	prov, post := newSeededQueryServer(t)

	const total = 501
	data := make([]mondriver.MetricDatum, 0, total)
	for i := 0; i < total; i++ {
		data = append(data, mondriver.MetricDatum{
			Namespace: "MyApp", MetricName: fmt.Sprintf("M%04d", i), Value: 1, Timestamp: time.Now().UTC(),
		})
	}

	if err := prov.PutMetricData(context.Background(), data); err != nil {
		t.Fatalf("seed PutMetricData: %v", err)
	}

	seen := map[string]int{}
	token := ""
	pages := 0

	for {
		form := url.Values{"Action": {"ListMetrics"}, "Namespace": {"MyApp"}}
		if token != "" {
			form.Set("NextToken", token)
		}

		code, body := post(form)
		if code != http.StatusOK {
			t.Fatalf("ListMetrics: code=%d body=%s", code, body)
		}

		pages++
		for _, name := range extractTagValues(body, "MetricName") {
			seen[name]++
		}

		token = xmlToken(body)
		if token == "" {
			break
		}
	}

	if pages < 2 {
		t.Fatalf("pages = %d, want > 1", pages)
	}

	if len(seen) != total {
		t.Fatalf("distinct metrics = %d, want %d", len(seen), total)
	}
}

// TestQueryDescribeAlarmsPaginates walks the legacy XML DescribeAlarms path with
// MaxRecords forcing small pages and asserts every alarm is returned once.
func TestQueryDescribeAlarmsPaginates(t *testing.T) {
	prov, post := newSeededQueryServer(t)

	const total = 3
	for i := 0; i < total; i++ {
		if err := prov.CreateAlarm(context.Background(), mondriver.AlarmConfig{
			Name: fmt.Sprintf("alarm-%d", i), Namespace: "MyApp", MetricName: "Latency",
			ComparisonOperator: "GreaterThanThreshold", Threshold: 10, Period: 60, EvaluationPeriods: 1,
		}); err != nil {
			t.Fatalf("seed CreateAlarm[%d]: %v", i, err)
		}
	}

	seen := map[string]int{}
	token := ""
	pages := 0

	for {
		form := url.Values{"Action": {"DescribeAlarms"}, "MaxRecords": {"2"}}
		if token != "" {
			form.Set("NextToken", token)
		}

		code, body := post(form)
		if code != http.StatusOK {
			t.Fatalf("DescribeAlarms: code=%d body=%s", code, body)
		}

		pages++
		for _, name := range extractTagValues(body, "AlarmName") {
			seen[name]++
		}

		token = xmlToken(body)
		if token == "" {
			break
		}
	}

	if pages < 2 {
		t.Fatalf("pages = %d, want > 1", pages)
	}

	if len(seen) != total {
		t.Fatalf("distinct alarms = %d, want %d", len(seen), total)
	}
}

// extractTagValues returns every value enclosed in <tag>...</tag> in body.
func extractTagValues(body, tag string) []string {
	openTag, closeTag := "<"+tag+">", "</"+tag+">"

	var out []string

	rest := body
	for {
		i := strings.Index(rest, openTag)
		if i < 0 {
			return out
		}

		rest = rest[i+len(openTag):]

		j := strings.Index(rest, closeTag)
		if j < 0 {
			return out
		}

		out = append(out, rest[:j])
		rest = rest[j+len(closeTag):]
	}
}
