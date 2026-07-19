package eventbridge_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
)

// TestSDKListOrderingDeterministic locks the #259 ordering fix at the wire
// level: ListEventBuses must return the same sequence on every call. The
// pre-fix bug was random map iteration order, so repetition is the signal.
// The driver pre-seeds a "default" bus, so we only assert len >= 5 and an
// identical sequence across calls.
func TestSDKListOrderingDeterministic(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	names := []string{"zeta", "alpha", "mid", "beta", "omega"}
	for _, name := range names {
		if _, err := client.CreateEventBus(ctx, &awseb.CreateEventBusInput{
			Name: aws.String(name),
		}); err != nil {
			t.Fatalf("CreateEventBus(%s): %v", name, err)
		}
	}

	list := func() []string {
		out, err := client.ListEventBuses(ctx, &awseb.ListEventBusesInput{})
		if err != nil {
			t.Fatalf("ListEventBuses: %v", err)
		}

		got := make([]string, 0, len(out.EventBuses))
		for i := range out.EventBuses {
			got = append(got, aws.ToString(out.EventBuses[i].Name))
		}

		return got
	}

	first := list()
	if len(first) < len(names) {
		t.Fatalf("ListEventBuses returned %d buses, want at least %d: %v", len(first), len(names), first)
	}

	for call := 2; call <= 5; call++ {
		got := list()
		if len(got) != len(first) {
			t.Fatalf("call %d: got %d buses, want %d", call, len(got), len(first))
		}

		for i := range first {
			if got[i] != first[i] {
				t.Fatalf("call %d: order diverged at index %d: got %v, first call %v", call, i, got, first)
			}
		}
	}
}
