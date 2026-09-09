package eventgrid_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"
)

// TestSDKEventSubscriptionDefaultsStamped proves that an event subscription
// created without a retry policy or delivery schema reports Event Grid's
// documented read-only defaults on GET — retryPolicy 30 attempts / 1440-minute
// TTL and eventDeliverySchema EventGridSchema — matching real Azure. A real user
// reading the subscription back (az CLI, SDK, Terraform state) sees these
// populated on Azure, so the emulator must populate them too.
func TestSDKEventSubscriptionDefaultsStamped(t *testing.T) {
	client := newEventGridFactory(t).NewEventSubscriptionsClient()
	ctx := context.Background()

	scope := "/subscriptions/" + testSub + "/resourceGroups/" + testRG

	createEventSubscription(t, client, scope, "defaults-sub", "/blobs")

	got, err := client.Get(ctx, scope, "defaults-sub", nil)
	if err != nil {
		t.Fatalf("Get defaults-sub: %v", err)
	}

	props := got.Properties
	if props == nil || props.RetryPolicy == nil {
		t.Fatalf("retryPolicy not stamped: %+v", props)
	}

	if props.RetryPolicy.MaxDeliveryAttempts == nil || *props.RetryPolicy.MaxDeliveryAttempts != 30 {
		t.Fatalf("MaxDeliveryAttempts = %v, want 30", props.RetryPolicy.MaxDeliveryAttempts)
	}

	if props.RetryPolicy.EventTimeToLiveInMinutes == nil || *props.RetryPolicy.EventTimeToLiveInMinutes != 1440 {
		t.Fatalf("EventTimeToLiveInMinutes = %v, want 1440", props.RetryPolicy.EventTimeToLiveInMinutes)
	}

	if props.EventDeliverySchema == nil || *props.EventDeliverySchema != armeventgrid.EventDeliverySchemaEventGridSchema {
		t.Fatalf("EventDeliverySchema = %v, want EventGridSchema", props.EventDeliverySchema)
	}
}

// TestSDKEventSubscriptionExplicitRetryPolicyPreserved proves that caller-set
// retry policy and delivery schema are round-tripped unchanged — the default
// stamping fills only absent fields and never overrides an explicit value.
func TestSDKEventSubscriptionExplicitRetryPolicyPreserved(t *testing.T) {
	client := newEventGridFactory(t).NewEventSubscriptionsClient()
	ctx := context.Background()

	scope := "/subscriptions/" + testSub + "/resourceGroups/" + testRG

	sub := armeventgrid.EventSubscription{
		Properties: &armeventgrid.EventSubscriptionProperties{
			Destination: &armeventgrid.WebHookEventSubscriptionDestination{
				EndpointType: to.Ptr(armeventgrid.EndpointTypeWebHook),
				Properties: &armeventgrid.WebHookEventSubscriptionDestinationProperties{
					EndpointURL: to.Ptr("https://example.test/hook"),
				},
			},
			EventDeliverySchema: to.Ptr(armeventgrid.EventDeliverySchemaCloudEventSchemaV10),
			RetryPolicy: &armeventgrid.RetryPolicy{
				MaxDeliveryAttempts:      to.Ptr(int32(5)),
				EventTimeToLiveInMinutes: to.Ptr(int32(60)),
			},
		},
	}

	poller, err := client.BeginCreateOrUpdate(ctx, scope, "explicit-sub", sub, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}
	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("CreateOrUpdate PollUntilDone: %v", err)
	}

	got, err := client.Get(ctx, scope, "explicit-sub", nil)
	if err != nil {
		t.Fatalf("Get explicit-sub: %v", err)
	}

	props := got.Properties
	if props == nil || props.RetryPolicy == nil ||
		props.RetryPolicy.MaxDeliveryAttempts == nil || *props.RetryPolicy.MaxDeliveryAttempts != 5 ||
		props.RetryPolicy.EventTimeToLiveInMinutes == nil || *props.RetryPolicy.EventTimeToLiveInMinutes != 60 {
		t.Fatalf("explicit retryPolicy not preserved: %+v", props.RetryPolicy)
	}

	if props.EventDeliverySchema == nil || *props.EventDeliverySchema != armeventgrid.EventDeliverySchemaCloudEventSchemaV10 {
		t.Fatalf("EventDeliverySchema = %v, want CloudEventSchemaV1_0", props.EventDeliverySchema)
	}
}

// TestSDKEventSubscriptionPartialRetryPolicyFilled proves the default stamping
// is per-subfield: a retry policy that sets only MaxDeliveryAttempts keeps that
// value and has its absent EventTimeToLiveInMinutes filled with the 1440 default
// — not an all-or-nothing fill that would either overwrite the caller's value or
// leave the TTL empty.
func TestSDKEventSubscriptionPartialRetryPolicyFilled(t *testing.T) {
	client := newEventGridFactory(t).NewEventSubscriptionsClient()
	ctx := context.Background()

	scope := "/subscriptions/" + testSub + "/resourceGroups/" + testRG

	sub := armeventgrid.EventSubscription{
		Properties: &armeventgrid.EventSubscriptionProperties{
			Destination: &armeventgrid.WebHookEventSubscriptionDestination{
				EndpointType: to.Ptr(armeventgrid.EndpointTypeWebHook),
				Properties: &armeventgrid.WebHookEventSubscriptionDestinationProperties{
					EndpointURL: to.Ptr("https://example.test/hook"),
				},
			},
			RetryPolicy: &armeventgrid.RetryPolicy{
				MaxDeliveryAttempts: to.Ptr(int32(7)),
			},
		},
	}

	poller, err := client.BeginCreateOrUpdate(ctx, scope, "partial-sub", sub, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}
	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("CreateOrUpdate PollUntilDone: %v", err)
	}

	got, err := client.Get(ctx, scope, "partial-sub", nil)
	if err != nil {
		t.Fatalf("Get partial-sub: %v", err)
	}

	props := got.Properties
	if props == nil || props.RetryPolicy == nil {
		t.Fatalf("retryPolicy missing: %+v", props)
	}

	// The caller's explicit attempts survive; only the absent TTL is defaulted.
	if props.RetryPolicy.MaxDeliveryAttempts == nil || *props.RetryPolicy.MaxDeliveryAttempts != 7 {
		t.Fatalf("MaxDeliveryAttempts = %v, want 7 (caller value preserved)", props.RetryPolicy.MaxDeliveryAttempts)
	}

	if props.RetryPolicy.EventTimeToLiveInMinutes == nil || *props.RetryPolicy.EventTimeToLiveInMinutes != 1440 {
		t.Fatalf("EventTimeToLiveInMinutes = %v, want 1440 (absent subfield defaulted)", props.RetryPolicy.EventTimeToLiveInMinutes)
	}
}
