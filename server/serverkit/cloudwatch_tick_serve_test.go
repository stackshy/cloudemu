package serverkit

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// TestServeTickFiresCloudWatchAlarmAction checks serve evaluates due alarms
// on its own. The breaching datum is dated in the near future, so creating
// the alarm leaves it in INSUFFICIENT_DATA. Only a later evaluation can move
// it to ALARM. The test never calls a CloudWatch read, so the SNS
// notification can only come from the background tick.
func TestServeTickFiresCloudWatchAlarmAction(t *testing.T) {
	app := newTestApp(t, Config{
		Providers:    []string{"aws"},
		Host:         "127.0.0.1",
		Ports:        map[string]string{"aws": "0"},
		TickInterval: 100 * time.Millisecond,
		Out:          io.Discard,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- app.Serve(ctx) }()

	t.Cleanup(func() {
		cancel()

		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})

	ts := httptest.NewServer(app.handlerFor(app.backends["aws"], app.seedFor("aws")))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	queueURL, topicARN := subscribeQueue(t, sns.NewFromConfig(cfg), sqs.NewFromConfig(cfg))

	cw := cloudwatch.NewFromConfig(cfg)
	bg := context.Background()

	if _, err := cw.PutMetricData(bg, &cloudwatch.PutMetricDataInput{
		Namespace: aws.String("Custom/Tick"),
		MetricData: []cwtypes.MetricDatum{{
			MetricName: aws.String("Errors"),
			Value:      aws.Float64(99),
			Timestamp:  aws.Time(time.Now().Add(3 * time.Second)),
		}},
	}); err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}

	if _, err := cw.PutMetricAlarm(bg, &cloudwatch.PutMetricAlarmInput{
		AlarmName:          aws.String("tick-alarm"),
		Namespace:          aws.String("Custom/Tick"),
		MetricName:         aws.String("Errors"),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		Threshold:          aws.Float64(10),
		EvaluationPeriods:  aws.Int32(1),
		Period:             aws.Int32(10),
		Statistic:          cwtypes.StatisticMaximum,
		AlarmActions:       []string{topicARN},
	}); err != nil {
		t.Fatalf("PutMetricAlarm: %v", err)
	}

	waitAlarmNotice(t, sqs.NewFromConfig(cfg), queueURL, "tick-alarm")
}

// subscribeQueue creates a topic and a queue subscribed to it.
func subscribeQueue(t *testing.T, snsClient *sns.Client, sqsClient *sqs.Client) (queueURL, topicARN string) {
	t.Helper()

	bg := context.Background()

	topic, err := snsClient.CreateTopic(bg, &sns.CreateTopicInput{Name: aws.String("tick-topic")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	queue, err := sqsClient.CreateQueue(bg, &sqs.CreateQueueInput{QueueName: aws.String("tick-queue")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	attrs, err := sqsClient.GetQueueAttributes(bg, &sqs.GetQueueAttributesInput{
		QueueUrl:       queue.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}

	if _, err := snsClient.Subscribe(bg, &sns.SubscribeInput{
		TopicArn: topic.TopicArn,
		Protocol: aws.String("sqs"),
		Endpoint: aws.String(attrs.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]),
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	return aws.ToString(queue.QueueUrl), aws.ToString(topic.TopicArn)
}

// waitAlarmNotice polls the queue until the alarm's ALARM notification lands.
func waitAlarmNotice(t *testing.T, sqsClient *sqs.Client, queueURL, alarm string) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for time.Now().Before(deadline) {
		out, err := sqsClient.ReceiveMessage(context.Background(), &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(queueURL),
			MaxNumberOfMessages: 10,
		})
		if err != nil {
			t.Fatalf("ReceiveMessage: %v", err)
		}

		for _, m := range out.Messages {
			body := aws.ToString(m.Body)
			if strings.Contains(body, alarm) && strings.Contains(body, "ALARM") {
				return
			}
		}

		time.Sleep(200 * time.Millisecond)
	}

	t.Fatalf("no ALARM notification for %s reached the queue", alarm)
}
