package aws_test

import (
	"context"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// sqsGaugeStat reads one statistic of an AWS/SQS metric for a queue over the
// last few minutes through the real CloudWatch client.
func sqsGaugeStat(
	t *testing.T, cw *cloudwatch.Client, metric, queue string, stat cwtypes.Statistic,
) (value float64, unit cwtypes.StandardUnit, found bool) {
	t.Helper()

	now := time.Now().UTC()

	out, err := cw.GetMetricStatistics(context.Background(), &cloudwatch.GetMetricStatisticsInput{
		Namespace:  aws.String("AWS/SQS"),
		MetricName: aws.String(metric),
		Dimensions: []cwtypes.Dimension{{Name: aws.String("QueueName"), Value: aws.String(queue)}},
		StartTime:  aws.Time(now.Add(-5 * time.Minute)),
		EndTime:    aws.Time(now.Add(5 * time.Minute)),
		Period:     aws.Int32(600),
		Statistics: []cwtypes.Statistic{stat},
	})
	require.NoError(t, err)

	if len(out.Datapoints) == 0 {
		return 0, "", false
	}

	dp := out.Datapoints[0]

	switch stat {
	case cwtypes.StatisticMaximum:
		return aws.ToFloat64(dp.Maximum), dp.Unit, true
	case cwtypes.StatisticMinimum:
		return aws.ToFloat64(dp.Minimum), dp.Unit, true
	case cwtypes.StatisticSum:
		return aws.ToFloat64(dp.Sum), dp.Unit, true
	default:
		return aws.ToFloat64(dp.SampleCount), dp.Unit, true
	}
}

// sqsMetricNames lists the AWS/SQS metric names CloudWatch knows.
func sqsMetricNames(t *testing.T, cw *cloudwatch.Client) []string {
	t.Helper()

	metrics, err := cw.ListMetrics(context.Background(), &cloudwatch.ListMetricsInput{Namespace: aws.String("AWS/SQS")})
	require.NoError(t, err)

	names := make([]string, 0, len(metrics.Metrics))
	for _, m := range metrics.Metrics {
		names = append(names, aws.ToString(m.MetricName))
	}

	return names
}

// tickProvider runs one background tick of every time-driven service, the
// way the serve scheduler does.
func tickProvider(p interface{ Tickables() []config.Tickable }) {
	now := time.Now()
	for _, t := range p.Tickables() {
		t.Tick(now)
	}
}

// TestSDKSQSQueueGaugesReachCloudWatch drives SQS with the real SDK and reads
// the queue-state gauges back through CloudWatch, the way a dashboard or an
// alarm on ApproximateNumberOfMessagesVisible would. The gauges come from the
// background tick, not from the requests.
func TestSDKSQSQueueGaugesReachCloudWatch(t *testing.T) {
	provider := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{CloudWatch: provider.CloudWatch, SQS: provider.SQS}))
	defer ts.Close()

	cfg := sdkConfig(t, ts.URL)
	ctx := context.Background()
	sqsClient := sqs.NewFromConfig(cfg)
	cw := cloudwatch.NewFromConfig(cfg)

	q, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("gauge-q")})
	require.NoError(t, err)

	_, err = sqsClient.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String("now")})
	require.NoError(t, err)

	_, err = sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl: q.QueueUrl, MessageBody: aws.String("later"), DelaySeconds: 600,
	})
	require.NoError(t, err)

	recv, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{QueueUrl: q.QueueUrl, VisibilityTimeout: 300})
	require.NoError(t, err)
	require.Len(t, recv.Messages, 1)

	// Requests publish the counters only.
	require.False(t, slices.Contains(sqsMetricNames(t, cw), "ApproximateNumberOfMessagesVisible"))

	tickProvider(provider)

	names := sqsMetricNames(t, cw)
	for _, want := range []string{
		"ApproximateNumberOfMessagesVisible", "ApproximateNumberOfMessagesNotVisible",
		"ApproximateNumberOfMessagesDelayed", "ApproximateAgeOfOldestMessage",
	} {
		require.True(t, slices.Contains(names, want), "ListMetrics AWS/SQS missing %s (got %v)", want, names)
	}

	// One sample: nothing visible, the received message in flight, the
	// delayed one delayed.
	visible, unit, ok := sqsGaugeStat(t, cw, "ApproximateNumberOfMessagesVisible", "gauge-q", cwtypes.StatisticMaximum)
	require.True(t, ok)
	require.InDelta(t, 0, visible, 0)
	require.Equal(t, cwtypes.StandardUnitCount, unit)

	samples, _, _ := sqsGaugeStat(t, cw, "ApproximateNumberOfMessagesVisible", "gauge-q", cwtypes.StatisticSampleCount)
	require.InDelta(t, 1, samples, 0)

	delayed, _, _ := sqsGaugeStat(t, cw, "ApproximateNumberOfMessagesDelayed", "gauge-q", cwtypes.StatisticMaximum)
	require.InDelta(t, 1, delayed, 0)

	inFlight, _, _ := sqsGaugeStat(t, cw, "ApproximateNumberOfMessagesNotVisible", "gauge-q", cwtypes.StatisticMaximum)
	require.InDelta(t, 1, inFlight, 0)

	_, ageUnit, ok := sqsGaugeStat(t, cw, "ApproximateAgeOfOldestMessage", "gauge-q", cwtypes.StatisticMaximum)
	require.True(t, ok)
	require.Equal(t, cwtypes.StandardUnitSeconds, ageUnit)

	// GetQueueAttributes agrees: the delayed message is not counted in flight.
	attrs, err := sqsClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       q.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
	})
	require.NoError(t, err)
	require.Equal(t, "1", attrs.Attributes["ApproximateNumberOfMessagesNotVisible"])
	require.Equal(t, "1", attrs.Attributes["ApproximateNumberOfMessagesDelayed"])
	require.Equal(t, "0", attrs.Attributes["ApproximateNumberOfMessages"])
}

// TestSDKSQSGaugeSumIsOneSamplePerMinute is the Sum-statistic case: six sends
// in a minute give one Visible datapoint of 6, so Sum is 6, not a running
// total of one sample per send.
func TestSDKSQSGaugeSumIsOneSamplePerMinute(t *testing.T) {
	provider := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{CloudWatch: provider.CloudWatch, SQS: provider.SQS}))
	defer ts.Close()

	cfg := sdkConfig(t, ts.URL)
	ctx := context.Background()
	sqsClient := sqs.NewFromConfig(cfg)
	cw := cloudwatch.NewFromConfig(cfg)

	q, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("sum-q")})
	require.NoError(t, err)

	for i := 0; i < 6; i++ {
		_, err = sqsClient.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String("m")})
		require.NoError(t, err)
	}

	tickProvider(provider)
	tickProvider(provider) // a second tick inside the minute publishes nothing

	sum, _, _ := sqsGaugeStat(t, cw, "ApproximateNumberOfMessagesVisible", "sum-q", cwtypes.StatisticSum)
	require.InDelta(t, 6, sum, 0)

	samples, _, _ := sqsGaugeStat(t, cw, "ApproximateNumberOfMessagesVisible", "sum-q", cwtypes.StatisticSampleCount)
	require.InDelta(t, 1, samples, 0)
}

// receiveSenderID sends body with client and returns the SenderId the queue
// reports for it.
func receiveSenderID(t *testing.T, client *sqs.Client, queueURL *string, body string) string {
	t.Helper()

	ctx := context.Background()

	_, err := client.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: queueURL, MessageBody: aws.String(body)})
	require.NoError(t, err)

	out, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:                    queueURL,
		MessageSystemAttributeNames: []sqstypes.MessageSystemAttributeName{sqstypes.MessageSystemAttributeNameSenderId},
	})
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	require.Equal(t, body, aws.ToString(out.Messages[0].Body))

	_, err = client.DeleteMessage(ctx, &sqs.DeleteMessageInput{QueueUrl: queueURL, ReceiptHandle: out.Messages[0].ReceiptHandle})
	require.NoError(t, err)

	return out.Messages[0].Attributes[string(sqstypes.MessageSystemAttributeNameSenderId)]
}

// TestSDKSQSSenderIDMatchesCallerIdentity checks that SenderId is the
// caller's IAM unique id, the same UserId STS GetCallerIdentity reports: the
// user id for long-term keys, "<role id>:<session>" for an assumed role.
func TestSDKSQSSenderIDMatchesCallerIdentity(t *testing.T) {
	provider := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.NewFromProvider(provider))
	defer ts.Close()

	cfg := sdkConfig(t, ts.URL)
	ctx := context.Background()
	sqsClient := sqs.NewFromConfig(cfg)

	q, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("sender-q")})
	require.NoError(t, err)

	who, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	require.NoError(t, err)
	require.NotEqual(t, aws.ToString(who.Account), aws.ToString(who.UserId))
	require.Equal(t, aws.ToString(who.UserId), receiveSenderID(t, sqsClient, q.QueueUrl, "as-user"))

	// An assumed role's messages carry "<role id>:<session name>".
	role, err := iam.NewFromConfig(cfg).CreateRole(ctx, &iam.CreateRoleInput{
		RoleName: aws.String("sender-role"),
		AssumeRolePolicyDocument: aws.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow",` +
			`"Principal":{"AWS":"*"},"Action":"sts:AssumeRole"}]}`),
	})
	require.NoError(t, err)

	assumed, err := sts.NewFromConfig(cfg).AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         aws.String("arn:aws:iam::" + aws.ToString(who.Account) + ":role/sender-role"),
		RoleSessionName: aws.String("worker-1"),
	})
	require.NoError(t, err)

	roleCfg := cfg.Copy()
	roleCfg.Credentials = credentials.NewStaticCredentialsProvider(
		aws.ToString(assumed.Credentials.AccessKeyId),
		aws.ToString(assumed.Credentials.SecretAccessKey),
		aws.ToString(assumed.Credentials.SessionToken),
	)

	// AssumedRoleId is the role's own RoleId plus the session name.
	roleID := aws.ToString(assumed.AssumedRoleUser.AssumedRoleId)
	require.Equal(t, aws.ToString(role.Role.RoleId)+":worker-1", roleID)
	require.Equal(t, roleID, receiveSenderID(t, sqs.NewFromConfig(roleCfg), q.QueueUrl, "as-role"))
}
