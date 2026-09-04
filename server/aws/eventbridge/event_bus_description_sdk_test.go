package eventbridge_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
)

// TestSDKEventBridgeEventBusDescriptionRoundTrips verifies that a bus's
// Description survives past the CreateEventBus response — it must also be
// returned by DescribeEventBus and ListEventBuses, not just echoed back on
// creation.
func TestSDKEventBridgeEventBusDescriptionRoundTrips(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	const wantDescription = "orders event bus for the checkout service"

	if _, err := client.CreateEventBus(ctx, &awseb.CreateEventBusInput{
		Name:        aws.String("described-bus"),
		Description: aws.String(wantDescription),
	}); err != nil {
		t.Fatalf("CreateEventBus: %v", err)
	}

	desc, err := client.DescribeEventBus(ctx, &awseb.DescribeEventBusInput{Name: aws.String("described-bus")})
	if err != nil {
		t.Fatalf("DescribeEventBus: %v", err)
	}

	if got := aws.ToString(desc.Description); got != wantDescription {
		t.Fatalf("DescribeEventBus Description = %q, want %q", got, wantDescription)
	}

	list, err := client.ListEventBuses(ctx, &awseb.ListEventBusesInput{NamePrefix: aws.String("described-bus")})
	if err != nil {
		t.Fatalf("ListEventBuses: %v", err)
	}

	if len(list.EventBuses) != 1 {
		t.Fatalf("ListEventBuses = %+v, want exactly one bus", list.EventBuses)
	}

	if got := aws.ToString(list.EventBuses[0].Description); got != wantDescription {
		t.Fatalf("ListEventBuses Description = %q, want %q", got, wantDescription)
	}
}

// TestSDKEventBridgeEventBusWithoutDescription verifies that a bus created
// without a Description reports an empty one on Describe/List, rather than
// echoing back the wrong value or erroring.
func TestSDKEventBridgeEventBusWithoutDescription(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	if _, err := client.CreateEventBus(ctx, &awseb.CreateEventBusInput{Name: aws.String("undescribed-bus")}); err != nil {
		t.Fatalf("CreateEventBus: %v", err)
	}

	desc, err := client.DescribeEventBus(ctx, &awseb.DescribeEventBusInput{Name: aws.String("undescribed-bus")})
	if err != nil {
		t.Fatalf("DescribeEventBus: %v", err)
	}

	if got := aws.ToString(desc.Description); got != "" {
		t.Fatalf("DescribeEventBus Description = %q, want empty", got)
	}
}
