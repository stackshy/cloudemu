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
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
)

// newLogsClientWithKey returns a CloudWatch Logs client plus the ARN of an
// enabled KMS key and a disabled one, both created in the same provider.
func newLogsClientWithKey(t *testing.T) (client *cwl.Client, enabledKey, disabledKey string) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ctx := context.Background()

	enabled, err := cloud.KMS.CreateKey(ctx, kmsdriver.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	disabled, err := cloud.KMS.CreateKey(ctx, kmsdriver.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	if err := cloud.KMS.DisableKey(ctx, disabled.KeyID); err != nil {
		t.Fatalf("DisableKey: %v", err)
	}

	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{CloudWatchLogs: cloud.CloudWatchLogs}))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client = cwl.NewFromConfig(cfg, func(o *cwl.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	return client, enabled.ARN, disabled.ARN
}

func describeOne(t *testing.T, c *cwl.Client, name string) cwltypes.LogGroup {
	t.Helper()

	out, err := c.DescribeLogGroups(context.Background(), &cwl.DescribeLogGroupsInput{LogGroupNamePrefix: aws.String(name)})
	if err != nil {
		t.Fatalf("DescribeLogGroups: %v", err)
	}

	if len(out.LogGroups) != 1 {
		t.Fatalf("DescribeLogGroups(%q) = %d groups, want 1", name, len(out.LogGroups))
	}

	return out.LogGroups[0]
}

func TestSDKDeleteRetentionPolicy(t *testing.T) {
	c := newLogsClient(t)
	ctx := context.Background()
	g := aws.String("/ret/del")

	if _, err := c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: g}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	if _, err := c.PutRetentionPolicy(ctx, &cwl.PutRetentionPolicyInput{LogGroupName: g, RetentionInDays: aws.Int32(7)}); err != nil {
		t.Fatalf("PutRetentionPolicy: %v", err)
	}

	if _, err := c.DeleteRetentionPolicy(ctx, &cwl.DeleteRetentionPolicyInput{LogGroupName: g}); err != nil {
		t.Fatalf("DeleteRetentionPolicy: %v", err)
	}

	if got := describeOne(t, c, "/ret/del"); got.RetentionInDays != nil {
		t.Fatalf("RetentionInDays = %d after DeleteRetentionPolicy, want unset", aws.ToInt32(got.RetentionInDays))
	}

	var nf *cwltypes.ResourceNotFoundException

	_, err := c.DeleteRetentionPolicy(ctx, &cwl.DeleteRetentionPolicyInput{LogGroupName: aws.String("/ret/missing")})
	if !errors.As(err, &nf) {
		t.Fatalf("DeleteRetentionPolicy on missing group: got %v, want ResourceNotFoundException", err)
	}

	var invalid *cwltypes.InvalidParameterException

	_, err = c.PutRetentionPolicy(ctx, &cwl.PutRetentionPolicyInput{LogGroupName: g, RetentionInDays: aws.Int32(2)})
	if !errors.As(err, &invalid) {
		t.Fatalf("PutRetentionPolicy(2): got %v, want InvalidParameterException", err)
	}
}

func TestSDKListLogGroups(t *testing.T) {
	c := newLogsClient(t)
	ctx := context.Background()

	for _, in := range []*cwl.CreateLogGroupInput{
		{LogGroupName: aws.String("/aws/lambda/fn")},
		{LogGroupName: aws.String("/app/api"), LogGroupClass: cwltypes.LogGroupClassInfrequentAccess},
		{LogGroupName: aws.String("/app/web")},
	} {
		if _, err := c.CreateLogGroup(ctx, in); err != nil {
			t.Fatalf("CreateLogGroup: %v", err)
		}
	}

	all, err := c.ListLogGroups(ctx, &cwl.ListLogGroupsInput{Limit: aws.Int32(2)})
	if err != nil {
		t.Fatalf("ListLogGroups: %v", err)
	}

	if len(all.LogGroups) != 2 || all.NextToken == nil {
		t.Fatalf("first page = %d groups, token %v; want 2 and a token", len(all.LogGroups), all.NextToken)
	}

	first := all.LogGroups[0]
	if aws.ToString(first.LogGroupName) != "/app/api" || first.LogGroupClass != cwltypes.LogGroupClassInfrequentAccess ||
		strings.HasSuffix(aws.ToString(first.LogGroupArn), ":*") || !strings.Contains(aws.ToString(first.LogGroupArn), ":log-group:/app/api") {
		t.Fatalf("first summary = %+v", first)
	}

	next, err := c.ListLogGroups(ctx, &cwl.ListLogGroupsInput{Limit: aws.Int32(2), NextToken: all.NextToken})
	if err != nil {
		t.Fatalf("ListLogGroups page 2: %v", err)
	}

	if len(next.LogGroups) != 1 || next.NextToken != nil {
		t.Fatalf("second page = %+v", next)
	}

	byPattern, err := c.ListLogGroups(ctx, &cwl.ListLogGroupsInput{LogGroupNamePattern: aws.String("^/aws")})
	if err != nil {
		t.Fatalf("ListLogGroups pattern: %v", err)
	}

	if len(byPattern.LogGroups) != 1 || aws.ToString(byPattern.LogGroups[0].LogGroupName) != "/aws/lambda/fn" {
		t.Fatalf("pattern ^/aws = %+v", byPattern.LogGroups)
	}

	var invalid *cwltypes.InvalidParameterException

	if _, err := c.ListLogGroups(ctx, &cwl.ListLogGroupsInput{LogGroupNamePattern: aws.String("ab")}); !errors.As(err, &invalid) {
		t.Fatalf("too-short pattern: got %v, want InvalidParameterException", err)
	}
}

func TestSDKPutLogEventsIngestWindow(t *testing.T) {
	c := newLogsClient(t)
	ctx := context.Background()
	g, s := aws.String("/win/g"), aws.String("s")

	if _, err := c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: g}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	if _, err := c.CreateLogStream(ctx, &cwl.CreateLogStreamInput{LogGroupName: g, LogStreamName: s}); err != nil {
		t.Fatalf("CreateLogStream: %v", err)
	}

	now := time.Now()
	ms := func(d time.Duration) *int64 { return aws.Int64(now.Add(d).UnixMilli()) }

	out, err := c.PutLogEvents(ctx, &cwl.PutLogEventsInput{LogGroupName: g, LogStreamName: s, LogEvents: []cwltypes.InputLogEvent{
		{Timestamp: ms(-20 * 24 * time.Hour), Message: aws.String("too old")},
		{Timestamp: ms(0), Message: aws.String("kept")},
		{Timestamp: ms(3 * time.Hour), Message: aws.String("too new")},
	}})
	if err != nil {
		t.Fatalf("PutLogEvents: %v", err)
	}

	rej := out.RejectedLogEventsInfo
	if rej == nil || aws.ToInt32(rej.TooOldLogEventEndIndex) != 1 || aws.ToInt32(rej.TooNewLogEventStartIndex) != 2 ||
		rej.ExpiredLogEventEndIndex != nil {
		t.Fatalf("RejectedLogEventsInfo = %+v, want tooOld=1 tooNew=2", rej)
	}

	second, err := c.PutLogEvents(ctx, &cwl.PutLogEventsInput{LogGroupName: g, LogStreamName: s, LogEvents: []cwltypes.InputLogEvent{
		{Timestamp: ms(time.Second), Message: aws.String("kept too")},
	}})
	if err != nil {
		t.Fatalf("PutLogEvents: %v", err)
	}

	if second.RejectedLogEventsInfo != nil {
		t.Fatalf("clean batch RejectedLogEventsInfo = %+v, want nil", second.RejectedLogEventsInfo)
	}

	tok1, tok2 := aws.ToString(out.NextSequenceToken), aws.ToString(second.NextSequenceToken)
	if len(tok1) != 56 || tok1 == tok2 || tok1 == "0" {
		t.Fatalf("sequence tokens %q, %q: want distinct 56-digit tokens", tok1, tok2)
	}

	got, err := c.GetLogEvents(ctx, &cwl.GetLogEventsInput{LogGroupName: g, LogStreamName: s, StartFromHead: aws.Bool(true)})
	if err != nil {
		t.Fatalf("GetLogEvents: %v", err)
	}

	if len(got.Events) != 2 || aws.ToString(got.Events[0].Message) != "kept" {
		t.Fatalf("stored events = %+v, want only the accepted two", got.Events)
	}
}

// TestSDKRetentionReadsAgree is the regression for GetLogEvents hiding expired
// events while FilterLogEvents still returned them.
func TestSDKRetentionReadsAgree(t *testing.T) {
	c := newLogsClient(t)
	ctx := context.Background()
	g, s := aws.String("/win/ret"), aws.String("s")

	if _, err := c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: g}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	if _, err := c.CreateLogStream(ctx, &cwl.CreateLogStreamInput{LogGroupName: g, LogStreamName: s}); err != nil {
		t.Fatalf("CreateLogStream: %v", err)
	}

	now := time.Now()

	for _, d := range []time.Duration{-3 * 24 * time.Hour, 0} {
		if _, err := c.PutLogEvents(ctx, &cwl.PutLogEventsInput{LogGroupName: g, LogStreamName: s, LogEvents: []cwltypes.InputLogEvent{
			{Timestamp: aws.Int64(now.Add(d).UnixMilli()), Message: aws.String("m")},
		}}); err != nil {
			t.Fatalf("PutLogEvents: %v", err)
		}
	}

	if _, err := c.PutRetentionPolicy(ctx, &cwl.PutRetentionPolicyInput{LogGroupName: g, RetentionInDays: aws.Int32(1)}); err != nil {
		t.Fatalf("PutRetentionPolicy: %v", err)
	}

	get, err := c.GetLogEvents(ctx, &cwl.GetLogEventsInput{LogGroupName: g, LogStreamName: s})
	if err != nil {
		t.Fatalf("GetLogEvents: %v", err)
	}

	filter, err := c.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: g})
	if err != nil {
		t.Fatalf("FilterLogEvents: %v", err)
	}

	if len(get.Events) != 1 || len(filter.Events) != 1 {
		t.Fatalf("after 1-day retention: Get=%d Filter=%d, want 1 and 1", len(get.Events), len(filter.Events))
	}

	if sb := aws.ToInt64(describeOne(t, c, "/win/ret").StoredBytes); sb != 1 {
		t.Fatalf("storedBytes = %d, want 1 after the purge", sb)
	}
}

func TestSDKLogGroupKmsKeyAndClass(t *testing.T) {
	c, key, disabledKey := newLogsClientWithKey(t)
	ctx := context.Background()

	if _, err := c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{
		LogGroupName: aws.String("/kms/g"), KmsKeyId: aws.String(key), LogGroupClass: cwltypes.LogGroupClassInfrequentAccess,
	}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	got := describeOne(t, c, "/kms/g")
	if aws.ToString(got.KmsKeyId) != key || got.LogGroupClass != cwltypes.LogGroupClassInfrequentAccess {
		t.Fatalf("described group = kms %q class %q", aws.ToString(got.KmsKeyId), got.LogGroupClass)
	}

	if arn := aws.ToString(got.Arn); !strings.HasSuffix(arn, ":log-group:/kms/g:*") || aws.ToString(got.LogGroupArn)+":*" != arn {
		t.Fatalf("arn = %q, logGroupArn = %q", arn, aws.ToString(got.LogGroupArn))
	}

	if _, err := c.DisassociateKmsKey(ctx, &cwl.DisassociateKmsKeyInput{LogGroupName: aws.String("/kms/g")}); err != nil {
		t.Fatalf("DisassociateKmsKey: %v", err)
	}

	if k := describeOne(t, c, "/kms/g").KmsKeyId; k != nil {
		t.Fatalf("kmsKeyId = %q after disassociate, want unset", aws.ToString(k))
	}

	if _, err := c.AssociateKmsKey(ctx, &cwl.AssociateKmsKeyInput{
		ResourceIdentifier: got.LogGroupArn, KmsKeyId: aws.String(key),
	}); err != nil {
		t.Fatalf("AssociateKmsKey by ARN: %v", err)
	}

	if aws.ToString(describeOne(t, c, "/kms/g").KmsKeyId) != key {
		t.Fatal("kmsKeyId not set by AssociateKmsKey")
	}

	var invalid *cwltypes.InvalidParameterException

	if _, err := c.AssociateKmsKey(ctx, &cwl.AssociateKmsKeyInput{
		LogGroupName: aws.String("/kms/g"), KmsKeyId: aws.String(disabledKey),
	}); !errors.As(err, &invalid) {
		t.Fatalf("disabled key: got %v, want InvalidParameterException", err)
	}

	if _, err := c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{
		LogGroupName: aws.String("/kms/missing"), KmsKeyId: aws.String("arn:aws:kms:us-east-1:123456789012:key/nope"),
	}); !errors.As(err, &invalid) {
		t.Fatalf("missing key on create: got %v, want InvalidParameterException", err)
	}

	var nf *cwltypes.ResourceNotFoundException

	if _, err := c.AssociateKmsKey(ctx, &cwl.AssociateKmsKeyInput{
		LogGroupName: aws.String("/kms/none"), KmsKeyId: aws.String(key),
	}); !errors.As(err, &nf) {
		t.Fatalf("missing group: got %v, want ResourceNotFoundException", err)
	}

	if got := describeOne(t, c, "/kms/g"); got.LogGroupClass != cwltypes.LogGroupClassInfrequentAccess {
		t.Fatalf("class changed to %q", got.LogGroupClass)
	}
}
