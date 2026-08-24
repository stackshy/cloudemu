package cloudwatch_test

import (
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	smithy "github.com/aws/smithy-go"
)

// TestSDKGetMetricStatisticsMultipleStatistics covers the multi-statistic
// finding: when several statistics are requested, every returned datapoint must
// populate all of them simultaneously, not just the first.
func TestSDKGetMetricStatisticsMultipleStatistics(t *testing.T) {
	client, ctx := newCWClient(t)

	now := time.Now().UTC()
	for _, v := range []float64{10, 20, 30} {
		if _, err := client.PutMetricData(ctx, &awscw.PutMetricDataInput{
			Namespace: aws.String("MyApp"),
			MetricData: []cwtypes.MetricDatum{{
				MetricName: aws.String("Latency"),
				Value:      aws.Float64(v),
				Timestamp:  aws.Time(now),
			}},
		}); err != nil {
			t.Fatalf("PutMetricData: %v", err)
		}
	}

	out, err := client.GetMetricStatistics(ctx, &awscw.GetMetricStatisticsInput{
		Namespace:  aws.String("MyApp"),
		MetricName: aws.String("Latency"),
		StartTime:  aws.Time(now.Add(-time.Hour)),
		EndTime:    aws.Time(now.Add(time.Hour)),
		Period:     aws.Int32(3600),
		Statistics: []cwtypes.Statistic{
			cwtypes.StatisticAverage,
			cwtypes.StatisticSum,
			cwtypes.StatisticMaximum,
			cwtypes.StatisticMinimum,
			cwtypes.StatisticSampleCount,
		},
	})
	if err != nil {
		t.Fatalf("GetMetricStatistics: %v", err)
	}

	if len(out.Datapoints) != 1 {
		t.Fatalf("datapoints = %+v, want exactly 1", out.Datapoints)
	}

	dp := out.Datapoints[0]
	assertStat(t, "Average", dp.Average, 20)
	assertStat(t, "Sum", dp.Sum, 60)
	assertStat(t, "Maximum", dp.Maximum, 30)
	assertStat(t, "Minimum", dp.Minimum, 10)
	assertStat(t, "SampleCount", dp.SampleCount, 3)
}

// TestSDKPutMetricDataStatisticValues covers the pre-aggregated finding: a datum
// carrying StatisticValues (SampleCount/Sum/Min/Max) instead of Value must be
// aggregated into the series so GetMetricStatistics reflects the supplied Sum.
func TestSDKPutMetricDataStatisticValues(t *testing.T) {
	client, ctx := newCWClient(t)

	now := time.Now().UTC()
	if _, err := client.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("MyApp"),
		MetricData: []cwtypes.MetricDatum{{
			MetricName: aws.String("Batch"),
			Timestamp:  aws.Time(now),
			StatisticValues: &cwtypes.StatisticSet{
				SampleCount: aws.Float64(4),
				Sum:         aws.Float64(100),
				Minimum:     aws.Float64(10),
				Maximum:     aws.Float64(40),
			},
		}},
	}); err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}

	out, err := client.GetMetricStatistics(ctx, &awscw.GetMetricStatisticsInput{
		Namespace:  aws.String("MyApp"),
		MetricName: aws.String("Batch"),
		StartTime:  aws.Time(now.Add(-time.Hour)),
		EndTime:    aws.Time(now.Add(time.Hour)),
		Period:     aws.Int32(3600),
		Statistics: []cwtypes.Statistic{
			cwtypes.StatisticSum,
			cwtypes.StatisticAverage,
			cwtypes.StatisticMinimum,
			cwtypes.StatisticMaximum,
			cwtypes.StatisticSampleCount,
		},
	})
	if err != nil {
		t.Fatalf("GetMetricStatistics: %v", err)
	}

	if len(out.Datapoints) != 1 {
		t.Fatalf("datapoints = %+v, want exactly 1", out.Datapoints)
	}

	dp := out.Datapoints[0]
	assertStat(t, "Sum", dp.Sum, 100)
	assertStat(t, "Average", dp.Average, 25)
	assertStat(t, "Minimum", dp.Minimum, 10)
	assertStat(t, "Maximum", dp.Maximum, 40)
	assertStat(t, "SampleCount", dp.SampleCount, 4)
}

// TestSDKPutMetricDataValuesCounts covers the Values/Counts finding: each
// Values[i] is observed Counts[i] times and must weight the aggregation.
func TestSDKPutMetricDataValuesCounts(t *testing.T) {
	client, ctx := newCWClient(t)

	now := time.Now().UTC()
	if _, err := client.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("MyApp"),
		MetricData: []cwtypes.MetricDatum{{
			MetricName: aws.String("Weighted"),
			Timestamp:  aws.Time(now),
			Values:     []float64{1, 2, 3},
			Counts:     []float64{4, 1, 1},
		}},
	}); err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}

	out, err := client.GetMetricStatistics(ctx, &awscw.GetMetricStatisticsInput{
		Namespace:  aws.String("MyApp"),
		MetricName: aws.String("Weighted"),
		StartTime:  aws.Time(now.Add(-time.Hour)),
		EndTime:    aws.Time(now.Add(time.Hour)),
		Period:     aws.Int32(3600),
		Statistics: []cwtypes.Statistic{
			cwtypes.StatisticSum,
			cwtypes.StatisticSampleCount,
			cwtypes.StatisticAverage,
			cwtypes.StatisticMaximum,
			cwtypes.StatisticMinimum,
		},
	})
	if err != nil {
		t.Fatalf("GetMetricStatistics: %v", err)
	}

	if len(out.Datapoints) != 1 {
		t.Fatalf("datapoints = %+v, want exactly 1", out.Datapoints)
	}

	dp := out.Datapoints[0]
	assertStat(t, "Sum", dp.Sum, 9)         // 1*4 + 2*1 + 3*1
	assertStat(t, "SampleCount", dp.SampleCount, 6)
	assertStat(t, "Average", dp.Average, 1.5) // 9 / 6
	assertStat(t, "Maximum", dp.Maximum, 3)
	assertStat(t, "Minimum", dp.Minimum, 1)
}

// TestSDKPutMetricAlarmInvalidComparisonOperator covers the enum-validation
// finding: an unrecognized ComparisonOperator must be rejected with a
// ValidationError, not silently stored as a never-firing alarm.
func TestSDKPutMetricAlarmInvalidComparisonOperator(t *testing.T) {
	client, ctx := newCWClient(t)

	_, err := client.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
		AlarmName:          aws.String("bogus"),
		Namespace:          aws.String("MyApp"),
		MetricName:         aws.String("Errors"),
		ComparisonOperator: cwtypes.ComparisonOperator("NotAnOperator"),
		Threshold:          aws.Float64(1),
		EvaluationPeriods:  aws.Int32(1),
		Period:             aws.Int32(60),
		Statistic:          cwtypes.StatisticAverage,
	})
	if err == nil {
		t.Fatalf("PutMetricAlarm with invalid ComparisonOperator: got nil error, want ValidationError")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationError" {
		t.Fatalf("PutMetricAlarm error = %v, want ValidationError", err)
	}

	// A valid operator must still be accepted.
	if _, err := client.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
		AlarmName:          aws.String("ok"),
		Namespace:          aws.String("MyApp"),
		MetricName:         aws.String("Errors"),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		Threshold:          aws.Float64(1),
		EvaluationPeriods:  aws.Int32(1),
		Period:             aws.Int32(60),
		Statistic:          cwtypes.StatisticAverage,
	}); err != nil {
		t.Fatalf("PutMetricAlarm valid operator: %v", err)
	}
}

func assertStat(t *testing.T, name string, got *float64, want float64) {
	t.Helper()

	if got == nil {
		t.Fatalf("%s statistic = nil, want %v", name, want)
	}

	if *got != want {
		t.Fatalf("%s statistic = %v, want %v", name, *got, want)
	}
}
