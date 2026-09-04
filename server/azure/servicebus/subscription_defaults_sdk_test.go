package servicebus_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicebus/armservicebus/v2"
)

// maxSBTimeToLive is Service Bus' TimeSpan.MaxValue default for
// defaultMessageTimeToLive / autoDeleteOnIdle when the client omits them.
const maxSBTimeToLive = "P10675199DT2H48M5.4775807S"

// TestSDKSubscriptionCreateDefaults asserts a subscription created with only the
// required maxDeliveryCount reads back Azure's server-side defaults, so a
// Terraform azurerm_servicebus_subscription with defaults does not drift:
//   - enableBatchedOperations = true
//   - deadLetteringOnFilterEvaluationExceptions = true
//   - autoDeleteOnIdle = TimeSpan.Max
//   - defaultMessageTimeToLive = TimeSpan.Max
//   - lockDuration = PT1M
func TestSDKSubscriptionCreateDefaults(t *testing.T) {
	ts := pubsubServer(t)
	cf := newClientFactory(t, ts)
	ctx := context.Background()

	createNS(t, cf.NewNamespacesClient(), rgName, nsName, nil)

	if _, err := cf.NewTopicsClient().CreateOrUpdate(ctx, rgName, nsName, "orders",
		armservicebus.SBTopic{}, nil); err != nil {
		t.Fatalf("topic CreateOrUpdate: %v", err)
	}

	sub, err := cf.NewSubscriptionsClient().CreateOrUpdate(ctx, rgName, nsName, "orders", "all",
		armservicebus.SBSubscription{Properties: &armservicebus.SBSubscriptionProperties{}}, nil)
	if err != nil {
		t.Fatalf("subscription CreateOrUpdate: %v", err)
	}

	p := sub.Properties
	if p == nil {
		t.Fatal("nil properties")
	}

	if p.EnableBatchedOperations == nil || !*p.EnableBatchedOperations {
		t.Errorf("enableBatchedOperations = %v, want true (default)", p.EnableBatchedOperations)
	}

	if p.DeadLetteringOnFilterEvaluationExceptions == nil || !*p.DeadLetteringOnFilterEvaluationExceptions {
		t.Errorf("deadLetteringOnFilterEvaluationExceptions = %v, want true (default)",
			p.DeadLetteringOnFilterEvaluationExceptions)
	}

	if p.AutoDeleteOnIdle == nil || *p.AutoDeleteOnIdle != maxSBTimeToLive {
		t.Errorf("autoDeleteOnIdle = %v, want %s (default)", p.AutoDeleteOnIdle, maxSBTimeToLive)
	}

	if p.DefaultMessageTimeToLive == nil || *p.DefaultMessageTimeToLive != maxSBTimeToLive {
		t.Errorf("defaultMessageTimeToLive = %v, want %s (default)", p.DefaultMessageTimeToLive, maxSBTimeToLive)
	}

	if p.LockDuration == nil || *p.LockDuration != "PT1M" {
		t.Errorf("lockDuration = %v, want PT1M (default)", p.LockDuration)
	}
}

// TestSDKSubscriptionExplicitRoundTrip asserts explicit non-default subscription
// properties survive create -> Get, so an azurerm_servicebus_subscription that
// sets these does not perpetually drift.
func TestSDKSubscriptionExplicitRoundTrip(t *testing.T) {
	ts := pubsubServer(t)
	cf := newClientFactory(t, ts)
	ctx := context.Background()

	createNS(t, cf.NewNamespacesClient(), rgName, nsName, nil)

	if _, err := cf.NewTopicsClient().CreateOrUpdate(ctx, rgName, nsName, "orders",
		armservicebus.SBTopic{}, nil); err != nil {
		t.Fatalf("topic CreateOrUpdate: %v", err)
	}

	subs := cf.NewSubscriptionsClient()

	if _, err := subs.CreateOrUpdate(ctx, rgName, nsName, "orders", "all", armservicebus.SBSubscription{
		Properties: &armservicebus.SBSubscriptionProperties{
			MaxDeliveryCount:                          to.Ptr[int32](5),
			EnableBatchedOperations:                   to.Ptr(false),
			AutoDeleteOnIdle:                          to.Ptr("PT5M"),
			DeadLetteringOnFilterEvaluationExceptions: to.Ptr(false),
		},
	}, nil); err != nil {
		t.Fatalf("subscription CreateOrUpdate: %v", err)
	}

	got, err := subs.Get(ctx, rgName, nsName, "orders", "all", nil)
	if err != nil {
		t.Fatalf("subscription Get: %v", err)
	}

	p := got.Properties
	if p.EnableBatchedOperations == nil || *p.EnableBatchedOperations {
		t.Errorf("enableBatchedOperations = %v, want false", p.EnableBatchedOperations)
	}

	if p.AutoDeleteOnIdle == nil || *p.AutoDeleteOnIdle != "PT5M" {
		t.Errorf("autoDeleteOnIdle = %v, want PT5M", p.AutoDeleteOnIdle)
	}

	if p.DeadLetteringOnFilterEvaluationExceptions == nil || *p.DeadLetteringOnFilterEvaluationExceptions {
		t.Errorf("deadLetteringOnFilterEvaluationExceptions = %v, want false", p.DeadLetteringOnFilterEvaluationExceptions)
	}
}

// TestSDKQueueForwardDeadLetteredRoundTrip asserts a queue's
// forwardDeadLetteredMessagesTo survives create -> Get, so an
// azurerm_servicebus_queue that sets forward_dead_lettered_messages_to does not
// drift.
func TestSDKQueueForwardDeadLetteredRoundTrip(t *testing.T) {
	ts := pubsubServer(t)
	cf := newClientFactory(t, ts)
	ctx := context.Background()

	createNS(t, cf.NewNamespacesClient(), rgName, nsName, nil)

	queues := cf.NewQueuesClient()

	if _, err := queues.CreateOrUpdate(ctx, rgName, nsName, "dead", armservicebus.SBQueue{}, nil); err != nil {
		t.Fatalf("create target queue: %v", err)
	}

	if _, err := queues.CreateOrUpdate(ctx, rgName, nsName, "src", armservicebus.SBQueue{
		Properties: &armservicebus.SBQueueProperties{ForwardDeadLetteredMessagesTo: to.Ptr("dead")},
	}, nil); err != nil {
		t.Fatalf("create src queue: %v", err)
	}

	got, err := queues.Get(ctx, rgName, nsName, "src", nil)
	if err != nil {
		t.Fatalf("queue Get: %v", err)
	}

	if got.Properties == nil || got.Properties.ForwardDeadLetteredMessagesTo == nil ||
		*got.Properties.ForwardDeadLetteredMessagesTo != "dead" {
		t.Errorf("forwardDeadLetteredMessagesTo = %v, want dead", got.Properties.ForwardDeadLetteredMessagesTo)
	}
}
