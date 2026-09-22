package athena_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsathena "github.com/aws/aws-sdk-go-v2/service/athena"
	athenatypes "github.com/aws/aws-sdk-go-v2/service/athena/types"
)

// TestSDKAthenaQueryStatisticsBreakdown pins that GetQueryExecution returns the
// full real Statistics timing breakdown (queue, planning, service pre- and
// post-processing), which the AWS/Athena metrics of the same names mirror.
func TestSDKAthenaQueryStatisticsBreakdown(t *testing.T) {
	c := newAthenaClient(t)
	ctx := context.Background()

	start, err := c.StartQueryExecution(ctx, &awsathena.StartQueryExecutionInput{
		QueryString:         aws.String("SELECT 1"),
		ResultConfiguration: &athenatypes.ResultConfiguration{OutputLocation: aws.String("s3://out/")},
	})
	if err != nil {
		t.Fatalf("StartQueryExecution: %v", err)
	}

	got, err := c.GetQueryExecution(ctx, &awsathena.GetQueryExecutionInput{QueryExecutionId: start.QueryExecutionId})
	if err != nil {
		t.Fatalf("GetQueryExecution: %v", err)
	}

	st := got.QueryExecution.Statistics
	if st == nil || st.QueryQueueTimeInMillis == nil || st.QueryPlanningTimeInMillis == nil ||
		st.ServicePreProcessingTimeInMillis == nil || st.ServiceProcessingTimeInMillis == nil {
		t.Fatalf("Statistics missing the timing breakdown: %+v", st)
	}
}
