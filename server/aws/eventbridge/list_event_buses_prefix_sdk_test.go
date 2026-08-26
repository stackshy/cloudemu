package eventbridge_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
)

// TestSDKEventBridgeListEventBusesNamePrefix verifies ListEventBuses honors the
// NamePrefix filter instead of returning every bus.
func TestSDKEventBridgeListEventBusesNamePrefix(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	for _, name := range []string{"alpha-bus", "beta-bus"} {
		if _, err := client.CreateEventBus(ctx, &awseb.CreateEventBusInput{Name: aws.String(name)}); err != nil {
			t.Fatalf("CreateEventBus(%s): %v", name, err)
		}
	}

	out, err := client.ListEventBuses(ctx, &awseb.ListEventBusesInput{NamePrefix: aws.String("alpha")})
	if err != nil {
		t.Fatalf("ListEventBuses: %v", err)
	}

	if len(out.EventBuses) != 1 {
		names := make([]string, 0, len(out.EventBuses))
		for i := range out.EventBuses {
			names = append(names, aws.ToString(out.EventBuses[i].Name))
		}

		t.Fatalf("NamePrefix=alpha returned %v, want only [alpha-bus]", names)
	}

	if aws.ToString(out.EventBuses[0].Name) != "alpha-bus" {
		t.Fatalf("got %q, want alpha-bus", aws.ToString(out.EventBuses[0].Name))
	}

	// No prefix still lists everything (default + both custom buses).
	all, err := client.ListEventBuses(ctx, &awseb.ListEventBusesInput{})
	if err != nil {
		t.Fatalf("ListEventBuses(all): %v", err)
	}

	if len(all.EventBuses) != 3 {
		t.Fatalf("no-prefix returned %d buses, want 3", len(all.EventBuses))
	}
}
