package cloudwatch_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// putPoint puts one datum at ts.
func putPoint(ctx context.Context, t *testing.T, client *awscw.Client, ns, name string, dims map[string]string,
	v float64, ts time.Time,
) {
	t.Helper()

	d := cwtypes.MetricDatum{MetricName: aws.String(name), Value: aws.Float64(v), Timestamp: aws.Time(ts)}
	for k, val := range dims {
		d.Dimensions = append(d.Dimensions, cwtypes.Dimension{Name: aws.String(k), Value: aws.String(val)})
	}

	if _, err := client.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String(ns), MetricData: []cwtypes.MetricDatum{d},
	}); err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}
}

func metricQuery(id, ns, name string) cwtypes.MetricDataQuery {
	return cwtypes.MetricDataQuery{
		Id: aws.String(id),
		MetricStat: &cwtypes.MetricStat{
			Metric: &cwtypes.Metric{Namespace: aws.String(ns), MetricName: aws.String(name)},
			Period: aws.Int32(60), Stat: aws.String("Sum"),
		},
		ReturnData: aws.Bool(false),
	}
}

// TestSDKGetMetricDataFILLAndIF drives FILL and IF through GetMetricData.
// Over five one-minute periods m1 has data at minutes 1 and 3 only.
func TestSDKGetMetricDataFILLAndIF(t *testing.T) {
	client, ctx := newCWClient(t)

	start := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	putPoint(ctx, t, client, "MathApp", "Errors", nil, 10, start.Add(1*time.Minute))
	putPoint(ctx, t, client, "MathApp", "Errors", nil, 30, start.Add(3*time.Minute))

	out, err := client.GetMetricData(ctx, &awscw.GetMetricDataInput{
		StartTime: aws.Time(start),
		EndTime:   aws.Time(start.Add(5 * time.Minute)),
		ScanBy:    cwtypes.ScanByTimestampAscending,
		MetricDataQueries: []cwtypes.MetricDataQuery{
			metricQuery("m1", "MathApp", "Errors"),
			{Id: aws.String("zero"), Expression: aws.String("FILL(m1, 0)")},
			{Id: aws.String("repeat"), Expression: aws.String("FILL(m1, REPEAT)")},
			{Id: aws.String("linear"), Expression: aws.String("FILL(m1, LINEAR)")},
			{Id: aws.String("high"), Expression: aws.String("IF(m1 > 15, 1, 0)")},
		},
	})
	if err != nil {
		t.Fatalf("GetMetricData: %v", err)
	}

	want := map[string][]float64{
		"zero":   {0, 10, 0, 30, 0},
		"repeat": {10, 10, 30, 30},
		"linear": {10, 20, 30},
		"high":   {0, 1},
	}

	if len(out.MetricDataResults) != len(want) {
		t.Fatalf("results = %d, want %d", len(out.MetricDataResults), len(want))
	}

	for _, r := range out.MetricDataResults {
		id := aws.ToString(r.Id)
		if !floatsEqual(r.Values, want[id]) {
			t.Errorf("%s values = %v, want %v", id, r.Values, want[id])
		}
	}
}

// TestSDKGetMetricDataSEARCH returns one result per found metric, all with
// the query's Id, read with the search's statistic.
func TestSDKGetMetricDataSEARCH(t *testing.T) {
	client, ctx := newCWClient(t)

	start := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	putPoint(ctx, t, client, "SearchApp", "Latency", map[string]string{"Host": "a"}, 5, start)
	putPoint(ctx, t, client, "SearchApp", "Latency", map[string]string{"Host": "a"}, 7, start)
	putPoint(ctx, t, client, "SearchApp", "Latency", map[string]string{"Host": "b"}, 9, start)
	putPoint(ctx, t, client, "SearchApp", "Latency", map[string]string{"Host": "b", "Zone": "z1"}, 1, start)
	putPoint(ctx, t, client, "SearchApp", "Errors", map[string]string{"Host": "a"}, 1, start)

	out, err := client.GetMetricData(ctx, &awscw.GetMetricDataInput{
		StartTime: aws.Time(start),
		EndTime:   aws.Time(start.Add(time.Minute)),
		MetricDataQueries: []cwtypes.MetricDataQuery{{
			Id:         aws.String("s1"),
			Expression: aws.String(`SEARCH('{SearchApp,Host} MetricName="Latency"', 'Maximum', 60)`),
		}},
	})
	if err != nil {
		t.Fatalf("GetMetricData: %v", err)
	}

	got := map[string][]float64{}

	for _, r := range out.MetricDataResults {
		if aws.ToString(r.Id) != "s1" {
			t.Fatalf("result Id = %q, want s1", aws.ToString(r.Id))
		}

		got[aws.ToString(r.Label)] = r.Values
	}

	want := map[string][]float64{"a Latency": {7}, "b Latency": {9}}
	if len(got) != len(want) {
		t.Fatalf("results = %v, want %v", got, want)
	}

	for label, v := range want {
		if !floatsEqual(got[label], v) {
			t.Errorf("%s = %v, want %v", label, got[label], v)
		}
	}
}

// TestSDKGetMetricDataFILLOverSEARCHNoPeriod fills a SEARCH on the step the
// SEARCH reads at when the query has no Period: the SEARCH's own period
// argument, else 60 seconds.
func TestSDKGetMetricDataFILLOverSEARCHNoPeriod(t *testing.T) {
	client, ctx := newCWClient(t)

	start := time.Now().UTC().Truncate(time.Minute).Add(-30 * time.Minute)
	putPoint(ctx, t, client, "FillSearch", "Hits", map[string]string{"Host": "a"}, 4, start.Add(5*time.Minute))

	tests := []struct {
		name string
		expr string
		want []float64
	}{
		{"explicit period", `FILL(SEARCH('{FillSearch,Host} MetricName="Hits"', 'Sum', 300), 0)`, []float64{0, 4, 0, 0}},
		{"default period", `FILL(SEARCH('{FillSearch,Host} MetricName="Hits"', 'Sum'), 0)`,
			[]float64{0, 0, 0, 0, 0, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := client.GetMetricData(ctx, &awscw.GetMetricDataInput{
				StartTime: aws.Time(start),
				EndTime:   aws.Time(start.Add(20 * time.Minute)),
				ScanBy:    cwtypes.ScanByTimestampAscending,
				MetricDataQueries: []cwtypes.MetricDataQuery{
					{Id: aws.String("f1"), Expression: aws.String(tc.expr)},
				},
			})
			if err != nil {
				t.Fatalf("GetMetricData: %v", err)
			}

			if len(out.MetricDataResults) != 1 {
				t.Fatalf("results = %d, want 1", len(out.MetricDataResults))
			}

			if got := out.MetricDataResults[0].Values; !floatsEqual(got, tc.want) {
				t.Fatalf("values = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSDKGetMetricDataArithmeticMissingAsZero drives the user guide's series,
// metric1 [30, 20, 0, 0] and metric2 [20, -, 20, -], through GetMetricData:
// arithmetic reads a missing point as 0, and division by zero drops the point.
func TestSDKGetMetricDataArithmeticMissingAsZero(t *testing.T) {
	client, ctx := newCWClient(t)

	start := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	for i, v := range []float64{30, 20, 0, 0} {
		putPoint(ctx, t, client, "ArithApp", "M1", nil, v, start.Add(time.Duration(i)*time.Minute))
	}

	putPoint(ctx, t, client, "ArithApp", "M2", nil, 20, start)
	putPoint(ctx, t, client, "ArithApp", "M2", nil, 20, start.Add(2*time.Minute))

	out, err := client.GetMetricData(ctx, &awscw.GetMetricDataInput{
		StartTime: aws.Time(start),
		EndTime:   aws.Time(start.Add(4 * time.Minute)),
		ScanBy:    cwtypes.ScanByTimestampAscending,
		MetricDataQueries: []cwtypes.MetricDataQuery{
			metricQuery("m1", "ArithApp", "M1"),
			metricQuery("m2", "ArithApp", "M2"),
			{Id: aws.String("diff"), Expression: aws.String("m1 - m2")},
			{Id: aws.String("ratio"), Expression: aws.String("m1 / m2")},
		},
	})
	if err != nil {
		t.Fatalf("GetMetricData: %v", err)
	}

	want := map[string][]float64{"diff": {10, 20, -20, 0}, "ratio": {1.5, 0}}

	for _, r := range out.MetricDataResults {
		id := aws.ToString(r.Id)
		if !floatsEqual(r.Values, want[id]) {
			t.Errorf("%s values = %v, want %v", id, r.Values, want[id])
		}
	}
}
