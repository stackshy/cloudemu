package cloudwatchlogs_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	smithy "github.com/aws/smithy-go"
)

func logsErrorCode(t *testing.T, err error) string {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want an API error, got %v", err)
	}

	return apiErr.ErrorCode()
}

func TestSDKLogsRejectsBadNamesAndBatches(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	const want = "InvalidParameterException"

	_, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String("bad name")})
	if code := logsErrorCode(t, err); code != want {
		t.Errorf("CreateLogGroup(bad name): code = %q, want %s", code, want)
	}

	_, err = client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String(strings.Repeat("a", 520))})
	if code := logsErrorCode(t, err); code != want {
		t.Errorf("CreateLogGroup(520 chars): code = %q, want %s", code, want)
	}

	mustLogGroupStream(t, client, "/app/valid", "stream")

	_, err = client.CreateLogStream(ctx, &cwl.CreateLogStreamInput{
		LogGroupName: aws.String("/app/valid"), LogStreamName: aws.String("a:b"),
	})
	if code := logsErrorCode(t, err); code != want {
		t.Errorf("CreateLogStream(a:b): code = %q, want %s", code, want)
	}

	now := aws.Int64(time.Now().UnixMilli())
	events := make([]cwltypes.InputLogEvent, 10001)

	for i := range events {
		events[i] = cwltypes.InputLogEvent{Timestamp: now, Message: aws.String("m")}
	}

	_, err = client.PutLogEvents(ctx, &cwl.PutLogEventsInput{
		LogGroupName: aws.String("/app/valid"), LogStreamName: aws.String("stream"), LogEvents: events,
	})
	if code := logsErrorCode(t, err); code != want {
		t.Errorf("PutLogEvents(10001 events): code = %q, want %s", code, want)
	}
}
