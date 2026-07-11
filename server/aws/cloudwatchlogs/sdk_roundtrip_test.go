package cloudwatchlogs_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	"github.com/stackshy/cloudemu"
	awsserver "github.com/stackshy/cloudemu/server/aws"
)

func newLogsClient(t *testing.T) *cwl.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{CloudWatchLogs: cloud.CloudWatchLogs})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return cwl.NewFromConfig(cfg, func(o *cwl.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKLogGroupLifecycle(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{
		LogGroupName: aws.String("/app/api"),
		Tags:         map[string]string{"env": "test"},
	}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	desc, err := client.DescribeLogGroups(ctx, &cwl.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String("/app"),
	})
	if err != nil {
		t.Fatalf("DescribeLogGroups: %v", err)
	}

	if len(desc.LogGroups) != 1 || aws.ToString(desc.LogGroups[0].LogGroupName) != "/app/api" {
		t.Fatalf("DescribeLogGroups = %+v, want one group /app/api", desc.LogGroups)
	}

	if aws.ToString(desc.LogGroups[0].Arn) == "" {
		t.Fatalf("log group ARN is empty: %+v", desc.LogGroups[0])
	}

	if _, err := client.DeleteLogGroup(ctx, &cwl.DeleteLogGroupInput{
		LogGroupName: aws.String("/app/api"),
	}); err != nil {
		t.Fatalf("DeleteLogGroup: %v", err)
	}

	after, err := client.DescribeLogGroups(ctx, &cwl.DescribeLogGroupsInput{})
	if err != nil {
		t.Fatalf("DescribeLogGroups after delete: %v", err)
	}

	if len(after.LogGroups) != 0 {
		t.Fatalf("got %d groups after delete, want 0", len(after.LogGroups))
	}
}

func TestSDKPutAndGetLogEvents(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{
		LogGroupName: aws.String("/app/svc"),
	}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	if _, err := client.CreateLogStream(ctx, &cwl.CreateLogStreamInput{
		LogGroupName:  aws.String("/app/svc"),
		LogStreamName: aws.String("instance-1"),
	}); err != nil {
		t.Fatalf("CreateLogStream: %v", err)
	}

	streams, err := client.DescribeLogStreams(ctx, &cwl.DescribeLogStreamsInput{
		LogGroupName: aws.String("/app/svc"),
	})
	if err != nil {
		t.Fatalf("DescribeLogStreams: %v", err)
	}

	if len(streams.LogStreams) != 1 || aws.ToString(streams.LogStreams[0].LogStreamName) != "instance-1" {
		t.Fatalf("DescribeLogStreams = %+v, want one stream instance-1", streams.LogStreams)
	}

	base := time.Now().UTC().Truncate(time.Millisecond)

	if _, err := client.PutLogEvents(ctx, &cwl.PutLogEventsInput{
		LogGroupName:  aws.String("/app/svc"),
		LogStreamName: aws.String("instance-1"),
		LogEvents: []cwltypes.InputLogEvent{
			{Timestamp: aws.Int64(base.UnixMilli()), Message: aws.String("hello world")},
			{Timestamp: aws.Int64(base.Add(time.Second).UnixMilli()), Message: aws.String("error: boom")},
		},
	}); err != nil {
		t.Fatalf("PutLogEvents: %v", err)
	}

	got, err := client.GetLogEvents(ctx, &cwl.GetLogEventsInput{
		LogGroupName:  aws.String("/app/svc"),
		LogStreamName: aws.String("instance-1"),
	})
	if err != nil {
		t.Fatalf("GetLogEvents: %v", err)
	}

	if len(got.Events) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(got.Events), got.Events)
	}

	if aws.ToString(got.Events[0].Message) != "hello world" {
		t.Fatalf("first event = %q, want hello world", aws.ToString(got.Events[0].Message))
	}

	if aws.ToInt64(got.Events[0].Timestamp) != base.UnixMilli() {
		t.Fatalf("first event ts = %d, want %d", aws.ToInt64(got.Events[0].Timestamp), base.UnixMilli())
	}

	// FilterLogEvents across the group with a substring pattern.
	filtered, err := client.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{
		LogGroupName:  aws.String("/app/svc"),
		FilterPattern: aws.String("error"),
	})
	if err != nil {
		t.Fatalf("FilterLogEvents: %v", err)
	}

	if len(filtered.Events) != 1 || aws.ToString(filtered.Events[0].Message) != "error: boom" {
		t.Fatalf("FilterLogEvents = %+v, want [error: boom]", filtered.Events)
	}

	if aws.ToString(filtered.Events[0].LogStreamName) != "instance-1" {
		t.Fatalf("filtered event stream = %q, want instance-1", aws.ToString(filtered.Events[0].LogStreamName))
	}

	if _, err := client.DeleteLogStream(ctx, &cwl.DeleteLogStreamInput{
		LogGroupName:  aws.String("/app/svc"),
		LogStreamName: aws.String("instance-1"),
	}); err != nil {
		t.Fatalf("DeleteLogStream: %v", err)
	}
}

func TestSDKLogsErrors(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{
		LogGroupName: aws.String("dup"),
	}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	_, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String("dup")})

	var exists *cwltypes.ResourceAlreadyExistsException
	if !errors.As(err, &exists) {
		t.Fatalf("duplicate CreateLogGroup: got %v, want ResourceAlreadyExistsException", err)
	}

	_, err = client.CreateLogStream(ctx, &cwl.CreateLogStreamInput{
		LogGroupName:  aws.String("missing"),
		LogStreamName: aws.String("s"),
	})

	var notFound *cwltypes.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("CreateLogStream(missing group): got %v, want ResourceNotFoundException", err)
	}

	_, err = client.DeleteLogGroup(ctx, &cwl.DeleteLogGroupInput{LogGroupName: aws.String("missing")})
	if !errors.As(err, &notFound) {
		t.Fatalf("DeleteLogGroup(missing): got %v, want ResourceNotFoundException", err)
	}
}
