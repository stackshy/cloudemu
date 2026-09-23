package redshift_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
	rstypes "github.com/aws/aws-sdk-go-v2/service/redshift/types"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newEventSubClients points Redshift and SNS clients at the full server, so
// RDS is registered ahead of Redshift as in production.
func newEventSubClients(t *testing.T) (*awsredshift.Client, *awssns.Client) {
	t.Helper()

	ts := httptest.NewServer(awsserver.NewFromProvider(cloudemu.NewAWS()))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	rs := awsredshift.NewFromConfig(cfg, func(o *awsredshift.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sn := awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	return rs, sn
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
}

func TestSDKRedshiftEventSubscriptionLifecycle(t *testing.T) {
	rs, sn := newEventSubClients(t)
	ctx := context.Background()

	topic, err := sn.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("rs-events")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	created, err := rs.CreateEventSubscription(ctx, &awsredshift.CreateEventSubscriptionInput{
		SubscriptionName: aws.String("sub-1"),
		SnsTopicArn:      topic.TopicArn,
		EventCategories:  []string{"management"},
		Tags:             []rstypes.Tag{{Key: aws.String("env"), Value: aws.String("dev")}},
	})
	if err != nil {
		t.Fatalf("CreateEventSubscription: %v", err)
	}

	sub := created.EventSubscription
	if aws.ToString(sub.CustSubscriptionId) != "sub-1" || aws.ToString(sub.Severity) != "INFO" ||
		!aws.ToBool(sub.Enabled) || aws.ToString(sub.Status) != "active" || len(sub.Tags) != 1 {
		t.Fatalf("created = %+v", sub)
	}

	_, err = rs.CreateEventSubscription(ctx, &awsredshift.CreateEventSubscriptionInput{
		SubscriptionName: aws.String("sub-1"), SnsTopicArn: topic.TopicArn,
	})
	wantCode(t, err, "SubscriptionAlreadyExist")

	modified, err := rs.ModifyEventSubscription(ctx, &awsredshift.ModifyEventSubscriptionInput{
		SubscriptionName: aws.String("sub-1"), Severity: aws.String("ERROR"), Enabled: aws.Bool(false),
	})
	if err != nil {
		t.Fatalf("ModifyEventSubscription: %v", err)
	}

	if aws.ToString(modified.EventSubscription.Severity) != "ERROR" || aws.ToBool(modified.EventSubscription.Enabled) ||
		len(modified.EventSubscription.EventCategoriesList) != 1 {
		t.Fatalf("modified = %+v", modified.EventSubscription)
	}

	listed, err := rs.DescribeEventSubscriptions(ctx, &awsredshift.DescribeEventSubscriptionsInput{
		TagKeys: []string{"env"},
	})
	if err != nil || len(listed.EventSubscriptionsList) != 1 {
		t.Fatalf("DescribeEventSubscriptions = %+v, err %v", listed, err)
	}

	if _, err := rs.DeleteEventSubscription(ctx, &awsredshift.DeleteEventSubscriptionInput{
		SubscriptionName: aws.String("sub-1"),
	}); err != nil {
		t.Fatalf("DeleteEventSubscription: %v", err)
	}

	_, err = rs.DescribeEventSubscriptions(ctx, &awsredshift.DescribeEventSubscriptionsInput{
		SubscriptionName: aws.String("sub-1"),
	})
	wantCode(t, err, "SubscriptionNotFound")

	_, err = rs.DeleteEventSubscription(ctx, &awsredshift.DeleteEventSubscriptionInput{SubscriptionName: aws.String("sub-1")})
	wantCode(t, err, "SubscriptionNotFound")
}

func TestSDKRedshiftEventSubscriptionErrors(t *testing.T) {
	rs, sn := newEventSubClients(t)
	ctx := context.Background()

	topic, err := sn.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("rs-errors")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	cases := []struct {
		name string
		in   *awsredshift.CreateEventSubscriptionInput
		code string
	}{
		{"missing topic", &awsredshift.CreateEventSubscriptionInput{
			SnsTopicArn: aws.String("arn:aws:sns:us-east-1:000000000000:nope"),
		}, "SNSTopicArnNotFound"},
		{"bad topic arn", &awsredshift.CreateEventSubscriptionInput{SnsTopicArn: aws.String("nope")}, "SNSInvalidTopic"},
		{"bad severity", &awsredshift.CreateEventSubscriptionInput{
			SnsTopicArn: topic.TopicArn, Severity: aws.String("WARN"),
		}, "SubscriptionSeverityNotFound"},
		{"bad category", &awsredshift.CreateEventSubscriptionInput{
			SnsTopicArn: topic.TopicArn, EventCategories: []string{"nope"},
		}, "SubscriptionCategoryNotFound"},
		{"missing source", &awsredshift.CreateEventSubscriptionInput{
			SnsTopicArn: topic.TopicArn, SourceType: aws.String("cluster"), SourceIds: []string{"nope"},
		}, "SourceNotFound"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.SubscriptionName = aws.String("sub-err")
			_, err := rs.CreateEventSubscription(ctx, tc.in)
			wantCode(t, err, tc.code)
		})
	}

	_, err = rs.ModifyEventSubscription(ctx, &awsredshift.ModifyEventSubscriptionInput{SubscriptionName: aws.String("none")})
	wantCode(t, err, "SubscriptionNotFound")
}

func TestSDKRedshiftDescribeEventCategories(t *testing.T) {
	rs, _ := newEventSubClients(t)

	out, err := rs.DescribeEventCategories(context.Background(), &awsredshift.DescribeEventCategoriesInput{
		SourceType: aws.String("cluster-parameter-group"),
	})
	if err != nil {
		t.Fatalf("DescribeEventCategories: %v", err)
	}

	if len(out.EventCategoriesMapList) != 1 || len(out.EventCategoriesMapList[0].Events) != 4 ||
		aws.ToString(out.EventCategoriesMapList[0].Events[0].EventId) != "REDSHIFT-EVENT-1002" {
		t.Fatalf("categories = %+v", out.EventCategoriesMapList)
	}
}
