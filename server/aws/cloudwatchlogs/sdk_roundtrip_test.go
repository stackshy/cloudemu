package cloudwatchlogs_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
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

// TestSDKPutRetentionPolicy is a regression guard for issue #319:
// PutRetentionPolicy was unimplemented (UnknownOperationException).
func TestSDKPutRetentionPolicy(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{
		LogGroupName: aws.String("/app/ret"),
	}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	if _, err := client.PutRetentionPolicy(ctx, &cwl.PutRetentionPolicyInput{
		LogGroupName: aws.String("/app/ret"), RetentionInDays: aws.Int32(14),
	}); err != nil {
		t.Fatalf("PutRetentionPolicy: %v", err)
	}

	desc, err := client.DescribeLogGroups(ctx, &cwl.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String("/app/ret"),
	})
	if err != nil {
		t.Fatalf("DescribeLogGroups: %v", err)
	}

	if len(desc.LogGroups) != 1 || aws.ToInt32(desc.LogGroups[0].RetentionInDays) != 14 {
		t.Fatalf("retention not applied: %+v", desc.LogGroups)
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

// TestSDKDescribeLogGroupsPagination guards that DescribeLogGroups honors Limit
// and hands back a NextToken the SDK paginator can follow. Without pagination
// the first page returns all groups and NextToken is nil, wedging the paginator.
func TestSDKDescribeLogGroupsPagination(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	names := []string{"/page/a", "/page/b", "/page/c"}
	for _, n := range names {
		if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String(n)}); err != nil {
			t.Fatalf("CreateLogGroup(%s): %v", n, err)
		}
	}

	first, err := client.DescribeLogGroups(ctx, &cwl.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String("/page"),
		Limit:              aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("DescribeLogGroups page 1: %v", err)
	}

	if len(first.LogGroups) != 2 {
		t.Fatalf("page 1 = %d groups, want 2 (Limit ignored?)", len(first.LogGroups))
	}

	if aws.ToString(first.NextToken) == "" {
		t.Fatalf("page 1 NextToken empty, want a token to fetch the third group")
	}

	second, err := client.DescribeLogGroups(ctx, &cwl.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String("/page"),
		Limit:              aws.Int32(2),
		NextToken:          first.NextToken,
	})
	if err != nil {
		t.Fatalf("DescribeLogGroups page 2: %v", err)
	}

	if len(second.LogGroups) != 1 || aws.ToString(second.LogGroups[0].LogGroupName) != "/page/c" {
		t.Fatalf("page 2 = %+v, want one group /page/c", second.LogGroups)
	}

	if aws.ToString(second.NextToken) != "" {
		t.Fatalf("page 2 NextToken = %q, want empty (pagination finished)", aws.ToString(second.NextToken))
	}
}

// TestSDKDescribeLogStreamsArnAndPagination guards that log streams carry an ARN
// (Terraform's aws_cloudwatch_log_stream reads it) and that DescribeLogStreams
// honors Limit + NextToken.
func TestSDKDescribeLogStreamsArnAndPagination(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String("/streams/grp")}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	for _, s := range []string{"s-a", "s-b", "s-c"} {
		if _, err := client.CreateLogStream(ctx, &cwl.CreateLogStreamInput{
			LogGroupName: aws.String("/streams/grp"), LogStreamName: aws.String(s),
		}); err != nil {
			t.Fatalf("CreateLogStream(%s): %v", s, err)
		}
	}

	first, err := client.DescribeLogStreams(ctx, &cwl.DescribeLogStreamsInput{
		LogGroupName: aws.String("/streams/grp"),
		Limit:        aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("DescribeLogStreams page 1: %v", err)
	}

	if len(first.LogStreams) != 2 {
		t.Fatalf("page 1 = %d streams, want 2 (Limit ignored?)", len(first.LogStreams))
	}

	arn := aws.ToString(first.LogStreams[0].Arn)
	if !strings.HasPrefix(arn, "arn:aws:logs:") ||
		!strings.Contains(arn, ":log-group:/streams/grp:log-stream:s-a") {
		t.Fatalf("stream ARN = %q, want arn:aws:logs:...:log-group:/streams/grp:log-stream:s-a", arn)
	}

	if aws.ToString(first.NextToken) == "" {
		t.Fatalf("page 1 NextToken empty, want a token for the third stream")
	}

	second, err := client.DescribeLogStreams(ctx, &cwl.DescribeLogStreamsInput{
		LogGroupName: aws.String("/streams/grp"),
		Limit:        aws.Int32(2),
		NextToken:    first.NextToken,
	})
	if err != nil {
		t.Fatalf("DescribeLogStreams page 2: %v", err)
	}

	if len(second.LogStreams) != 1 || aws.ToString(second.LogStreams[0].LogStreamName) != "s-c" {
		t.Fatalf("page 2 = %+v, want one stream s-c", second.LogStreams)
	}
}

// TestSDKLogStreamFirstEventAndIngestionTime guards that a stream reports its
// FirstEventTimestamp and that events carry a wall-clock IngestionTime distinct
// from the caller's event timestamp. The event is stamped an hour in the past;
// without the fix IngestionTime echoes that stale timestamp instead of "now".
func TestSDKLogStreamFirstEventAndIngestionTime(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String("/ingest/grp")}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	if _, err := client.CreateLogStream(ctx, &cwl.CreateLogStreamInput{
		LogGroupName: aws.String("/ingest/grp"), LogStreamName: aws.String("s1"),
	}); err != nil {
		t.Fatalf("CreateLogStream: %v", err)
	}

	eventTime := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	before := time.Now().UTC().Add(-time.Minute)

	if _, err := client.PutLogEvents(ctx, &cwl.PutLogEventsInput{
		LogGroupName:  aws.String("/ingest/grp"),
		LogStreamName: aws.String("s1"),
		LogEvents: []cwltypes.InputLogEvent{
			{Timestamp: aws.Int64(eventTime.UnixMilli()), Message: aws.String("aged event")},
		},
	}); err != nil {
		t.Fatalf("PutLogEvents: %v", err)
	}

	got, err := client.GetLogEvents(ctx, &cwl.GetLogEventsInput{
		LogGroupName: aws.String("/ingest/grp"), LogStreamName: aws.String("s1"),
	})
	if err != nil {
		t.Fatalf("GetLogEvents: %v", err)
	}

	if len(got.Events) != 1 {
		t.Fatalf("got %d events, want 1", len(got.Events))
	}

	if aws.ToInt64(got.Events[0].Timestamp) != eventTime.UnixMilli() {
		t.Fatalf("event timestamp = %d, want %d", aws.ToInt64(got.Events[0].Timestamp), eventTime.UnixMilli())
	}

	ingestion := aws.ToInt64(got.Events[0].IngestionTime)
	if ingestion < before.UnixMilli() {
		t.Fatalf("IngestionTime = %d, want a wall-clock time >= %d (not the stale event ts %d)",
			ingestion, before.UnixMilli(), eventTime.UnixMilli())
	}

	streams, err := client.DescribeLogStreams(ctx, &cwl.DescribeLogStreamsInput{
		LogGroupName: aws.String("/ingest/grp"),
	})
	if err != nil {
		t.Fatalf("DescribeLogStreams: %v", err)
	}

	if len(streams.LogStreams) != 1 {
		t.Fatalf("got %d streams, want 1", len(streams.LogStreams))
	}

	if aws.ToInt64(streams.LogStreams[0].FirstEventTimestamp) != eventTime.UnixMilli() {
		t.Fatalf("FirstEventTimestamp = %d, want %d",
			aws.ToInt64(streams.LogStreams[0].FirstEventTimestamp), eventTime.UnixMilli())
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
