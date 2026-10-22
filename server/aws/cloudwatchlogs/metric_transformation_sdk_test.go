package cloudwatchlogs_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newLogsAndMetricsClients serves CloudWatch Logs and CloudWatch from one
// provider, so metric filter output can be read back with GetMetricStatistics.
func newLogsAndMetricsClients(t *testing.T) (*cwl.Client, *cloudwatch.Client) {
	t.Helper()

	p := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{CloudWatchLogs: p.CloudWatchLogs, CloudWatch: p.CloudWatch}))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return cwl.NewFromConfig(cfg), cloudwatch.NewFromConfig(cfg)
}

func metricSum(t *testing.T, cw *cloudwatch.Client, name string, dims ...cwtypes.Dimension) (float64, cwtypes.StandardUnit) {
	t.Helper()

	now := time.Now()

	out, err := cw.GetMetricStatistics(context.Background(), &cloudwatch.GetMetricStatisticsInput{
		Namespace: aws.String("E2E/App"), MetricName: aws.String(name), Dimensions: dims,
		StartTime: aws.Time(now.Add(-time.Hour)), EndTime: aws.Time(now.Add(time.Hour)),
		Period: aws.Int32(7200), Statistics: []cwtypes.Statistic{cwtypes.StatisticSum},
	})
	if err != nil {
		t.Fatalf("GetMetricStatistics(%s): %v", name, err)
	}

	if len(out.Datapoints) == 0 {
		return 0, ""
	}

	return aws.ToFloat64(out.Datapoints[0].Sum), out.Datapoints[0].Unit
}

func TestSDKMetricTransformationDimensionsUnitDefault(t *testing.T) {
	logs, cw := newLogsAndMetricsClients(t)
	ctx := context.Background()
	g, s := aws.String("/mt/g"), aws.String("s")

	if _, err := logs.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: g}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	if _, err := logs.CreateLogStream(ctx, &cwl.CreateLogStreamInput{LogGroupName: g, LogStreamName: s}); err != nil {
		t.Fatalf("CreateLogStream: %v", err)
	}

	put := func(name, pattern, value string, mt cwltypes.MetricTransformation) error {
		mt.MetricName, mt.MetricNamespace, mt.MetricValue = aws.String(name), aws.String("E2E/App"), aws.String(value)
		_, err := logs.PutMetricFilter(ctx, &cwl.PutMetricFilterInput{
			LogGroupName: g, FilterName: aws.String(name), FilterPattern: aws.String(pattern),
			MetricTransformations: []cwltypes.MetricTransformation{mt},
		})

		return err
	}

	if err := put("Latency", `{ $.latency > 0 }`, "$.latency", cwltypes.MetricTransformation{
		Unit: cwltypes.StandardUnitMilliseconds, Dimensions: map[string]string{"Service": "$.service"},
	}); err != nil {
		t.Fatalf("PutMetricFilter Latency: %v", err)
	}

	if err := put("Fatal", "FATAL", "1", cwltypes.MetricTransformation{DefaultValue: aws.Float64(0)}); err != nil {
		t.Fatalf("PutMetricFilter Fatal: %v", err)
	}

	now := time.Now()
	msgs := []string{`{"service":"api","latency":10}`, `{"service":"api","latency":5}`, `{"service":"web","latency":7}`}

	events := make([]cwltypes.InputLogEvent, 0, len(msgs))
	for i, m := range msgs {
		events = append(events, cwltypes.InputLogEvent{
			Timestamp: aws.Int64(now.Add(time.Duration(i) * time.Millisecond).UnixMilli()), Message: aws.String(m),
		})
	}

	if _, err := logs.PutLogEvents(ctx, &cwl.PutLogEventsInput{LogGroupName: g, LogStreamName: s, LogEvents: events}); err != nil {
		t.Fatalf("PutLogEvents: %v", err)
	}

	api := cwtypes.Dimension{Name: aws.String("Service"), Value: aws.String("api")}
	if sum, unit := metricSum(t, cw, "Latency", api); sum != 15 || unit != cwtypes.StandardUnitMilliseconds {
		t.Fatalf("Latency{Service=api} = %v %s, want 15 Milliseconds", sum, unit)
	}

	web := cwtypes.Dimension{Name: aws.String("Service"), Value: aws.String("web")}
	if sum, _ := metricSum(t, cw, "Latency", web); sum != 7 {
		t.Fatalf("Latency{Service=web} = %v, want 7", sum)
	}

	// No FATAL in the batch: the default value is published instead.
	stats, err := cw.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{
		Namespace: aws.String("E2E/App"), MetricName: aws.String("Fatal"),
		StartTime: aws.Time(now.Add(-time.Hour)), EndTime: aws.Time(now.Add(time.Hour)),
		Period: aws.Int32(7200), Statistics: []cwtypes.Statistic{cwtypes.StatisticSampleCount},
	})
	if err != nil || len(stats.Datapoints) != 1 || aws.ToFloat64(stats.Datapoints[0].SampleCount) != 1 {
		t.Fatalf("Fatal default value datapoints = %+v, %v; want one sample", stats.Datapoints, err)
	}

	var invalid *cwltypes.InvalidParameterException

	if err := put("Bad", `{ $.level = * }`, "1", cwltypes.MetricTransformation{
		DefaultValue: aws.Float64(0), Dimensions: map[string]string{"Level": "$.level"},
	}); !errors.As(err, &invalid) {
		t.Fatalf("dimensions with default value: got %v, want InvalidParameterException", err)
	}

	if err := put("Bad", "ERROR", "$size", cwltypes.MetricTransformation{}); !errors.As(err, &invalid) {
		t.Fatalf("field value on unstructured pattern: got %v, want InvalidParameterException", err)
	}
}

func TestSDKDescribeMetricFiltersFiltersAndPaging(t *testing.T) {
	logs := newLogsClient(t)
	ctx := context.Background()

	for _, group := range []string{"/dmf/a", "/dmf/b"} {
		if _, err := logs.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String(group)}); err != nil {
			t.Fatalf("CreateLogGroup: %v", err)
		}

		for i := range 3 {
			if _, err := logs.PutMetricFilter(ctx, &cwl.PutMetricFilterInput{
				LogGroupName: aws.String(group), FilterName: aws.String(fmt.Sprintf("f%d", i)),
				FilterPattern: aws.String("ERROR"),
				MetricTransformations: []cwltypes.MetricTransformation{{
					MetricName: aws.String(fmt.Sprintf("M%d", i)), MetricNamespace: aws.String("NS"), MetricValue: aws.String("1"),
				}},
			}); err != nil {
				t.Fatalf("PutMetricFilter: %v", err)
			}
		}
	}

	page, err := logs.DescribeMetricFilters(ctx, &cwl.DescribeMetricFiltersInput{Limit: aws.Int32(4)})
	if err != nil {
		t.Fatalf("DescribeMetricFilters: %v", err)
	}

	if len(page.MetricFilters) != 4 || page.NextToken == nil {
		t.Fatalf("first page = %d filters, token %v; want 4 and a token", len(page.MetricFilters), page.NextToken)
	}

	rest, err := logs.DescribeMetricFilters(ctx, &cwl.DescribeMetricFiltersInput{Limit: aws.Int32(4), NextToken: page.NextToken})
	if err != nil || len(rest.MetricFilters) != 2 || rest.NextToken != nil {
		t.Fatalf("second page = %+v, %v", rest, err)
	}

	byMetric, err := logs.DescribeMetricFilters(ctx, &cwl.DescribeMetricFiltersInput{
		MetricName: aws.String("M1"), MetricNamespace: aws.String("NS"),
	})
	if err != nil || len(byMetric.MetricFilters) != 2 {
		t.Fatalf("metric filter by metric = %+v, %v; want one per group", byMetric, err)
	}

	byPrefix, err := logs.DescribeMetricFilters(ctx, &cwl.DescribeMetricFiltersInput{
		LogGroupName: aws.String("/dmf/a"), FilterNamePrefix: aws.String("f2"),
	})
	if err != nil || len(byPrefix.MetricFilters) != 1 || aws.ToString(byPrefix.MetricFilters[0].FilterName) != "f2" {
		t.Fatalf("by prefix = %+v, %v", byPrefix, err)
	}

	if u := byPrefix.MetricFilters[0].MetricTransformations[0].Unit; u != cwltypes.StandardUnitNone {
		t.Fatalf("unit = %q, want None when the filter omits one", u)
	}

	var invalid *cwltypes.InvalidParameterException

	if _, err := logs.DescribeMetricFilters(ctx, &cwl.DescribeMetricFiltersInput{MetricName: aws.String("M1")}); !errors.As(err, &invalid) {
		t.Fatalf("metricName without namespace: got %v, want InvalidParameterException", err)
	}

	var nf *cwltypes.ResourceNotFoundException

	if _, err := logs.DescribeMetricFilters(ctx, &cwl.DescribeMetricFiltersInput{LogGroupName: aws.String("/dmf/none")}); !errors.As(err, &nf) {
		t.Fatalf("missing group: got %v, want ResourceNotFoundException", err)
	}
}
