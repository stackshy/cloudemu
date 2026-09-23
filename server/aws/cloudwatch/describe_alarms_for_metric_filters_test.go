package cloudwatch_test

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

func seedMetricAlarms(t *testing.T, w cwWire) {
	t.Helper()

	base := awscw.PutMetricAlarmInput{
		Namespace: aws.String("T/App"), MetricName: aws.String("A"), Dimensions: dims("Env", "prod", "Svc", "x"),
		Period: aws.Int32(60), EvaluationPeriods: aws.Int32(1), Threshold: aws.Float64(5),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
	}

	avg := base
	avg.AlarmName, avg.Statistic, avg.Unit = aws.String("avg"), cwtypes.StatisticAverage, cwtypes.StandardUnitCount
	w.putAlarm(t, &avg)

	p90 := base
	p90.AlarmName, p90.ExtendedStatistic, p90.Unit = aws.String("p90"), aws.String("p90"), cwtypes.StandardUnitSeconds
	w.putAlarm(t, &p90)
}

// TestDescribeAlarmsForMetricFilters covers both protocols. The query
// protocol returned InvalidAction before this fix, and ExtendedStatistic and
// Unit were ignored on CBOR.
func TestDescribeAlarmsForMetricFilters(t *testing.T) {
	tests := []struct {
		name string
		in   awscw.DescribeAlarmsForMetricInput
		want []string
	}{
		{name: "full dimensions", in: awscw.DescribeAlarmsForMetricInput{Dimensions: dims("Env", "prod", "Svc", "x")}, want: []string{"avg", "p90"}},
		{name: "dimension subset", in: awscw.DescribeAlarmsForMetricInput{Dimensions: dims("Env", "prod")}},
		{
			name: "extended statistic",
			in:   awscw.DescribeAlarmsForMetricInput{Dimensions: dims("Env", "prod", "Svc", "x"), ExtendedStatistic: aws.String("p90")},
			want: []string{"p90"},
		},
		{
			name: "statistic",
			in:   awscw.DescribeAlarmsForMetricInput{Dimensions: dims("Env", "prod", "Svc", "x"), Statistic: cwtypes.StatisticAverage},
			want: []string{"avg"},
		},
		{
			name: "unit",
			in:   awscw.DescribeAlarmsForMetricInput{Dimensions: dims("Env", "prod", "Svc", "x"), Unit: cwtypes.StandardUnitSeconds},
			want: []string{"p90"},
		},
	}

	for _, p := range cwProtocols() {
		for _, tc := range tests {
			t.Run(p.name+"/"+tc.name, func(t *testing.T) {
				w := p.build(t, nil)
				seedMetricAlarms(t, w)

				in := tc.in
				in.Namespace, in.MetricName = aws.String("T/App"), aws.String("A")

				out, code := w.alarmsForMetric(t, &in)
				if code != "" {
					t.Fatalf("DescribeAlarmsForMetric error %s", code)
				}

				got := make([]string, 0, len(out.MetricAlarms))
				for _, a := range out.MetricAlarms {
					got = append(got, aws.ToString(a.AlarmName))
				}

				if len(got) != len(tc.want) {
					t.Fatalf("alarms = %v, want %v", got, tc.want)
				}

				for i := range got {
					if got[i] != tc.want[i] {
						t.Fatalf("alarms = %v, want %v", got, tc.want)
					}
				}
			})
		}
	}
}

// TestQueryDescribeAlarmsForMetricMissingNamespace: the SDK checks required
// fields itself, so only the query protocol reaches the server without one.
func TestQueryDescribeAlarmsForMetricMissingNamespace(t *testing.T) {
	w := newQueryWire(t, nil)

	_, code := w.alarmsForMetric(t, &awscw.DescribeAlarmsForMetricInput{MetricName: aws.String("A")})
	if code != "MissingParameter" {
		t.Fatalf("error code = %q, want MissingParameter", code)
	}
}
