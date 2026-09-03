package cloudwatchlogs_test

import (
	"context"
	"errors"
	"fmt"
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

// TestSDKGetLogEventsPagination guards that GetLogEvents honors Limit + nextToken
// and returns real forward/backward tokens (never the hardcoded "f/0"/"b/0"): the
// second page must advance past the first, and the terminal call echoes the same
// forward token so the SDK paginator stops.
func TestSDKGetLogEventsPagination(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String("/pg/g")}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	if _, err := client.CreateLogStream(ctx, &cwl.CreateLogStreamInput{
		LogGroupName: aws.String("/pg/g"), LogStreamName: aws.String("s1"),
	}); err != nil {
		t.Fatalf("CreateLogStream: %v", err)
	}

	base := time.Now().UTC().Truncate(time.Millisecond)

	events := make([]cwltypes.InputLogEvent, 0, 3)
	for i := 0; i < 3; i++ {
		events = append(events, cwltypes.InputLogEvent{
			Timestamp: aws.Int64(base.Add(time.Duration(i) * time.Second).UnixMilli()),
			Message:   aws.String(fmt.Sprintf("e%d", i)),
		})
	}

	if _, err := client.PutLogEvents(ctx, &cwl.PutLogEventsInput{
		LogGroupName: aws.String("/pg/g"), LogStreamName: aws.String("s1"), LogEvents: events,
	}); err != nil {
		t.Fatalf("PutLogEvents: %v", err)
	}

	// StartFromHead=true walks oldest→newest; the AWS default (false) returns the
	// latest events first, which TestSDKGetLogEventsStartFromHeadDefault covers.
	first, err := client.GetLogEvents(ctx, &cwl.GetLogEventsInput{
		LogGroupName: aws.String("/pg/g"), LogStreamName: aws.String("s1"),
		Limit: aws.Int32(2), StartFromHead: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("GetLogEvents page 1: %v", err)
	}

	if len(first.Events) != 2 || aws.ToString(first.Events[0].Message) != "e0" {
		t.Fatalf("page 1 = %+v, want [e0 e1]", first.Events)
	}

	if aws.ToString(first.NextForwardToken) == "" || aws.ToString(first.NextForwardToken) == "f/0" {
		t.Fatalf("page 1 NextForwardToken = %q, want a real cursor", aws.ToString(first.NextForwardToken))
	}

	second, err := client.GetLogEvents(ctx, &cwl.GetLogEventsInput{
		LogGroupName: aws.String("/pg/g"), LogStreamName: aws.String("s1"),
		Limit: aws.Int32(2), NextToken: first.NextForwardToken,
	})
	if err != nil {
		t.Fatalf("GetLogEvents page 2: %v", err)
	}

	if len(second.Events) != 1 || aws.ToString(second.Events[0].Message) != "e2" {
		t.Fatalf("page 2 = %+v, want [e2] (nextToken ignored?)", second.Events)
	}

	// Terminal page: no more events, forward token echoes the one we passed in.
	third, err := client.GetLogEvents(ctx, &cwl.GetLogEventsInput{
		LogGroupName: aws.String("/pg/g"), LogStreamName: aws.String("s1"),
		Limit: aws.Int32(2), NextToken: second.NextForwardToken,
	})
	if err != nil {
		t.Fatalf("GetLogEvents page 3: %v", err)
	}

	if len(third.Events) != 0 {
		t.Fatalf("page 3 = %+v, want no events", third.Events)
	}

	if aws.ToString(third.NextForwardToken) != aws.ToString(second.NextForwardToken) {
		t.Fatalf("terminal forward token = %q, want same as %q",
			aws.ToString(third.NextForwardToken), aws.ToString(second.NextForwardToken))
	}
}

func mustLogGroupStream(t *testing.T, client *cwl.Client, group, stream string) {
	t.Helper()

	ctx := context.Background()
	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String(group)}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	if _, err := client.CreateLogStream(ctx, &cwl.CreateLogStreamInput{
		LogGroupName: aws.String(group), LogStreamName: aws.String(stream),
	}); err != nil {
		t.Fatalf("CreateLogStream: %v", err)
	}
}

// TestSDKGetLogEventsStartFromHeadDefault pins the AWS default: with
// startFromHead unset (false), GetLogEvents returns the LATEST events first.
func TestSDKGetLogEventsStartFromHeadDefault(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	mustLogGroupStream(t, client, "/h/g", "s1")

	base := time.Now().UTC().Truncate(time.Millisecond)
	events := make([]cwltypes.InputLogEvent, 0, 3)
	for i := 0; i < 3; i++ {
		events = append(events, cwltypes.InputLogEvent{
			Timestamp: aws.Int64(base.Add(time.Duration(i) * time.Second).UnixMilli()),
			Message:   aws.String(fmt.Sprintf("e%d", i)),
		})
	}
	if _, err := client.PutLogEvents(ctx, &cwl.PutLogEventsInput{
		LogGroupName: aws.String("/h/g"), LogStreamName: aws.String("s1"), LogEvents: events,
	}); err != nil {
		t.Fatalf("PutLogEvents: %v", err)
	}

	out, err := client.GetLogEvents(ctx, &cwl.GetLogEventsInput{
		LogGroupName: aws.String("/h/g"), LogStreamName: aws.String("s1"), Limit: aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("GetLogEvents: %v", err)
	}

	if len(out.Events) != 2 || aws.ToString(out.Events[0].Message) != "e1" ||
		aws.ToString(out.Events[1].Message) != "e2" {
		t.Fatalf("default page = %+v, want the latest two [e1 e2]", out.Events)
	}

	// Backward pagination (the default latest-first direction) must reach OLDER
	// events, not repeat the current page. Follow NextBackwardToken to page 2.
	older, err := client.GetLogEvents(ctx, &cwl.GetLogEventsInput{
		LogGroupName: aws.String("/h/g"), LogStreamName: aws.String("s1"),
		Limit: aws.Int32(2), NextToken: out.NextBackwardToken,
	})
	if err != nil {
		t.Fatalf("GetLogEvents backward page: %v", err)
	}

	if len(older.Events) != 1 || aws.ToString(older.Events[0].Message) != "e0" {
		t.Fatalf("backward page = %+v, want the older event [e0] (backward token repeats page?)", older.Events)
	}

	// One more backward step stabilizes (no events, token echoes).
	last, err := client.GetLogEvents(ctx, &cwl.GetLogEventsInput{
		LogGroupName: aws.String("/h/g"), LogStreamName: aws.String("s1"),
		Limit: aws.Int32(2), NextToken: older.NextBackwardToken,
	})
	if err != nil {
		t.Fatalf("GetLogEvents backward terminal: %v", err)
	}

	if len(last.Events) != 0 {
		t.Fatalf("backward terminal page = %+v, want no events", last.Events)
	}
}

// TestSDKGetLogEventsBeyond100 pins that streams with more than the internal
// default page size are fully reachable via pagination (no silent 100-event cap).
func TestSDKGetLogEventsBeyond100(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	mustLogGroupStream(t, client, "/big/g", "s1")

	const total = 150
	base := time.Now().UTC().Truncate(time.Millisecond)
	events := make([]cwltypes.InputLogEvent, 0, total)
	for i := 0; i < total; i++ {
		events = append(events, cwltypes.InputLogEvent{
			Timestamp: aws.Int64(base.Add(time.Duration(i) * time.Millisecond).UnixMilli()),
			Message:   aws.String(fmt.Sprintf("e%d", i)),
		})
	}
	if _, err := client.PutLogEvents(ctx, &cwl.PutLogEventsInput{
		LogGroupName: aws.String("/big/g"), LogStreamName: aws.String("s1"), LogEvents: events,
	}); err != nil {
		t.Fatalf("PutLogEvents: %v", err)
	}

	// Walk head→tail and confirm all 150 events are seen (the last one, e149,
	// is only reachable if the internal 100 cap is gone).
	seen := 0
	var token *string
	for page := 0; page < 20; page++ {
		out, err := client.GetLogEvents(ctx, &cwl.GetLogEventsInput{
			LogGroupName: aws.String("/big/g"), LogStreamName: aws.String("s1"),
			Limit: aws.Int32(40), StartFromHead: aws.Bool(true), NextToken: token,
		})
		if err != nil {
			t.Fatalf("GetLogEvents page %d: %v", page, err)
		}
		if len(out.Events) == 0 {
			break
		}
		seen += len(out.Events)
		token = out.NextForwardToken
	}

	if seen != total {
		t.Fatalf("paginated walk saw %d events, want %d (100-event cap regression?)", seen, total)
	}
}

// TestSDKFilterLogEventsPagination guards that FilterLogEvents honors Limit and
// hands back a nextToken for the remainder. Without pagination the first page
// returns every match and nextToken is empty, wedging the SDK paginator.
func TestSDKFilterLogEventsPagination(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String("/pf/g")}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	if _, err := client.CreateLogStream(ctx, &cwl.CreateLogStreamInput{
		LogGroupName: aws.String("/pf/g"), LogStreamName: aws.String("s1"),
	}); err != nil {
		t.Fatalf("CreateLogStream: %v", err)
	}

	base := time.Now().UTC().Truncate(time.Millisecond)

	events := make([]cwltypes.InputLogEvent, 0, 3)
	for i := 0; i < 3; i++ {
		events = append(events, cwltypes.InputLogEvent{
			Timestamp: aws.Int64(base.Add(time.Duration(i) * time.Second).UnixMilli()),
			Message:   aws.String(fmt.Sprintf("match %d", i)),
		})
	}

	if _, err := client.PutLogEvents(ctx, &cwl.PutLogEventsInput{
		LogGroupName: aws.String("/pf/g"), LogStreamName: aws.String("s1"), LogEvents: events,
	}); err != nil {
		t.Fatalf("PutLogEvents: %v", err)
	}

	first, err := client.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{
		LogGroupName: aws.String("/pf/g"), FilterPattern: aws.String("match"), Limit: aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("FilterLogEvents page 1: %v", err)
	}

	if len(first.Events) != 2 {
		t.Fatalf("page 1 = %d events, want 2 (Limit ignored?)", len(first.Events))
	}

	if aws.ToString(first.NextToken) == "" {
		t.Fatalf("page 1 NextToken empty, want a token for the third match")
	}

	second, err := client.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{
		LogGroupName: aws.String("/pf/g"), FilterPattern: aws.String("match"),
		Limit: aws.Int32(2), NextToken: first.NextToken,
	})
	if err != nil {
		t.Fatalf("FilterLogEvents page 2: %v", err)
	}

	if len(second.Events) != 1 {
		t.Fatalf("page 2 = %d events, want 1", len(second.Events))
	}

	if aws.ToString(second.NextToken) != "" {
		t.Fatalf("page 2 NextToken = %q, want empty (pagination finished)", aws.ToString(second.NextToken))
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

// TestSDKPutLogEventsBatchValidation locks real CloudWatch Logs' PutLogEvents
// batch constraints: events in a single request must be in chronological
// order, and the oldest and newest event in a request cannot span more than
// 24 hours. Either violation is a hard InvalidParameterException that rejects
// the whole batch — nothing from it is ingested.
func TestSDKPutLogEventsBatchValidation(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String("/pv/g")}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	if _, err := client.CreateLogStream(ctx, &cwl.CreateLogStreamInput{
		LogGroupName: aws.String("/pv/g"), LogStreamName: aws.String("s1"),
	}); err != nil {
		t.Fatalf("CreateLogStream: %v", err)
	}

	base := time.Now().UTC().Truncate(time.Millisecond)

	_, err := client.PutLogEvents(ctx, &cwl.PutLogEventsInput{
		LogGroupName:  aws.String("/pv/g"),
		LogStreamName: aws.String("s1"),
		LogEvents: []cwltypes.InputLogEvent{
			{Timestamp: aws.Int64(base.Add(time.Second).UnixMilli()), Message: aws.String("second")},
			{Timestamp: aws.Int64(base.UnixMilli()), Message: aws.String("first")},
		},
	})

	var invalid *cwltypes.InvalidParameterException
	if !errors.As(err, &invalid) {
		t.Fatalf("out-of-order PutLogEvents: got %v, want InvalidParameterException", err)
	}

	_, err = client.PutLogEvents(ctx, &cwl.PutLogEventsInput{
		LogGroupName:  aws.String("/pv/g"),
		LogStreamName: aws.String("s1"),
		LogEvents: []cwltypes.InputLogEvent{
			{Timestamp: aws.Int64(base.UnixMilli()), Message: aws.String("early")},
			{Timestamp: aws.Int64(base.Add(25 * time.Hour).UnixMilli()), Message: aws.String("too late")},
		},
	})
	if !errors.As(err, &invalid) {
		t.Fatalf("batch spanning >24h PutLogEvents: got %v, want InvalidParameterException", err)
	}

	// Neither rejected batch ingested anything.
	got, gerr := client.GetLogEvents(ctx, &cwl.GetLogEventsInput{
		LogGroupName: aws.String("/pv/g"), LogStreamName: aws.String("s1"),
	})
	if gerr != nil {
		t.Fatalf("GetLogEvents: %v", gerr)
	}

	if len(got.Events) != 0 {
		t.Fatalf("GetLogEvents after rejected batches = %d events, want 0: %+v", len(got.Events), got.Events)
	}

	// A well-formed batch (chronological, within 24h) still succeeds.
	if _, err := client.PutLogEvents(ctx, &cwl.PutLogEventsInput{
		LogGroupName:  aws.String("/pv/g"),
		LogStreamName: aws.String("s1"),
		LogEvents: []cwltypes.InputLogEvent{
			{Timestamp: aws.Int64(base.UnixMilli()), Message: aws.String("ok1")},
			{Timestamp: aws.Int64(base.Add(time.Second).UnixMilli()), Message: aws.String("ok2")},
		},
	}); err != nil {
		t.Fatalf("well-formed PutLogEvents: %v", err)
	}
}
