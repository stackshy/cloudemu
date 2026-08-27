package cloudwatchlogs_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// TestSDKDescribeLogStreamsPrefix covers the logStreamNamePrefix finding:
// DescribeLogStreams must filter streams to those whose name starts with the
// prefix, not silently ignore it and return every stream.
func TestSDKDescribeLogStreamsPrefix(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{
		LogGroupName: aws.String("grp"),
	}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	for _, name := range []string{"app-1", "app-2", "worker-1"} {
		if _, err := client.CreateLogStream(ctx, &cwl.CreateLogStreamInput{
			LogGroupName:  aws.String("grp"),
			LogStreamName: aws.String(name),
		}); err != nil {
			t.Fatalf("CreateLogStream(%s): %v", name, err)
		}
	}

	out, err := client.DescribeLogStreams(ctx, &cwl.DescribeLogStreamsInput{
		LogGroupName:        aws.String("grp"),
		LogStreamNamePrefix: aws.String("app-"),
	})
	if err != nil {
		t.Fatalf("DescribeLogStreams: %v", err)
	}

	if len(out.LogStreams) != 2 {
		t.Fatalf("streams = %d, want 2 (only app-* streams)", len(out.LogStreams))
	}

	for _, s := range out.LogStreams {
		if got := aws.ToString(s.LogStreamName); got != "app-1" && got != "app-2" {
			t.Fatalf("unexpected stream %q returned for prefix app-", got)
		}
	}
}

// TestSDKPutRetentionPolicyInvalid covers the retention-validation finding: a
// retentionInDays value outside the allowed set must be rejected with
// InvalidParameterException, not accepted and stored.
func TestSDKPutRetentionPolicyInvalid(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{
		LogGroupName: aws.String("grp"),
	}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	_, err := client.PutRetentionPolicy(ctx, &cwl.PutRetentionPolicyInput{
		LogGroupName:    aws.String("grp"),
		RetentionInDays: aws.Int32(17),
	})
	if err == nil {
		t.Fatalf("PutRetentionPolicy(17): got nil error, want InvalidParameterException")
	}

	var invalid *cwltypes.InvalidParameterException
	if !errors.As(err, &invalid) {
		t.Fatalf("PutRetentionPolicy(17) error = %v, want InvalidParameterException", err)
	}

	// A valid value must still be accepted and reflected on the group.
	if _, err := client.PutRetentionPolicy(ctx, &cwl.PutRetentionPolicyInput{
		LogGroupName:    aws.String("grp"),
		RetentionInDays: aws.Int32(30),
	}); err != nil {
		t.Fatalf("PutRetentionPolicy(30): %v", err)
	}

	out, err := client.DescribeLogGroups(ctx, &cwl.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String("grp"),
	})
	if err != nil {
		t.Fatalf("DescribeLogGroups: %v", err)
	}

	if len(out.LogGroups) != 1 || aws.ToInt32(out.LogGroups[0].RetentionInDays) != 30 {
		t.Fatalf("retention = %+v, want 30", out.LogGroups)
	}
}
