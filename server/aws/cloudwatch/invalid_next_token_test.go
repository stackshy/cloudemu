package cloudwatch_test

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// TestInvalidNextToken: a token the server did not issue is rejected. It was
// read as offset 0 before, so a bad token silently restarted paging.
// Each op returns the code its API reference documents: InvalidNextToken,
// or InvalidParameterValue for ListMetrics and ListDashboards.
func TestInvalidNextToken(t *testing.T) {
	const bogus = "not-a-token"

	tests := []struct {
		name string
		call func(t *testing.T, w cwWire) string
		want string
	}{
		{
			name: "GetMetricData",
			want: "InvalidNextToken",
			call: func(t *testing.T, w cwWire) string {
				t.Helper()

				now := time.Now().UTC()
				_, code := w.getMetricData(t, &awscw.GetMetricDataInput{
					StartTime: aws.Time(now.Add(-time.Hour)), EndTime: aws.Time(now), NextToken: aws.String(bogus),
					MetricDataQueries: []cwtypes.MetricDataQuery{sumQuery("m1", nil)},
				})

				return code
			},
		},
		{
			name: "DescribeAlarms",
			want: "InvalidNextToken",
			call: func(t *testing.T, w cwWire) string {
				t.Helper()
				return w.alarmsCode(t, &awscw.DescribeAlarmsInput{NextToken: aws.String(bogus)})
			},
		},
		{
			name: "ListMetrics",
			want: "InvalidParameterValue",
			call: func(t *testing.T, w cwWire) string {
				t.Helper()
				return w.listMetricsCode(t, &awscw.ListMetricsInput{NextToken: aws.String(bogus)})
			},
		},
		{name: "DescribeAlarmHistory", want: "InvalidNextToken", call: tokenCall("DescribeAlarmHistory", bogus)},
		{name: "ListMetricStreams", want: "InvalidNextToken", call: tokenCall("ListMetricStreams", bogus)},
		{name: "ListDashboards", want: "InvalidParameterValue", call: tokenCall("ListDashboards", bogus)},
	}

	for _, p := range cwProtocols() {
		for _, tc := range tests {
			t.Run(p.name+"/"+tc.name, func(t *testing.T) {
				if got := tc.call(t, p.build(t, nil)); got != tc.want {
					t.Fatalf("error code = %q, want %q", got, tc.want)
				}
			})
		}
	}
}

func tokenCall(op, token string) func(t *testing.T, w cwWire) string {
	return func(t *testing.T, w cwWire) string {
		t.Helper()
		return w.tokenCode(t, op, token)
	}
}
