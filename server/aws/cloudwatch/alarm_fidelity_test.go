package cloudwatch_test

import (
	"context"
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

// newCWClientClock wires the real aws-sdk-go-v2 CloudWatch client at a handler
// backed by a provider using the given clock, so alarm evaluation (which reads
// the clock) is deterministic across per-period bucketing tests.
func newCWClientClock(t *testing.T, clock config.Clock) (*awscw.Client, context.Context) {
	t.Helper()

	h := cwserver.New(cwprovider.New(config.NewOptions(config.WithClock(clock))))
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

// TestSDKExactDimensionMatching covers F1: a metric published with a dimension
// is a distinct series from the no-dimension metric. A no-dimension query must
// exclude it; the exact-dimension query must return it.
func TestSDKExactDimensionMatching(t *testing.T) {
	client, ctx := newCWClient(t)

	now := time.Now().UTC()
	if _, err := client.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("Custom/App"),
		MetricData: []cwtypes.MetricDatum{{
			MetricName: aws.String("Latency"),
			Value:      aws.Float64(42),
			Timestamp:  aws.Time(now),
			Dimensions: []cwtypes.Dimension{{Name: aws.String("InstanceId"), Value: aws.String("i-1")}},
		}},
	}); err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}

	stat := func(dims []cwtypes.Dimension) *awscw.GetMetricStatisticsOutput {
		t.Helper()

		out, err := client.GetMetricStatistics(ctx, &awscw.GetMetricStatisticsInput{
			Namespace:  aws.String("Custom/App"),
			MetricName: aws.String("Latency"),
			StartTime:  aws.Time(now.Add(-time.Hour)),
			EndTime:    aws.Time(now.Add(time.Hour)),
			Period:     aws.Int32(60),
			Statistics: []cwtypes.Statistic{cwtypes.StatisticSum},
			Dimensions: dims,
		})
		if err != nil {
			t.Fatalf("GetMetricStatistics: %v", err)
		}

		return out
	}

	if got := stat(nil); len(got.Datapoints) != 0 {
		t.Fatalf("no-dimension query returned %d datapoints, want 0 (dimensioned metric is a separate series)", len(got.Datapoints))
	}

	exact := stat([]cwtypes.Dimension{{Name: aws.String("InstanceId"), Value: aws.String("i-1")}})
	if len(exact.Datapoints) != 1 {
		t.Fatalf("exact-dimension query returned %d datapoints, want 1", len(exact.Datapoints))
	}

	if got := aws.ToFloat64(exact.Datapoints[0].Sum); got != 42 {
		t.Fatalf("Sum = %v, want 42", got)
	}
}

// TestSDKAlarmMOfNAndRecovery covers F3: an alarm with EvaluationPeriods=3 and
// DatapointsToAlarm=3 must require all three periods to breach (not the window
// average), and must recover to OK once the breaching periods leave the window.
func TestSDKAlarmMOfNAndRecovery(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC))
	client, ctx := newCWClientClock(t, fc)

	if _, err := client.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
		AlarmName:          aws.String("m-of-n"),
		Namespace:          aws.String("Custom/App"),
		MetricName:         aws.String("Load"),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		Threshold:          aws.Float64(10),
		EvaluationPeriods:  aws.Int32(3),
		DatapointsToAlarm:  aws.Int32(3),
		Period:             aws.Int32(60),
		Statistic:          cwtypes.StatisticAverage,
	}); err != nil {
		t.Fatalf("PutMetricAlarm: %v", err)
	}

	// feed publishes one datum per period (oldest..newest) at the clock's now.
	feed := func(vals ...float64) {
		t.Helper()

		data := make([]cwtypes.MetricDatum, 0, len(vals))
		for i, v := range vals {
			age := time.Duration(len(vals)-1-i) * 60 * time.Second
			data = append(data, cwtypes.MetricDatum{
				MetricName: aws.String("Load"),
				Value:      aws.Float64(v),
				Timestamp:  aws.Time(fc.Now().Add(-age)),
			})
		}

		if _, err := client.PutMetricData(ctx, &awscw.PutMetricDataInput{
			Namespace:  aws.String("Custom/App"),
			MetricData: data,
		}); err != nil {
			t.Fatalf("PutMetricData: %v", err)
		}
	}

	state := func() cwtypes.StateValue {
		t.Helper()

		out, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmNames: []string{"m-of-n"}})
		if err != nil {
			t.Fatalf("DescribeAlarms: %v", err)
		}
		if len(out.MetricAlarms) != 1 {
			t.Fatalf("DescribeAlarms returned %d alarms, want 1", len(out.MetricAlarms))
		}

		return out.MetricAlarms[0].StateValue
	}

	// Only the most recent of three periods breaches: 1 of 3 < DatapointsToAlarm=3,
	// so the alarm stays OK (the old window-average bug went to ALARM on 33.3 avg).
	feed(0, 0, 100)
	if got := state(); got != cwtypes.StateValueOk {
		t.Fatalf("state after 0,0,100 = %s, want OK (only 1 of 3 periods breaches)", got)
	}

	// All three periods breach -> ALARM. Advance so the prior window ages out.
	fc.Advance(time.Hour)
	feed(100, 100, 100)
	if got := state(); got != cwtypes.StateValueAlarm {
		t.Fatalf("state after 100,100,100 = %s, want ALARM", got)
	}

	// Fresh non-breaching periods -> recovery to OK.
	fc.Advance(time.Hour)
	feed(0, 0, 0)
	if got := state(); got != cwtypes.StateValueOk {
		t.Fatalf("state after 0,0,0 = %s, want OK (recovery)", got)
	}
}

// TestSDKPutMetricAlarmFieldRoundTrip covers F4: DatapointsToAlarm,
// TreatMissingData, Unit and ExtendedStatistic must survive PutMetricAlarm and
// be reflected on DescribeAlarms (else Terraform sees perpetual drift).
func TestSDKPutMetricAlarmFieldRoundTrip(t *testing.T) {
	client, ctx := newCWClient(t)

	if _, err := client.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
		AlarmName:          aws.String("fields"),
		Namespace:          aws.String("Custom/App"),
		MetricName:         aws.String("Errors"),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		Threshold:          aws.Float64(1),
		EvaluationPeriods:  aws.Int32(3),
		DatapointsToAlarm:  aws.Int32(2),
		Period:             aws.Int32(60),
		Statistic:          cwtypes.StatisticSum,
		Unit:               cwtypes.StandardUnitCount,
		TreatMissingData:   aws.String("notBreaching"),
	}); err != nil {
		t.Fatalf("PutMetricAlarm: %v", err)
	}

	// ExtendedStatistic is mutually exclusive with Statistic, so use a second alarm.
	if _, err := client.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
		AlarmName:          aws.String("pctile"),
		Namespace:          aws.String("Custom/App"),
		MetricName:         aws.String("Latency"),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		Threshold:          aws.Float64(100),
		EvaluationPeriods:  aws.Int32(1),
		Period:             aws.Int32(60),
		ExtendedStatistic:  aws.String("p95"),
	}); err != nil {
		t.Fatalf("PutMetricAlarm(pctile): %v", err)
	}

	out, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmNames: []string{"fields", "pctile"}})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}

	byName := map[string]cwtypes.MetricAlarm{}
	for _, a := range out.MetricAlarms {
		byName[aws.ToString(a.AlarmName)] = a
	}

	fields, ok := byName["fields"]
	if !ok {
		t.Fatal("alarm 'fields' not returned")
	}
	if got := aws.ToInt32(fields.DatapointsToAlarm); got != 2 {
		t.Errorf("DatapointsToAlarm = %d, want 2", got)
	}
	if got := aws.ToString(fields.TreatMissingData); got != "notBreaching" {
		t.Errorf("TreatMissingData = %q, want notBreaching", got)
	}
	if fields.Unit != cwtypes.StandardUnitCount {
		t.Errorf("Unit = %q, want Count", fields.Unit)
	}

	pctile, ok := byName["pctile"]
	if !ok {
		t.Fatal("alarm 'pctile' not returned")
	}
	if got := aws.ToString(pctile.ExtendedStatistic); got != "p95" {
		t.Errorf("ExtendedStatistic = %q, want p95", got)
	}
}

// TestSDKDeleteAlarmsToleratesUnknown covers F5: DeleteAlarms deletes the valid
// names and ignores unknown ones with no error and no half-deleted state.
func TestSDKDeleteAlarmsToleratesUnknown(t *testing.T) {
	client, ctx := newCWClient(t)

	for _, name := range []string{"keep-1", "keep-2"} {
		if _, err := client.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
			AlarmName:          aws.String(name),
			Namespace:          aws.String("Custom/App"),
			MetricName:         aws.String("Errors"),
			ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
			Threshold:          aws.Float64(1),
			EvaluationPeriods:  aws.Int32(1),
			Period:             aws.Int32(60),
			Statistic:          cwtypes.StatisticSum,
		}); err != nil {
			t.Fatalf("PutMetricAlarm(%s): %v", name, err)
		}
	}

	// A batch mixing valid and unknown names must succeed (AWS tolerates the
	// unknown one) and delete both valid alarms regardless of their position.
	if _, err := client.DeleteAlarms(ctx, &awscw.DeleteAlarmsInput{
		AlarmNames: []string{"keep-1", "ghost", "keep-2"},
	}); err != nil {
		t.Fatalf("DeleteAlarms with an unknown name should succeed, got: %v", err)
	}

	out, err := client.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{
		AlarmNames: []string{"keep-1", "keep-2"},
	})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}
	if len(out.MetricAlarms) != 0 {
		t.Fatalf("%d alarms survived, want 0 (both valid names deleted)", len(out.MetricAlarms))
	}
}

// TestSDKSetAlarmStateRecordsHistory covers F2 at the wire layer: forcing a state
// with SetAlarmState records a StateUpdate history entry (no metric needed).
func TestSDKSetAlarmStateRecordsHistory(t *testing.T) {
	client, ctx := newCWClient(t)

	if _, err := client.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
		AlarmName:          aws.String("forced"),
		Namespace:          aws.String("Custom/App"),
		MetricName:         aws.String("Errors"),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		Threshold:          aws.Float64(1),
		EvaluationPeriods:  aws.Int32(1),
		Period:             aws.Int32(60),
		Statistic:          cwtypes.StatisticSum,
	}); err != nil {
		t.Fatalf("PutMetricAlarm: %v", err)
	}

	if _, err := client.SetAlarmState(ctx, &awscw.SetAlarmStateInput{
		AlarmName:   aws.String("forced"),
		StateValue:  cwtypes.StateValueAlarm,
		StateReason: aws.String("manual"),
	}); err != nil {
		t.Fatalf("SetAlarmState: %v", err)
	}

	out, err := client.DescribeAlarmHistory(ctx, &awscw.DescribeAlarmHistoryInput{
		AlarmName: aws.String("forced"),
	})
	if err != nil {
		t.Fatalf("DescribeAlarmHistory: %v", err)
	}
	if len(out.AlarmHistoryItems) == 0 {
		t.Fatal("SetAlarmState recorded no history entry")
	}
	if out.AlarmHistoryItems[0].HistoryItemType != cwtypes.HistoryItemTypeStateUpdate {
		t.Fatalf("HistoryItemType = %q, want StateUpdate", out.AlarmHistoryItems[0].HistoryItemType)
	}
}

// TestSDKDescribeAlarmHistoryOrderingAndFilters covers F6: history is newest-first
// by default, MaxRecords keeps the newest, ScanBy reverses to oldest-first, and
// HistoryItemType filters.
func TestSDKDescribeAlarmHistoryOrderingAndFilters(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC))
	client, ctx := newCWClientClock(t, fc)

	if _, err := client.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
		AlarmName:          aws.String("hist"),
		Namespace:          aws.String("Custom/App"),
		MetricName:         aws.String("Errors"),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		Threshold:          aws.Float64(1),
		EvaluationPeriods:  aws.Int32(1),
		Period:             aws.Int32(60),
		Statistic:          cwtypes.StatisticSum,
	}); err != nil {
		t.Fatalf("PutMetricAlarm: %v", err)
	}

	// Three distinct-timestamp transitions: INSUFFICIENT_DATA->ALARM->OK->ALARM.
	for _, sv := range []cwtypes.StateValue{cwtypes.StateValueAlarm, cwtypes.StateValueOk, cwtypes.StateValueAlarm} {
		if _, err := client.SetAlarmState(ctx, &awscw.SetAlarmStateInput{
			AlarmName:   aws.String("hist"),
			StateValue:  sv,
			StateReason: aws.String("step"),
		}); err != nil {
			t.Fatalf("SetAlarmState(%s): %v", sv, err)
		}

		fc.Advance(time.Minute)
	}

	desc, err := client.DescribeAlarmHistory(ctx, &awscw.DescribeAlarmHistoryInput{AlarmName: aws.String("hist")})
	if err != nil {
		t.Fatalf("DescribeAlarmHistory: %v", err)
	}
	if len(desc.AlarmHistoryItems) != 3 {
		t.Fatalf("history len = %d, want 3", len(desc.AlarmHistoryItems))
	}

	// Default order is newest-first: strictly descending timestamps.
	for i := 1; i < len(desc.AlarmHistoryItems); i++ {
		if desc.AlarmHistoryItems[i-1].Timestamp.Before(*desc.AlarmHistoryItems[i].Timestamp) {
			t.Fatalf("history not newest-first at %d: %v then %v",
				i, desc.AlarmHistoryItems[i-1].Timestamp, desc.AlarmHistoryItems[i].Timestamp)
		}
	}

	newest := *desc.AlarmHistoryItems[0].Timestamp

	// MaxRecords keeps the newest N (here the single newest entry).
	limited, err := client.DescribeAlarmHistory(ctx, &awscw.DescribeAlarmHistoryInput{
		AlarmName:  aws.String("hist"),
		MaxRecords: aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("DescribeAlarmHistory(limit): %v", err)
	}
	if len(limited.AlarmHistoryItems) != 1 {
		t.Fatalf("limited history len = %d, want 1", len(limited.AlarmHistoryItems))
	}
	if !limited.AlarmHistoryItems[0].Timestamp.Equal(newest) {
		t.Fatalf("MaxRecords=1 kept %v, want the newest %v", *limited.AlarmHistoryItems[0].Timestamp, newest)
	}

	// ScanBy ascending flips to oldest-first.
	asc, err := client.DescribeAlarmHistory(ctx, &awscw.DescribeAlarmHistoryInput{
		AlarmName: aws.String("hist"),
		ScanBy:    cwtypes.ScanByTimestampAscending,
	})
	if err != nil {
		t.Fatalf("DescribeAlarmHistory(asc): %v", err)
	}
	if got := asc.AlarmHistoryItems[len(asc.AlarmHistoryItems)-1].Timestamp; !got.Equal(newest) {
		t.Fatalf("ascending last = %v, want newest %v", *got, newest)
	}

	// HistoryItemType filter: all entries are StateUpdate.
	su, err := client.DescribeAlarmHistory(ctx, &awscw.DescribeAlarmHistoryInput{
		AlarmName:       aws.String("hist"),
		HistoryItemType: cwtypes.HistoryItemTypeStateUpdate,
	})
	if err != nil {
		t.Fatalf("DescribeAlarmHistory(StateUpdate): %v", err)
	}
	if len(su.AlarmHistoryItems) != 3 {
		t.Fatalf("StateUpdate filter len = %d, want 3", len(su.AlarmHistoryItems))
	}

	cu, err := client.DescribeAlarmHistory(ctx, &awscw.DescribeAlarmHistoryInput{
		AlarmName:       aws.String("hist"),
		HistoryItemType: cwtypes.HistoryItemTypeConfigurationUpdate,
	})
	if err != nil {
		t.Fatalf("DescribeAlarmHistory(ConfigurationUpdate): %v", err)
	}
	if len(cu.AlarmHistoryItems) != 0 {
		t.Fatalf("ConfigurationUpdate filter len = %d, want 0", len(cu.AlarmHistoryItems))
	}
}
