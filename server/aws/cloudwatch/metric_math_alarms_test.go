package cloudwatch_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// describeAlarmsXML decodes the query-protocol MetricAlarm fields these
// tests read, including the Metrics list.
type describeAlarmsXML struct {
	Alarms []struct {
		AlarmName         string   `xml:"AlarmName"`
		MetricName        string   `xml:"MetricName"`
		StateValue        string   `xml:"StateValue"`
		Period            int32    `xml:"Period"`
		ThresholdMetricID string   `xml:"ThresholdMetricId"`
		Threshold         *float64 `xml:"Threshold"`
		Metrics           []struct {
			ID         string `xml:"Id"`
			Expression string `xml:"Expression"`
			Label      string `xml:"Label"`
			ReturnData bool   `xml:"ReturnData"`
			Period     int32  `xml:"Period"`
			MetricStat *struct {
				Namespace  string `xml:"Metric>Namespace"`
				MetricName string `xml:"Metric>MetricName"`
				Period     int32  `xml:"Period"`
				Stat       string `xml:"Stat"`
			} `xml:"MetricStat"`
		} `xml:"Metrics>member"`
	} `xml:"DescribeAlarmsResult>MetricAlarms>member"`
}

func optString(s string) *string {
	if s == "" {
		return nil
	}

	return aws.String(s)
}

func optInt32(v int32) *int32 {
	if v == 0 {
		return nil
	}

	return aws.Int32(v)
}

func (x describeAlarmsXML) toSDK() []cwtypes.MetricAlarm {
	out := make([]cwtypes.MetricAlarm, 0, len(x.Alarms))

	for _, a := range x.Alarms {
		ma := cwtypes.MetricAlarm{
			AlarmName: aws.String(a.AlarmName), MetricName: optString(a.MetricName),
			StateValue: cwtypes.StateValue(a.StateValue), Period: optInt32(a.Period),
			ThresholdMetricId: optString(a.ThresholdMetricID), Threshold: a.Threshold,
		}

		for _, q := range a.Metrics {
			mq := cwtypes.MetricDataQuery{
				Id: aws.String(q.ID), Expression: optString(q.Expression), Label: optString(q.Label),
				ReturnData: aws.Bool(q.ReturnData), Period: optInt32(q.Period),
			}

			if ms := q.MetricStat; ms != nil {
				mq.MetricStat = &cwtypes.MetricStat{
					Metric: &cwtypes.Metric{Namespace: aws.String(ms.Namespace), MetricName: aws.String(ms.MetricName)},
					Period: aws.Int32(ms.Period), Stat: aws.String(ms.Stat),
				}
			}

			ma.Metrics = append(ma.Metrics, mq)
		}

		out = append(out, ma)
	}

	return out
}

const mathNS = "M/App"

func mathStat(id, name string) cwtypes.MetricDataQuery {
	return cwtypes.MetricDataQuery{
		Id: aws.String(id), ReturnData: aws.Bool(false),
		MetricStat: &cwtypes.MetricStat{
			Metric: &cwtypes.Metric{Namespace: aws.String(mathNS), MetricName: aws.String(name)},
			Period: aws.Int32(60), Stat: aws.String("Sum"),
		},
	}
}

// rateAlarmInput is err/req*100 > 20, as in the CW-12a plan.
func rateAlarmInput(name string) *awscw.PutMetricAlarmInput {
	return &awscw.PutMetricAlarmInput{
		AlarmName: aws.String(name), EvaluationPeriods: aws.Int32(1), Threshold: aws.Float64(20),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		Metrics: []cwtypes.MetricDataQuery{
			mathStat("err", "Errors"),
			mathStat("req", "Requests"),
			{Id: aws.String("rate"), Expression: aws.String("err/req*100"), Label: aws.String("ErrorRate"), ReturnData: aws.Bool(true)},
		},
	}
}

func putRateData(t *testing.T, w cwWire, errors, requests float64) {
	t.Helper()

	now := time.Now().UTC()
	w.put(t, &awscw.PutMetricDataInput{Namespace: aws.String(mathNS), MetricData: []cwtypes.MetricDatum{
		{MetricName: aws.String("Errors"), Value: aws.Float64(errors), Timestamp: aws.Time(now)},
		{MetricName: aws.String("Requests"), Value: aws.Float64(requests), Timestamp: aws.Time(now)},
	}})
}

// A metric-math alarm is accepted on both protocols, evaluates its expression
// and reads back its Metrics list. Before CW-12a the list was dropped, the
// alarm never left INSUFFICIENT_DATA, and Terraform metric_query drifted.
func TestMathAlarmRoundTrip(t *testing.T) {
	for _, proto := range cwProtocols() {
		t.Run(proto.name, func(t *testing.T) {
			w := proto.build(t, nil)
			putRateData(t, w, 30, 100)

			if code := w.putAlarmCode(t, rateAlarmInput("rate")); code != "" {
				t.Fatalf("PutMetricAlarm: %s", code)
			}

			alarms := w.describeAlarms(t, "rate")
			if len(alarms) != 1 {
				t.Fatalf("alarms = %d, want 1", len(alarms))
			}

			a := alarms[0]
			if a.StateValue != cwtypes.StateValueAlarm {
				t.Fatalf("state = %s, want ALARM (30/100*100 > 20)", a.StateValue)
			}

			if a.Period != nil || a.MetricName != nil {
				t.Fatalf("math alarm reports Period %v MetricName %v, want neither", a.Period, a.MetricName)
			}

			if len(a.Metrics) != 3 {
				t.Fatalf("Metrics = %d entries, want 3", len(a.Metrics))
			}

			rate := a.Metrics[2]
			if aws.ToString(rate.Expression) != "err/req*100" || aws.ToString(rate.Label) != "ErrorRate" || !aws.ToBool(rate.ReturnData) {
				t.Fatalf("rate entry = %+v", rate)
			}

			errQ := a.Metrics[0]
			if aws.ToBool(errQ.ReturnData) || errQ.MetricStat == nil ||
				aws.ToString(errQ.MetricStat.Metric.MetricName) != "Errors" ||
				aws.ToInt32(errQ.MetricStat.Period) != 60 || aws.ToString(errQ.MetricStat.Stat) != "Sum" {
				t.Fatalf("err entry = %+v", errQ)
			}

			// DescribeAlarmsForMetric leaves math alarms out.
			out, code := w.alarmsForMetric(t, &awscw.DescribeAlarmsForMetricInput{
				Namespace: aws.String(mathNS), MetricName: aws.String("Errors"),
			})
			if code != "" || len(out.MetricAlarms) != 0 {
				t.Fatalf("DescribeAlarmsForMetric = %v %s, want none", out.MetricAlarms, code)
			}
		})
	}
}

// An anomaly detection alarm returns data from both the metric and the band
// and names the band with ThresholdMetricId. It is stored and read back.
func TestMathAlarmThresholdMetricID(t *testing.T) {
	for _, proto := range cwProtocols() {
		t.Run(proto.name, func(t *testing.T) {
			w := proto.build(t, nil)

			m1 := mathStat("m1", "Errors")
			m1.ReturnData = aws.Bool(true)

			in := &awscw.PutMetricAlarmInput{
				AlarmName: aws.String("band"), EvaluationPeriods: aws.Int32(1),
				ComparisonOperator: cwtypes.ComparisonOperatorLessThanLowerOrGreaterThanUpperThreshold,
				ThresholdMetricId:  aws.String("ad1"),
				Metrics: []cwtypes.MetricDataQuery{
					m1,
					{Id: aws.String("ad1"), Expression: aws.String("ANOMALY_DETECTION_BAND(m1, 2)"), ReturnData: aws.Bool(true)},
				},
			}

			if code := w.putAlarmCode(t, in); code != "" {
				t.Fatalf("PutMetricAlarm: %s", code)
			}

			a := w.describeAlarms(t, "band")[0]
			if aws.ToString(a.ThresholdMetricId) != "ad1" || len(a.Metrics) != 2 {
				t.Fatalf("ThresholdMetricId = %v, Metrics = %d", a.ThresholdMetricId, len(a.Metrics))
			}
		})
	}
}

func TestMathAlarmValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(in *awscw.PutMetricAlarmInput)
	}{
		{"MetricName with Metrics", func(in *awscw.PutMetricAlarmInput) {
			in.MetricName, in.Namespace = aws.String("Errors"), aws.String(mathNS)
		}},
		{"Period with Metrics", func(in *awscw.PutMetricAlarmInput) { in.Period = aws.Int32(60) }},
		{"two ReturnData", func(in *awscw.PutMetricAlarmInput) { in.Metrics[0].ReturnData = aws.Bool(true) }},
		{"no ReturnData", func(in *awscw.PutMetricAlarmInput) { in.Metrics[2].ReturnData = aws.Bool(false) }},
		{"uppercase id", func(in *awscw.PutMetricAlarmInput) { in.Metrics[2].Id = aws.String("Rate") }},
		{"duplicate id", func(in *awscw.PutMetricAlarmInput) { in.Metrics[1].Id = aws.String("err") }},
		{"stat and expression", func(in *awscw.PutMetricAlarmInput) { in.Metrics[0].Expression = aws.String("1") }},
		{"unknown reference", func(in *awscw.PutMetricAlarmInput) { in.Metrics[2].Expression = aws.String("err/nope") }},
		{"cycle", func(in *awscw.PutMetricAlarmInput) {
			in.Metrics = append(in.Metrics, cwtypes.MetricDataQuery{
				Id: aws.String("loop"), Expression: aws.String("rate+1"), ReturnData: aws.Bool(false),
			})
			in.Metrics[2].Expression = aws.String("loop*2")
		}},
		{"MetricStat Period 7", func(in *awscw.PutMetricAlarmInput) { in.Metrics[0].MetricStat.Period = aws.Int32(7) }},
		{"MetricStat Period 90", func(in *awscw.PutMetricAlarmInput) { in.Metrics[0].MetricStat.Period = aws.Int32(90) }},
		{"MetricStat Period 0", func(in *awscw.PutMetricAlarmInput) { in.Metrics[0].MetricStat.Period = aws.Int32(0) }},
		{"MetricStat empty Stat", func(in *awscw.PutMetricAlarmInput) { in.Metrics[0].MetricStat.Stat = aws.String("") }},
		{"expression Period 45", func(in *awscw.PutMetricAlarmInput) { in.Metrics[2].Period = aws.Int32(45) }},
		{"11 MetricStat entries", func(in *awscw.PutMetricAlarmInput) {
			for i := range 9 {
				in.Metrics = append(in.Metrics, mathStat("x"+strconv.Itoa(i), "Errors"))
			}
		}},
		{"11 Expression entries", func(in *awscw.PutMetricAlarmInput) {
			for i := range 10 {
				in.Metrics = append(in.Metrics, cwtypes.MetricDataQuery{
					Id: aws.String("x" + strconv.Itoa(i)), Expression: aws.String("err*2"), ReturnData: aws.Bool(false),
				})
			}
		}},
		{"neither MetricName nor Metrics", func(in *awscw.PutMetricAlarmInput) { in.Metrics = nil }},
		{"unknown ThresholdMetricId", func(in *awscw.PutMetricAlarmInput) { in.ThresholdMetricId = aws.String("ad9") }},
		{"ThresholdMetricId without Metrics", func(in *awscw.PutMetricAlarmInput) {
			in.Metrics = nil
			in.MetricName, in.Namespace, in.Period = aws.String("Errors"), aws.String(mathNS), aws.Int32(60)
			in.Statistic = cwtypes.StatisticSum
			in.ThresholdMetricId = aws.String("ad1")
		}},
	}

	for _, proto := range cwProtocols() {
		t.Run(proto.name, func(t *testing.T) {
			w := proto.build(t, nil)

			for _, tc := range tests {
				in := rateAlarmInput("bad")
				tc.mutate(in)

				if code := w.putAlarmCode(t, in); code != "ValidationError" {
					t.Errorf("%s: code = %q, want ValidationError", tc.name, code)
				}
			}

			if got := w.describeAlarms(t, "bad"); len(got) != 0 {
				t.Fatalf("a rejected alarm was stored: %+v", got)
			}
		})
	}
}

// TestExpressionPeriod closes tracker row CW-X1: the top-level Period of an
// Expression query sets the granularity of its points. It was ignored, so the
// expression came back at its input's 60s period.
func TestExpressionPeriod(t *testing.T) {
	for _, proto := range cwProtocols() {
		t.Run(proto.name, func(t *testing.T) {
			w := proto.build(t, nil)

			base := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
			data := make([]cwtypes.MetricDatum, 0, 4)

			for i := range 4 {
				data = append(data, cwtypes.MetricDatum{
					MetricName: aws.String("Errors"), Value: aws.Float64(float64(i + 1)),
					Timestamp: aws.Time(base.Add(time.Duration(i) * time.Minute)),
				})
			}

			w.put(t, &awscw.PutMetricDataInput{Namespace: aws.String(mathNS), MetricData: data})

			out, code := w.getMetricData(t, &awscw.GetMetricDataInput{
				StartTime: aws.Time(base), EndTime: aws.Time(base.Add(10 * time.Minute)),
				ScanBy: cwtypes.ScanByTimestampAscending,
				MetricDataQueries: []cwtypes.MetricDataQuery{
					mathStat("m1", "Errors"),
					{Id: aws.String("e1"), Expression: aws.String("m1*1"), Period: aws.Int32(120)},
				},
			})
			if code != "" {
				t.Fatalf("GetMetricData: %s", code)
			}

			if len(out.MetricDataResults) != 1 {
				t.Fatalf("results = %d, want 1", len(out.MetricDataResults))
			}

			got := out.MetricDataResults[0].Values
			if !floatsEqual(got, []float64{3, 7}) {
				t.Fatalf("e1 values = %v, want [3 7] (two 120s sums)", got)
			}
		})
	}
}
