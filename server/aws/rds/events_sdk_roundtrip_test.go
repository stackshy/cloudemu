package rds_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
)

func TestSDKRDSEventSubscriptionLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateEventSubscription(ctx, &awsrds.CreateEventSubscriptionInput{
		SubscriptionName: aws.String("sub"),
		SnsTopicArn:      aws.String("arn:aws:sns:us-east-1:123456789012:rds-events"),
		SourceType:       aws.String("db-instance"),
		EventCategories:  []string{"failure", "failover"},
		SourceIds:        []string{"mydb"},
	})
	if err != nil {
		t.Fatalf("CreateEventSubscription: %v", err)
	}

	if aws.ToString(created.EventSubscription.EventSubscriptionArn) == "" {
		t.Error("expected subscription ARN")
	}

	if !aws.ToBool(created.EventSubscription.Enabled) {
		t.Error("Enabled should default to true")
	}

	if len(created.EventSubscription.EventCategoriesList) != 2 {
		t.Fatalf("event categories = %v, want 2", created.EventSubscription.EventCategoriesList)
	}

	desc, err := client.DescribeEventSubscriptions(ctx, &awsrds.DescribeEventSubscriptionsInput{})
	if err != nil {
		t.Fatalf("DescribeEventSubscriptions: %v", err)
	}

	if len(desc.EventSubscriptionsList) != 1 {
		t.Fatalf("got %d subscriptions, want 1", len(desc.EventSubscriptionsList))
	}

	disabled := false
	if _, err := client.ModifyEventSubscription(ctx, &awsrds.ModifyEventSubscriptionInput{
		SubscriptionName: aws.String("sub"),
		Enabled:          &disabled,
	}); err != nil {
		t.Fatalf("ModifyEventSubscription: %v", err)
	}

	cats, err := client.DescribeEventCategories(ctx, &awsrds.DescribeEventCategoriesInput{
		SourceType: aws.String("db-instance"),
	})
	if err != nil {
		t.Fatalf("DescribeEventCategories: %v", err)
	}

	if len(cats.EventCategoriesMapList) != 1 || len(cats.EventCategoriesMapList[0].EventCategories) == 0 {
		t.Fatalf("unexpected event categories: %+v", cats.EventCategoriesMapList)
	}

	events, err := client.DescribeEvents(ctx, &awsrds.DescribeEventsInput{
		SourceType: "db-instance",
	})
	if err != nil {
		t.Fatalf("DescribeEvents: %v", err)
	}

	if len(events.Events) != 0 {
		t.Fatalf("got %d events, want 0", len(events.Events))
	}

	if _, err := client.DeleteEventSubscription(ctx, &awsrds.DeleteEventSubscriptionInput{
		SubscriptionName: aws.String("sub"),
	}); err != nil {
		t.Fatalf("DeleteEventSubscription: %v", err)
	}
}
