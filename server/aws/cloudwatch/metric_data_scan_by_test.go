package cloudwatch_test

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// TestGetMetricDataScanBy: AWS returns newest first unless ScanBy says
// TimestampAscending. The old code always returned oldest first.
func TestGetMetricDataScanBy(t *testing.T) {
	tests := []struct {
		name   string
		scanBy cwtypes.ScanBy
		want   []float64
		code   string
	}{
		{name: "default is descending", want: []float64{3, 2, 1}},
		{name: "descending", scanBy: cwtypes.ScanByTimestampDescending, want: []float64{3, 2, 1}},
		{name: "ascending", scanBy: cwtypes.ScanByTimestampAscending, want: []float64{1, 2, 3}},
		{name: "bad value", scanBy: "Sideways", code: "ValidationError"},
	}

	for _, p := range cwProtocols() {
		for _, tc := range tests {
			t.Run(p.name+"/"+tc.name, func(t *testing.T) {
				w := p.build(t, nil)
				start, end := seedSeries(t, w)

				out, code := w.getMetricData(t, &awscw.GetMetricDataInput{
					StartTime: aws.Time(start), EndTime: aws.Time(end), ScanBy: tc.scanBy,
					MetricDataQueries: []cwtypes.MetricDataQuery{sumQuery("m1", nil)},
				})
				if code != tc.code {
					t.Fatalf("error code = %q, want %q", code, tc.code)
				}

				if tc.code != "" {
					return
				}

				got := out.MetricDataResults[0]
				if !floatsEqual(got.Values, tc.want) {
					t.Fatalf("values = %v, want %v", got.Values, tc.want)
				}

				for i := 1; i < len(got.Timestamps); i++ {
					newer := got.Timestamps[i].After(got.Timestamps[i-1])
					if newer != (tc.scanBy == cwtypes.ScanByTimestampAscending) {
						t.Fatalf("timestamps out of order: %v", got.Timestamps)
					}
				}
			})
		}
	}
}

// TestGetMetricDataPagesInScanOrder: with MaxDatapoints=2 the first page
// holds the two newest points and the next page the oldest.
func TestGetMetricDataPagesInScanOrder(t *testing.T) {
	for _, p := range cwProtocols() {
		t.Run(p.name, func(t *testing.T) {
			w := p.build(t, nil)
			start, end := seedSeries(t, w)

			in := &awscw.GetMetricDataInput{
				StartTime: aws.Time(start), EndTime: aws.Time(end), MaxDatapoints: aws.Int32(2),
				MetricDataQueries: []cwtypes.MetricDataQuery{sumQuery("m1", nil)},
			}

			first, code := w.getMetricData(t, in)
			if code != "" {
				t.Fatalf("page 1 error %s", code)
			}

			row := first.MetricDataResults[0]
			if !floatsEqual(row.Values, []float64{3, 2}) || row.StatusCode != cwtypes.StatusCodePartialData {
				t.Fatalf("page 1 = %v %s, want [3 2] PartialData", row.Values, row.StatusCode)
			}

			if first.NextToken == nil {
				t.Fatal("page 1 has no NextToken")
			}

			in.NextToken = first.NextToken

			second, code := w.getMetricData(t, in)
			if code != "" {
				t.Fatalf("page 2 error %s", code)
			}

			row = second.MetricDataResults[0]
			if !floatsEqual(row.Values, []float64{1}) || row.StatusCode != cwtypes.StatusCodeComplete {
				t.Fatalf("page 2 = %v %s, want [1] Complete", row.Values, row.StatusCode)
			}

			if second.NextToken != nil {
				t.Fatalf("page 2 NextToken = %q, want none", *second.NextToken)
			}
		})
	}
}
