package servicebus_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicebus/armservicebus/v2"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestQueueScopedAuthorizationRuleDoesNotCorruptQueue is the regression for a
// misrouting bug: a queue-scoped "authorizationRules" sub-path (a documented
// real Service Bus operation exposed by QueuesClient.CreateOrUpdateAuthorizationRule
// / GetAuthorizationRule / DeleteAuthorizationRule / ListAuthorizationRules) has
// more path segments than "queues/{name}". serveQueue previously extracted the
// queue name from segs[1] and dispatched on HTTP method regardless of any
// trailing segments, so these calls fell through to the queue's own CRUD
// handlers: a PUT silently reset the queue's properties to defaults (the
// authorization-rule request body doesn't decode into queue properties), a GET
// echoed the queue's own resource instead of 404ing, and a DELETE removed the
// whole queue. The fix rejects any queue path deeper than "queues/{name}" with
// 501 (mirroring how topics/subscriptions/rules already dispatch on exact
// segment length), so the sub-resource is a clean gap rather than data loss.
func TestQueueScopedAuthorizationRuleDoesNotCorruptQueue(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{ServiceBus: cloudP.ServiceBus})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	nsClient := newNamespacesClient(t, ts)
	createNS(t, nsClient, rgName, nsName, nil)

	qc := newQueuesClient(t, ts)
	ctx := context.Background()

	const queueName = "protected-q"

	if _, err := qc.CreateOrUpdate(ctx, rgName, nsName, queueName, armservicebus.SBQueue{
		Properties: &armservicebus.SBQueueProperties{
			MaxSizeInMegabytes: to.Ptr[int32](5120),
			LockDuration:       to.Ptr("PT45S"),
			MaxDeliveryCount:   to.Ptr[int32](7),
		},
	}, nil); err != nil {
		t.Fatalf("create queue: %v", err)
	}

	// CreateOrUpdateAuthorizationRule must not silently succeed by falling
	// through to createQueue; it must fail explicitly.
	if _, err := qc.CreateOrUpdateAuthorizationRule(ctx, rgName, nsName, queueName, "send-rule",
		armservicebus.SBAuthorizationRule{
			Properties: &armservicebus.SBAuthorizationRuleProperties{
				Rights: []*armservicebus.AccessRights{to.Ptr(armservicebus.AccessRightsSend)},
			},
		}, nil); err == nil {
		t.Fatal("CreateOrUpdateAuthorizationRule on a queue returned nil error, want a rejection")
	}

	// The queue's own properties must be untouched.
	got, err := qc.Get(ctx, rgName, nsName, queueName, nil)
	if err != nil {
		t.Fatalf("Get queue after authrule call: %v", err)
	}

	if got.Properties == nil || got.Properties.MaxSizeInMegabytes == nil || *got.Properties.MaxSizeInMegabytes != 5120 {
		t.Fatalf("queue MaxSizeInMegabytes corrupted: %+v", got.Properties)
	}

	if got.Properties.LockDuration == nil || *got.Properties.LockDuration != "PT45S" {
		t.Fatalf("queue LockDuration corrupted: %+v", got.Properties)
	}

	if got.Properties.MaxDeliveryCount == nil || *got.Properties.MaxDeliveryCount != 7 {
		t.Fatalf("queue MaxDeliveryCount corrupted: %+v", got.Properties)
	}

	// DeleteAuthorizationRule must not delete the queue itself.
	if _, err := qc.DeleteAuthorizationRule(ctx, rgName, nsName, queueName, "send-rule", nil); err == nil {
		t.Fatal("DeleteAuthorizationRule on a queue returned nil error, want a rejection")
	}

	if _, err := qc.Get(ctx, rgName, nsName, queueName, nil); err != nil {
		t.Fatalf("queue must survive DeleteAuthorizationRule call, but Get failed: %v", err)
	}

	// GetAuthorizationRule must not echo the queue's own resource as if it
	// were the auth rule.
	if _, err := qc.GetAuthorizationRule(ctx, rgName, nsName, queueName, "does-not-exist", nil); err == nil {
		t.Fatal("GetAuthorizationRule on a queue returned nil error, want a rejection")
	}

	// All of the above must surface as a real ARM error response (not, say, a
	// transport-level failure), so a caller sees an explicit rejection.
	var respErr *azcore.ResponseError
	if _, err := qc.CreateOrUpdateAuthorizationRule(ctx, rgName, nsName, queueName, "send-rule",
		armservicebus.SBAuthorizationRule{}, nil); err != nil {
		if !errors.As(err, &respErr) {
			t.Fatalf("expected an azcore.ResponseError, got %T: %v", err, err)
		}
	}
}
