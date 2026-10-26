package cloudwatchlogs_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const sdkExportPolicy = `{"Version":"2012-10-17","Statement":[
 {"Effect":"Allow","Principal":{"Service":"logs.us-east-1.amazonaws.com"},"Action":"s3:GetBucketAcl","Resource":"arn:aws:s3:::sdk-exp"},
 {"Effect":"Allow","Principal":{"Service":"logs.us-east-1.amazonaws.com"},"Action":"s3:PutObject","Resource":"arn:aws:s3:::sdk-exp/*"}]}`

func newLogsS3Clients(t *testing.T) (*cwl.Client, *awss3.Client) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{CloudWatchLogs: cloud.CloudWatchLogs, S3: cloud.S3}))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return cwl.NewFromConfig(cfg), awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.UsePathStyle = true })
}

func TestSDKExportTaskToS3(t *testing.T) {
	logs, s3c := newLogsS3Clients(t)
	ctx := context.Background()
	g := aws.String("/export/g")

	mustSDK(t, "CreateBucket", func() error {
		_, err := s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("sdk-exp")})
		return err
	})

	mustSDK(t, "CreateLogGroup", func() error {
		_, err := logs.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: g})
		return err
	})

	mustSDK(t, "CreateLogStream", func() error {
		_, err := logs.CreateLogStream(ctx, &cwl.CreateLogStreamInput{LogGroupName: g, LogStreamName: aws.String("web/1")})
		return err
	})

	now := time.Now()
	mustSDK(t, "PutLogEvents", func() error {
		_, err := logs.PutLogEvents(ctx, &cwl.PutLogEventsInput{
			LogGroupName: g, LogStreamName: aws.String("web/1"),
			LogEvents: []cwltypes.InputLogEvent{{Timestamp: aws.Int64(now.UnixMilli()), Message: aws.String("hello export")}},
		})

		return err
	})

	in := &cwl.CreateExportTaskInput{
		LogGroupName: g, Destination: aws.String("sdk-exp"), DestinationPrefix: aws.String("pfx"),
		From: aws.Int64(now.Add(-time.Hour).UnixMilli()), To: aws.Int64(now.Add(time.Hour).UnixMilli()),
		TaskName: aws.String("sdk-task"),
	}

	var ipe *cwltypes.InvalidParameterException
	if _, err := logs.CreateExportTask(ctx, in); !errors.As(err, &ipe) || !strings.Contains(err.Error(), "GetBucketAcl") {
		t.Fatalf("CreateExportTask without a bucket policy = %v, want InvalidParameterException", err)
	}

	mustSDK(t, "PutBucketPolicy", func() error {
		_, err := s3c.PutBucketPolicy(ctx, &awss3.PutBucketPolicyInput{Bucket: aws.String("sdk-exp"), Policy: aws.String(sdkExportPolicy)})
		return err
	})

	created, err := logs.CreateExportTask(ctx, in)
	if err != nil {
		t.Fatalf("CreateExportTask: %v", err)
	}

	out, err := logs.DescribeExportTasks(ctx, &cwl.DescribeExportTasksInput{TaskId: created.TaskId})
	if err != nil || len(out.ExportTasks) != 1 {
		t.Fatalf("DescribeExportTasks = %+v, %v", out, err)
	}

	task := out.ExportTasks[0]
	if task.Status.Code != cwltypes.ExportTaskStatusCodeCompleted || aws.ToString(task.TaskName) != "sdk-task" ||
		aws.ToString(task.DestinationPrefix) != "pfx" || task.ExecutionInfo.CompletionTime == nil {
		t.Fatalf("task = %+v status %+v", task, task.Status)
	}

	obj, err := s3c.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String("sdk-exp"), Key: aws.String("pfx/" + aws.ToString(created.TaskId) + "/web/1/000000.gz"),
	})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}

	body, _ := io.ReadAll(obj.Body)
	_ = obj.Body.Close()

	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}

	text, _ := io.ReadAll(zr)
	want := now.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z") + " hello export\n"

	if string(text) != want {
		t.Fatalf("export body = %q, want %q", text, want)
	}

	var ioe *cwltypes.InvalidOperationException
	if _, err := logs.CancelExportTask(ctx, &cwl.CancelExportTaskInput{TaskId: created.TaskId}); !errors.As(err, &ioe) {
		t.Fatalf("CancelExportTask on a completed task = %v, want InvalidOperationException", err)
	}

	var rnf *cwltypes.ResourceNotFoundException
	if _, err := logs.CancelExportTask(ctx, &cwl.CancelExportTaskInput{TaskId: aws.String("nope")}); !errors.As(err, &rnf) {
		t.Fatalf("CancelExportTask unknown = %v, want ResourceNotFoundException", err)
	}
}

func TestSDKDescribeExportTasksPaging(t *testing.T) {
	logs, s3c := newLogsS3Clients(t)
	ctx := context.Background()
	g := aws.String("g")

	mustSDK(t, "CreateBucket", func() error {
		_, err := s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("sdk-exp")})
		return err
	})
	mustSDK(t, "PutBucketPolicy", func() error {
		_, err := s3c.PutBucketPolicy(ctx, &awss3.PutBucketPolicyInput{Bucket: aws.String("sdk-exp"), Policy: aws.String(sdkExportPolicy)})
		return err
	})
	mustSDK(t, "CreateLogGroup", func() error {
		_, err := logs.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: g})
		return err
	})

	now := time.Now()

	for range 3 {
		mustSDK(t, "CreateExportTask", func() error {
			_, err := logs.CreateExportTask(ctx, &cwl.CreateExportTaskInput{
				LogGroupName: g, Destination: aws.String("sdk-exp"),
				From: aws.Int64(now.Add(-time.Hour).UnixMilli()), To: aws.Int64(now.Add(time.Hour).UnixMilli()),
			})

			return err
		})
	}

	seen, pages := 0, 0

	var token *string

	for {
		page, err := logs.DescribeExportTasks(ctx, &cwl.DescribeExportTasksInput{Limit: aws.Int32(2), NextToken: token})
		if err != nil {
			t.Fatalf("DescribeExportTasks page: %v", err)
		}

		seen += len(page.ExportTasks)
		pages++

		if page.NextToken == nil {
			break
		}

		token = page.NextToken
	}

	if seen != 3 || pages != 2 {
		t.Fatalf("paged %d tasks over %d pages, want 3 over 2", seen, pages)
	}

	var ipe *cwltypes.InvalidParameterException
	if _, err := logs.DescribeExportTasks(ctx, &cwl.DescribeExportTasksInput{Limit: aws.Int32(51)}); !errors.As(err, &ipe) {
		t.Fatalf("limit 51 = %v, want InvalidParameterException", err)
	}
}

func mustSDK(t *testing.T, op string, fn func() error) {
	t.Helper()

	if err := fn(); err != nil {
		t.Fatalf("%s: %v", op, err)
	}
}
