package cloudwatchlogs_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// TestSDKSubscriptionFilterRoundTrip drives PutSubscriptionFilter /
// DescribeSubscriptionFilters / DeleteSubscriptionFilter with the real SDK,
// guarding the Terraform aws_cloudwatch_log_subscription_filter flow that
// previously failed with UnknownOperationException.
func TestSDKSubscriptionFilterRoundTrip(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	const (
		group = "/sub/app"
		dest  = "arn:aws:lambda:us-east-1:000000000000:function:log-sub"
		role  = "arn:aws:iam::000000000000:role/cwl"
	)

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String(group)}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	if _, err := client.PutSubscriptionFilter(ctx, &cwl.PutSubscriptionFilterInput{
		LogGroupName:   aws.String(group),
		FilterName:     aws.String("to-lambda"),
		FilterPattern:  aws.String("ERROR"),
		DestinationArn: aws.String(dest),
		RoleArn:        aws.String(role),
		Distribution:   cwltypes.DistributionByLogStream,
	}); err != nil {
		t.Fatalf("PutSubscriptionFilter: %v", err)
	}

	desc, err := client.DescribeSubscriptionFilters(ctx, &cwl.DescribeSubscriptionFiltersInput{
		LogGroupName: aws.String(group),
	})
	if err != nil {
		t.Fatalf("DescribeSubscriptionFilters: %v", err)
	}

	if len(desc.SubscriptionFilters) != 1 {
		t.Fatalf("got %d filters, want 1: %+v", len(desc.SubscriptionFilters), desc.SubscriptionFilters)
	}

	sf := desc.SubscriptionFilters[0]
	if aws.ToString(sf.FilterName) != "to-lambda" ||
		aws.ToString(sf.FilterPattern) != "ERROR" ||
		aws.ToString(sf.DestinationArn) != dest ||
		aws.ToString(sf.RoleArn) != role ||
		aws.ToString(sf.LogGroupName) != group {
		t.Fatalf("filter round-trip mismatch: %+v", sf)
	}

	if sf.Distribution != cwltypes.DistributionByLogStream {
		t.Fatalf("distribution = %q, want ByLogStream", sf.Distribution)
	}

	if aws.ToInt64(sf.CreationTime) == 0 {
		t.Fatalf("creationTime is zero: %+v", sf)
	}

	// filterNamePrefix narrows the listing.
	none, err := client.DescribeSubscriptionFilters(ctx, &cwl.DescribeSubscriptionFiltersInput{
		LogGroupName: aws.String(group), FilterNamePrefix: aws.String("zzz"),
	})
	if err != nil {
		t.Fatalf("DescribeSubscriptionFilters(prefix): %v", err)
	}

	if len(none.SubscriptionFilters) != 0 {
		t.Fatalf("prefix zzz = %d filters, want 0", len(none.SubscriptionFilters))
	}

	if _, err := client.DeleteSubscriptionFilter(ctx, &cwl.DeleteSubscriptionFilterInput{
		LogGroupName: aws.String(group), FilterName: aws.String("to-lambda"),
	}); err != nil {
		t.Fatalf("DeleteSubscriptionFilter: %v", err)
	}

	after, err := client.DescribeSubscriptionFilters(ctx, &cwl.DescribeSubscriptionFiltersInput{
		LogGroupName: aws.String(group),
	})
	if err != nil {
		t.Fatalf("DescribeSubscriptionFilters after delete: %v", err)
	}

	if len(after.SubscriptionFilters) != 0 {
		t.Fatalf("got %d filters after delete, want 0", len(after.SubscriptionFilters))
	}
}

// TestSDKSubscriptionFilterErrors guards the error surface: a missing log group
// is ResourceNotFoundException and a third distinct filter is LimitExceededException.
func TestSDKSubscriptionFilterErrors(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	const dest = "arn:aws:lambda:us-east-1:000000000000:function:log-sub"

	_, err := client.PutSubscriptionFilter(ctx, &cwl.PutSubscriptionFilterInput{
		LogGroupName:   aws.String("/missing/grp"),
		FilterName:     aws.String("f"),
		FilterPattern:  aws.String(""),
		DestinationArn: aws.String(dest),
	})

	var notFound *cwltypes.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("PutSubscriptionFilter(missing group): got %v, want ResourceNotFoundException", err)
	}

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String("/lim/grp")}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	for _, name := range []string{"f1", "f2"} {
		if _, err := client.PutSubscriptionFilter(ctx, &cwl.PutSubscriptionFilterInput{
			LogGroupName:   aws.String("/lim/grp"),
			FilterName:     aws.String(name),
			FilterPattern:  aws.String(""),
			DestinationArn: aws.String(dest),
		}); err != nil {
			t.Fatalf("PutSubscriptionFilter(%s): %v", name, err)
		}
	}

	_, err = client.PutSubscriptionFilter(ctx, &cwl.PutSubscriptionFilterInput{
		LogGroupName:   aws.String("/lim/grp"),
		FilterName:     aws.String("f3"),
		FilterPattern:  aws.String(""),
		DestinationArn: aws.String(dest),
	})

	var limit *cwltypes.LimitExceededException
	if !errors.As(err, &limit) {
		t.Fatalf("third PutSubscriptionFilter: got %v, want LimitExceededException", err)
	}
}
