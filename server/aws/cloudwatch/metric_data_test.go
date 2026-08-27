package cloudwatch_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"github.com/stackshy/cloudemu/v2/config"
	cwprovider "github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	cwserver "github.com/stackshy/cloudemu/v2/server/aws/cloudwatch"
)

// newCWClient wires the real aws-sdk-go-v2 CloudWatch client at an in-process
// CloudWatch handler backed by the in-memory provider.
func newCWClient(t *testing.T) (*awscw.Client, context.Context) {
	t.Helper()

	h := cwserver.New(cwprovider.New(config.NewOptions()))
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	client := awscw.NewFromConfig(cfg, func(o *awscw.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	return client, context.Background()
}

// TestSDKPutMetricDataDefaultTimestamp covers finding 1: a datum with no
// Timestamp must be stored at request-receipt time (not the Go zero value), so
// it is queryable immediately afterward.
func TestSDKPutMetricDataDefaultTimestamp(t *testing.T) {
	client, ctx := newCWClient(t)

	if _, err := client.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("MyApp"),
		MetricData: []cwtypes.MetricDatum{{
			MetricName: aws.String("Requests"),
			Value:      aws.Float64(42),
			// No Timestamp on purpose.
		}},
	}); err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}

	now := time.Now().UTC()
	out, err := client.GetMetricStatistics(ctx, &awscw.GetMetricStatisticsInput{
		Namespace:  aws.String("MyApp"),
		MetricName: aws.String("Requests"),
		StartTime:  aws.Time(now.Add(-1 * time.Hour)),
		EndTime:    aws.Time(now.Add(1 * time.Hour)),
		Period:     aws.Int32(60),
		Statistics: []cwtypes.Statistic{cwtypes.StatisticSum},
	})
	if err != nil {
		t.Fatalf("GetMetricStatistics: %v", err)
	}

	if len(out.Datapoints) != 1 || out.Datapoints[0].Sum == nil || *out.Datapoints[0].Sum != 42 {
		t.Fatalf("datapoints = %+v, want one datapoint with Sum=42 (default timestamp must be now)", out.Datapoints)
	}
}

// TestSDKPutMetricDataFiresAlarm covers finding 1's alarm consequence: without a
// receipt-time default, alarms stay INSUFFICIENT_DATA forever.
func TestSDKPutMetricDataFiresAlarm(t *testing.T) {
	client, ctx := newCWClient(t)

	if _, err := client.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
		AlarmName:          aws.String("high"),
		Namespace:          aws.String("MyApp"),
		MetricName:         aws.String("Errors"),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		Threshold:          aws.Float64(10),
		EvaluationPeriods:  aws.Int32(1),
		Period:             aws.Int32(60),
		Statistic:          cwtypes.StatisticAverage,
	}); err != nil {
		t.Fatalf("PutMetricAlarm: %v", err)
	}

	if _, err := client.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("MyApp"),
		MetricData: []cwtypes.MetricDatum{{
			MetricName: aws.String("Errors"),
			Value:      aws.Float64(99),
		}},
	}); err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}

	out, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmNames: []string{"high"}})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}

	if len(out.MetricAlarms) != 1 || out.MetricAlarms[0].StateValue != cwtypes.StateValueAlarm {
		t.Fatalf("alarm state = %+v, want ALARM", out.MetricAlarms)
	}
}

// TestSDKGetMetricData covers finding 3: the modern GetMetricData read API must
// be dispatched and return the stored series.
func TestSDKGetMetricData(t *testing.T) {
	client, ctx := newCWClient(t)

	base := time.Now().UTC().Truncate(time.Minute)
	for i, v := range []float64{10, 20, 30} {
		if _, err := client.PutMetricData(ctx, &awscw.PutMetricDataInput{
			Namespace: aws.String("MyApp"),
			MetricData: []cwtypes.MetricDatum{{
				MetricName: aws.String("Latency"),
				Value:      aws.Float64(v),
				Timestamp:  aws.Time(base.Add(time.Duration(i) * time.Minute)),
			}},
		}); err != nil {
			t.Fatalf("PutMetricData: %v", err)
		}
	}

	out, err := client.GetMetricData(ctx, &awscw.GetMetricDataInput{
		StartTime: aws.Time(base.Add(-time.Minute)),
		EndTime:   aws.Time(base.Add(10 * time.Minute)),
		MetricDataQueries: []cwtypes.MetricDataQuery{{
			Id: aws.String("q1"),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  aws.String("MyApp"),
					MetricName: aws.String("Latency"),
				},
				Period: aws.Int32(60),
				Stat:   aws.String("Sum"),
			},
		}},
	})
	if err != nil {
		t.Fatalf("GetMetricData: %v", err)
	}

	if len(out.MetricDataResults) != 1 {
		t.Fatalf("results = %+v, want 1", out.MetricDataResults)
	}

	res := out.MetricDataResults[0]
	if aws.ToString(res.Id) != "q1" {
		t.Fatalf("result Id = %q, want q1", aws.ToString(res.Id))
	}

	var total float64
	for _, v := range res.Values {
		total += v
	}

	if total != 60 {
		t.Fatalf("summed values = %v, want 60 (10+20+30)", total)
	}
}

// TestSDKGetMetricStatisticsUnit covers finding 8: the stored unit must be
// echoed, not hardcoded to "Count".
func TestSDKGetMetricStatisticsUnit(t *testing.T) {
	client, ctx := newCWClient(t)

	now := time.Now().UTC()
	if _, err := client.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("AWS/EC2"),
		MetricData: []cwtypes.MetricDatum{{
			MetricName: aws.String("CPUUtilization"),
			Value:      aws.Float64(75),
			Unit:       cwtypes.StandardUnitPercent,
			Timestamp:  aws.Time(now),
		}},
	}); err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}

	out, err := client.GetMetricStatistics(ctx, &awscw.GetMetricStatisticsInput{
		Namespace:  aws.String("AWS/EC2"),
		MetricName: aws.String("CPUUtilization"),
		StartTime:  aws.Time(now.Add(-time.Hour)),
		EndTime:    aws.Time(now.Add(time.Hour)),
		Period:     aws.Int32(60),
		Statistics: []cwtypes.Statistic{cwtypes.StatisticAverage},
	})
	if err != nil {
		t.Fatalf("GetMetricStatistics: %v", err)
	}

	if len(out.Datapoints) != 1 || out.Datapoints[0].Unit != cwtypes.StandardUnitPercent {
		t.Fatalf("unit = %+v, want Percent", out.Datapoints)
	}
}

// TestSDKListMetricsDimensions covers finding 6: ListMetrics must return each
// metric's dimensions and honor a DimensionFilter.
func TestSDKListMetricsDimensions(t *testing.T) {
	client, ctx := newCWClient(t)

	put := func(instance string) {
		if _, err := client.PutMetricData(ctx, &awscw.PutMetricDataInput{
			Namespace: aws.String("AWS/EC2"),
			MetricData: []cwtypes.MetricDatum{{
				MetricName: aws.String("CPUUtilization"),
				Value:      aws.Float64(1),
				Dimensions: []cwtypes.Dimension{{
					Name:  aws.String("InstanceId"),
					Value: aws.String(instance),
				}},
			}},
		}); err != nil {
			t.Fatalf("PutMetricData: %v", err)
		}
	}

	put("i-aaa")
	put("i-bbb")

	all, err := client.ListMetrics(ctx, &awscw.ListMetricsInput{Namespace: aws.String("AWS/EC2")})
	if err != nil {
		t.Fatalf("ListMetrics: %v", err)
	}

	if len(all.Metrics) != 2 {
		t.Fatalf("metrics = %+v, want 2 (one per InstanceId)", all.Metrics)
	}

	for _, m := range all.Metrics {
		if len(m.Dimensions) != 1 || aws.ToString(m.Dimensions[0].Name) != "InstanceId" {
			t.Fatalf("metric %+v missing InstanceId dimension", m)
		}
	}

	filtered, err := client.ListMetrics(ctx, &awscw.ListMetricsInput{
		Namespace: aws.String("AWS/EC2"),
		Dimensions: []cwtypes.DimensionFilter{{
			Name:  aws.String("InstanceId"),
			Value: aws.String("i-aaa"),
		}},
	})
	if err != nil {
		t.Fatalf("ListMetrics filtered: %v", err)
	}

	if len(filtered.Metrics) != 1 || aws.ToString(filtered.Metrics[0].Dimensions[0].Value) != "i-aaa" {
		t.Fatalf("filtered metrics = %+v, want only i-aaa", filtered.Metrics)
	}
}

// TestSDKListMetricsPagination guards that ListMetrics pages at 500 metrics and
// hands back a NextToken. Without pagination every metric comes back on the
// first page and NextToken is empty, wedging the SDK paginator.
func TestSDKListMetricsPagination(t *testing.T) {
	client, ctx := newCWClient(t)

	// AWS pages ListMetrics at 500; put 501 distinct metrics in one call so the
	// first page must spill into a second.
	const total = 501

	// PutMetricData accepts up to 1000 datums per call; send in batches of 100 to
	// stay well within any single-request limits.
	const batch = 100

	for start := 0; start < total; start += batch {
		data := make([]cwtypes.MetricDatum, 0, batch)
		for i := start; i < start+batch && i < total; i++ {
			data = append(data, cwtypes.MetricDatum{
				MetricName: aws.String(fmt.Sprintf("m-%04d", i)),
				Value:      aws.Float64(1),
			})
		}

		if _, err := client.PutMetricData(ctx, &awscw.PutMetricDataInput{
			Namespace:  aws.String("Paged/Metrics"),
			MetricData: data,
		}); err != nil {
			t.Fatalf("PutMetricData: %v", err)
		}
	}

	first, err := client.ListMetrics(ctx, &awscw.ListMetricsInput{Namespace: aws.String("Paged/Metrics")})
	if err != nil {
		t.Fatalf("ListMetrics page 1: %v", err)
	}

	if len(first.Metrics) != 500 {
		t.Fatalf("page 1 = %d metrics, want 500 (pagination missing?)", len(first.Metrics))
	}

	if aws.ToString(first.NextToken) == "" {
		t.Fatalf("page 1 NextToken empty, want a token for the 501st metric")
	}

	second, err := client.ListMetrics(ctx, &awscw.ListMetricsInput{
		Namespace: aws.String("Paged/Metrics"),
		NextToken: first.NextToken,
	})
	if err != nil {
		t.Fatalf("ListMetrics page 2: %v", err)
	}

	if len(second.Metrics) != 1 {
		t.Fatalf("page 2 = %d metrics, want 1", len(second.Metrics))
	}

	if aws.ToString(second.NextToken) != "" {
		t.Fatalf("page 2 NextToken = %q, want empty (pagination finished)", aws.ToString(second.NextToken))
	}
}
