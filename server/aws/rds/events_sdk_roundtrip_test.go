package rds_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	smithy "github.com/aws/smithy-go"
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

// TestSDKRDSEventSubscriptionFaultCodes: a missing or duplicate subscription
// answers with the RDS wire codes SubscriptionNotFound and
// SubscriptionAlreadyExist, so the SDK returns its typed faults. Terraform's
// aws_db_event_subscription delete waiter relies on the typed NotFound.
func TestSDKRDSEventSubscriptionFaultCodes(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.DeleteEventSubscription(ctx, &awsrds.DeleteEventSubscriptionInput{
		SubscriptionName: aws.String("missing"),
	})

	var notFound *rdstypes.SubscriptionNotFoundFault
	if !errors.As(err, &notFound) {
		t.Fatalf("DeleteEventSubscription(missing) err = %v, want *types.SubscriptionNotFoundFault", err)
	}

	assertAPICode(t, err, "SubscriptionNotFound")

	_, err = client.DescribeEventSubscriptions(ctx, &awsrds.DescribeEventSubscriptionsInput{
		SubscriptionName: aws.String("missing"),
	})
	if !errors.As(err, &notFound) {
		t.Fatalf("DescribeEventSubscriptions(missing) err = %v, want *types.SubscriptionNotFoundFault", err)
	}

	in := &awsrds.CreateEventSubscriptionInput{
		SubscriptionName: aws.String("dup"),
		SnsTopicArn:      aws.String("arn:aws:sns:us-east-1:123456789012:rds-events"),
	}
	if _, err := client.CreateEventSubscription(ctx, in); err != nil {
		t.Fatalf("CreateEventSubscription: %v", err)
	}

	_, err = client.CreateEventSubscription(ctx, in)

	var exists *rdstypes.SubscriptionAlreadyExistFault
	if !errors.As(err, &exists) {
		t.Fatalf("duplicate CreateEventSubscription err = %v, want *types.SubscriptionAlreadyExistFault", err)
	}

	assertAPICode(t, err, "SubscriptionAlreadyExist")
}

// assertAPICode checks the wire error code of an SDK error.
func assertAPICode(t *testing.T, err error, want string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != want {
		t.Fatalf("error code of %v, want %s", err, want)
	}
}
