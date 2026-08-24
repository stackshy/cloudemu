package cloudwatch_test

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// TestSDKGetMetricDataExpression covers finding 6: a raw metric query with
// ReturnData=false is used only as an input and is not returned, while a math
// expression referencing it returns its computed series.
func TestSDKGetMetricDataExpression(t *testing.T) {
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
		MetricDataQueries: []cwtypes.MetricDataQuery{
			{
				Id: aws.String("m1"),
				MetricStat: &cwtypes.MetricStat{
					Metric: &cwtypes.Metric{
						Namespace:  aws.String("MyApp"),
						MetricName: aws.String("Latency"),
					},
					Period: aws.Int32(60),
					Stat:   aws.String("Sum"),
				},
				ReturnData: aws.Bool(false),
			},
			{
				Id:         aws.String("e1"),
				Expression: aws.String("m1*2"),
				ReturnData: aws.Bool(true),
			},
		},
	})
	if err != nil {
		t.Fatalf("GetMetricData: %v", err)
	}

	// Only e1 is returned; the ReturnData=false input query is dropped.
	if len(out.MetricDataResults) != 1 {
		t.Fatalf("results = %+v, want exactly 1 (only e1)", out.MetricDataResults)
	}

	res := out.MetricDataResults[0]
	if aws.ToString(res.Id) != "e1" {
		t.Fatalf("result Id = %q, want e1", aws.ToString(res.Id))
	}

	var total float64
	for _, v := range res.Values {
		total += v
	}

	// m1 = 10+20+30 = 60; e1 = m1*2 element-wise => 20+40+60 = 120.
	if total != 120 {
		t.Fatalf("summed e1 values = %v, want 120 (2*(10+20+30))", total)
	}
}
