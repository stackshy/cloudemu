package cloudwatch_test

import (
	"sort"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// putUnit puts one datum on U/App with the given unit ("" sends none).
func putUnit(t *testing.T, w cwWire, name string, value float64, unit cwtypes.StandardUnit, ts time.Time) {
	t.Helper()

	w.put(t, &awscw.PutMetricDataInput{
		Namespace: aws.String("U/App"),
		MetricData: []cwtypes.MetricDatum{{
			MetricName: aws.String(name), Value: aws.Float64(value), Unit: unit, Timestamp: aws.Time(ts),
		}},
	})
}

func unitStats(name string, unit cwtypes.StandardUnit, now time.Time) *awscw.GetMetricStatisticsInput {
	in := statsRequest("U/App", name, nil, []cwtypes.Statistic{cwtypes.StatisticSum}, now)
	in.Unit = unit

	return in
}

// TestGetMetricStatisticsUnitFilter checks that a Unit filter only returns
// data put with that unit. AWS does no unit conversion.
func TestGetMetricStatisticsUnitFilter(t *testing.T) {
	for _, p := range cwProtocols() {
		t.Run(p.name, func(t *testing.T) {
			w := p.build(t, nil)
			now := time.Now().UTC()
			putUnit(t, w, "Cpu", 90, cwtypes.StandardUnitPercent, now)

			if out := w.getStats(t, unitStats("Cpu", cwtypes.StandardUnitBytes, now)); len(out.Datapoints) != 0 {
				t.Fatalf("Unit=Bytes on Percent data: got %d datapoints, want 0", len(out.Datapoints))
			}

			out := w.getStats(t, unitStats("Cpu", cwtypes.StandardUnitPercent, now))
			if len(out.Datapoints) != 1 || out.Datapoints[0].Unit != cwtypes.StandardUnitPercent {
				t.Fatalf("Unit=Percent: got %+v, want one Percent datapoint", out.Datapoints)
			}

			requireStat(t, "Sum", out.Datapoints[0].Sum, 90)
		})
	}
}

// TestGetMetricStatisticsUnitlessReadsNone checks that data put without a unit
// reads back as None. CloudWatch uses None when no unit is given.
func TestGetMetricStatisticsUnitlessReadsNone(t *testing.T) {
	for _, p := range cwProtocols() {
		t.Run(p.name, func(t *testing.T) {
			w := p.build(t, nil)
			now := time.Now().UTC()
			putUnit(t, w, "Raw", 1, "", now)

			out := w.getStats(t, unitStats("Raw", "", now))
			if len(out.Datapoints) != 1 || out.Datapoints[0].Unit != cwtypes.StandardUnitNone {
				t.Fatalf("got %+v, want one datapoint with Unit None", out.Datapoints)
			}

			if out := w.getStats(t, unitStats("Raw", cwtypes.StandardUnitNone, now)); len(out.Datapoints) != 1 {
				t.Fatalf("Unit=None: got %d datapoints, want 1", len(out.Datapoints))
			}
		})
	}
}

// TestGetMetricStatisticsOneDatapointPerUnit checks that data put with two
// units at the same time comes back as two datapoints when Unit is omitted.
func TestGetMetricStatisticsOneDatapointPerUnit(t *testing.T) {
	for _, p := range cwProtocols() {
		t.Run(p.name, func(t *testing.T) {
			w := p.build(t, nil)
			now := time.Now().UTC()
			putUnit(t, w, "Mixed", 1, cwtypes.StandardUnitCount, now)
			putUnit(t, w, "Mixed", 5, cwtypes.StandardUnitSeconds, now)

			out := w.getStats(t, unitStats("Mixed", "", now))
			if len(out.Datapoints) != 2 {
				t.Fatalf("got %d datapoints, want 2 (one per unit)", len(out.Datapoints))
			}

			got := map[cwtypes.StandardUnit]float64{}
			for _, dp := range out.Datapoints {
				got[dp.Unit] = aws.ToFloat64(dp.Sum)
			}

			if got[cwtypes.StandardUnitCount] != 1 || got[cwtypes.StandardUnitSeconds] != 5 {
				t.Fatalf("sums by unit = %v, want Count=1 Seconds=5", got)
			}
		})
	}
}

// TestGetMetricDataUnitFilter checks that MetricStat.Unit filters the series.
// It was decoded and then dropped.
func TestGetMetricDataUnitFilter(t *testing.T) {
	for _, p := range cwProtocols() {
		t.Run(p.name, func(t *testing.T) {
			w := p.build(t, nil)
			now := time.Now().UTC()
			putUnit(t, w, "Cpu", 90, cwtypes.StandardUnitPercent, now)

			query := func(unit cwtypes.StandardUnit) []float64 {
				out, code := w.getMetricData(t, &awscw.GetMetricDataInput{
					StartTime: aws.Time(now.Add(-time.Hour)), EndTime: aws.Time(now.Add(time.Hour)),
					MetricDataQueries: []cwtypes.MetricDataQuery{{
						Id: aws.String("m1"),
						MetricStat: &cwtypes.MetricStat{
							Metric: &cwtypes.Metric{Namespace: aws.String("U/App"), MetricName: aws.String("Cpu")},
							Period: aws.Int32(60), Stat: aws.String("Sum"), Unit: unit,
						},
					}},
				})
				if code != "" || len(out.MetricDataResults) != 1 {
					t.Fatalf("GetMetricData: code %q results %+v", code, out)
				}

				return out.MetricDataResults[0].Values
			}

			if v := query(cwtypes.StandardUnitBytes); len(v) != 0 {
				t.Fatalf("Unit=Bytes on Percent data: values %v, want none", v)
			}

			if v := query(cwtypes.StandardUnitPercent); len(v) != 1 || v[0] != 90 {
				t.Fatalf("Unit=Percent: values %v, want [90]", v)
			}
		})
	}
}

// TestPutMetricDataRejectsBadUnit checks the Unit enum. A bad datum rejects
// the whole request, so nothing is stored.
func TestPutMetricDataRejectsBadUnit(t *testing.T) {
	for _, p := range cwProtocols() {
		t.Run(p.name, func(t *testing.T) {
			w := p.build(t, nil)

			code := w.putCode(t, &awscw.PutMetricDataInput{
				Namespace: aws.String("U/App"),
				MetricData: []cwtypes.MetricDatum{
					{MetricName: aws.String("Good"), Value: aws.Float64(1)},
					{MetricName: aws.String("Bad"), Value: aws.Float64(1), Unit: "Parsecs"},
				},
			})
			if code != "InvalidParameterValue" {
				t.Fatalf("code = %q, want InvalidParameterValue", code)
			}

			if got := metricKeys(w.listMetrics(t, &awscw.ListMetricsInput{Namespace: aws.String("U/App")}).Metrics); len(got) != 0 {
				t.Fatalf("metrics stored after a rejected put: %v", got)
			}
		})
	}
}

// TestPutMetricAlarmRejectsBadUnit checks the Unit enum on PutMetricAlarm.
func TestPutMetricAlarmRejectsBadUnit(t *testing.T) {
	for _, p := range cwProtocols() {
		t.Run(p.name, func(t *testing.T) {
			w := p.build(t, nil)

			in := &awscw.PutMetricAlarmInput{
				AlarmName: aws.String("bad-unit"), Namespace: aws.String("U/App"), MetricName: aws.String("Cpu"),
				Statistic: cwtypes.StatisticAverage, Period: aws.Int32(60), EvaluationPeriods: aws.Int32(1),
				Threshold: aws.Float64(1), ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
				Unit: "Parsecs",
			}
			if code := w.putAlarmCode(t, in); code != "ValidationError" {
				t.Fatalf("code = %q, want ValidationError", code)
			}

			in.Unit = cwtypes.StandardUnitPercent
			if code := w.putAlarmCode(t, in); code != "" {
				t.Fatalf("valid unit: code = %q, want success", code)
			}
		})
	}
}

// TestAlarmWrongUnitStaysInsufficientData checks that an alarm only sees data
// put with its unit. AWS documents that a wrong unit leaves the alarm stuck
// in INSUFFICIENT_DATA.
func TestAlarmWrongUnitStaysInsufficientData(t *testing.T) {
	for _, p := range cwProtocols() {
		t.Run(p.name, func(t *testing.T) {
			w := p.build(t, nil)

			for _, unit := range []cwtypes.StandardUnit{cwtypes.StandardUnitBytes, cwtypes.StandardUnitPercent} {
				w.putAlarm(t, &awscw.PutMetricAlarmInput{
					AlarmName: aws.String("unit-" + string(unit)), Namespace: aws.String("U/App"), MetricName: aws.String("Cpu"),
					Statistic: cwtypes.StatisticAverage, Period: aws.Int32(60), EvaluationPeriods: aws.Int32(1),
					Threshold: aws.Float64(50), ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
					Unit: unit,
				})
			}

			putUnit(t, w, "Cpu", 90, cwtypes.StandardUnitPercent, time.Now().UTC())

			alarms, err := w.provider.DescribeAlarms(t.Context(), nil)
			if err != nil {
				t.Fatalf("DescribeAlarms: %v", err)
			}

			sort.Slice(alarms, func(i, j int) bool { return alarms[i].Name < alarms[j].Name })

			if len(alarms) != 2 || alarms[0].State != "INSUFFICIENT_DATA" || alarms[1].State != "ALARM" {
				t.Fatalf("states = %+v, want unit-Bytes INSUFFICIENT_DATA and unit-Percent ALARM", alarms)
			}
		})
	}
}
