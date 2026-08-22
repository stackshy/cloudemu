package aws

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const (
	logGroup      = "/app/api"
	logStream     = "instance-1"
	retentionDays = 14
)

// TestCloudWatchLogsCompat drives a real aws-sdk-go-v2 CloudWatch Logs client
// against CloudEmu's in-process wire server and records one compat result per
// portable "logging" op the handler routes.
func TestCloudWatchLogsCompat(t *testing.T) {
	cloud := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{CloudWatchLogs: cloud.CloudWatchLogs})

	cfg := sess.Config()
	endpoint := sess.Endpoint()
	client := cwl.NewFromConfig(cfg, func(o *cwl.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	ctx := context.Background()
	const service = "logging"

	sess.Op(service, "CreateLogGroup", func() error {
		_, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{
			LogGroupName: aws.String(logGroup),
			Tags:         map[string]string{"env": "test"},
		})
		return err
	})

	sess.Op(service, "ListLogGroups", func() error {
		_, err := client.DescribeLogGroups(ctx, &cwl.DescribeLogGroupsInput{})
		return err
	})

	sess.Op(service, "GetLogGroup", func() error {
		_, err := client.DescribeLogGroups(ctx, &cwl.DescribeLogGroupsInput{
			LogGroupNamePrefix: aws.String(logGroup),
		})
		return err
	})

	sess.Op(service, "UpdateLogGroup", func() error {
		_, err := client.PutRetentionPolicy(ctx, &cwl.PutRetentionPolicyInput{
			LogGroupName:    aws.String(logGroup),
			RetentionInDays: aws.Int32(retentionDays),
		})
		return err
	})

	sess.Op(service, "CreateLogStream", func() error {
		_, err := client.CreateLogStream(ctx, &cwl.CreateLogStreamInput{
			LogGroupName:  aws.String(logGroup),
			LogStreamName: aws.String(logStream),
		})
		return err
	})

	sess.Op(service, "ListLogStreams", func() error {
		_, err := client.DescribeLogStreams(ctx, &cwl.DescribeLogStreamsInput{
			LogGroupName: aws.String(logGroup),
		})
		return err
	})

	base := time.Now().UTC().Truncate(time.Millisecond)

	sess.Op(service, "PutLogEvents", func() error {
		_, err := client.PutLogEvents(ctx, &cwl.PutLogEventsInput{
			LogGroupName:  aws.String(logGroup),
			LogStreamName: aws.String(logStream),
			LogEvents: []cwltypes.InputLogEvent{
				{Timestamp: aws.Int64(base.UnixMilli()), Message: aws.String("hello world")},
				{Timestamp: aws.Int64(base.Add(time.Second).UnixMilli()), Message: aws.String("error: boom")},
			},
		})
		return err
	})

	sess.Op(service, "GetLogEvents", func() error {
		_, err := client.GetLogEvents(ctx, &cwl.GetLogEventsInput{
			LogGroupName:  aws.String(logGroup),
			LogStreamName: aws.String(logStream),
		})
		return err
	})

	sess.Op(service, "FilterLogEvents", func() error {
		_, err := client.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{
			LogGroupName:  aws.String(logGroup),
			FilterPattern: aws.String("error"),
		})
		return err
	})

	sess.Op(service, "DeleteLogStream", func() error {
		_, err := client.DeleteLogStream(ctx, &cwl.DeleteLogStreamInput{
			LogGroupName:  aws.String(logGroup),
			LogStreamName: aws.String(logStream),
		})
		return err
	})

	sess.Op(service, "DeleteLogGroup", func() error {
		_, err := client.DeleteLogGroup(ctx, &cwl.DeleteLogGroupInput{
			LogGroupName: aws.String(logGroup),
		})
		return err
	})
}
