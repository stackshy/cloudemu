package cloudwatchlogs_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// TestSDKMetricFilterTransformationFields reproduces the metric-filter round-trip
// bug: DefaultValue and Unit set on PutMetricFilter must survive to
// DescribeMetricFilters (previously dropped to nil / "").
func TestSDKMetricFilterTransformationFields(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String("/mf/g")}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	if _, err := client.PutMetricFilter(ctx, &cwl.PutMetricFilterInput{
		LogGroupName:  aws.String("/mf/g"),
		FilterName:    aws.String("errors"),
		FilterPattern: aws.String("ERROR"),
		MetricTransformations: []cwltypes.MetricTransformation{{
			MetricName:      aws.String("ErrorCount"),
			MetricNamespace: aws.String("MyApp"),
			MetricValue:     aws.String("1"),
			DefaultValue:    aws.Float64(0),
			Unit:            cwltypes.StandardUnitCount,
		}},
	}); err != nil {
		t.Fatalf("PutMetricFilter: %v", err)
	}

	desc, err := client.DescribeMetricFilters(ctx, &cwl.DescribeMetricFiltersInput{
		LogGroupName: aws.String("/mf/g"),
	})
	if err != nil {
		t.Fatalf("DescribeMetricFilters: %v", err)
	}

	if len(desc.MetricFilters) != 1 || len(desc.MetricFilters[0].MetricTransformations) != 1 {
		t.Fatalf("DescribeMetricFilters = %+v, want one filter with one transformation", desc.MetricFilters)
	}

	mt := desc.MetricFilters[0].MetricTransformations[0]

	if mt.DefaultValue == nil || aws.ToFloat64(mt.DefaultValue) != 0 {
		t.Fatalf("DefaultValue = %v, want 0", mt.DefaultValue)
	}

	if mt.Unit != cwltypes.StandardUnitCount {
		t.Fatalf("Unit = %q, want Count", mt.Unit)
	}
}
