package azure_test

// Real-user end-to-end proof that an Azure Service Bus TOPIC/SUBSCRIPTION
// trigger actually fires the bound function: a function app declares a
// serviceBusTrigger binding on (topicName, subscriptionName) via ARM, then a
// message published to that topic with the real Service Bus REST data plane
// synchronously invokes the function with the copy delivered to THAT
// subscription. This is the topic/subscription counterpart of
// TestQueueStorageTriggerInvokesFunction (queue-bound queueTrigger) and closes
// the gap left by #997, which only wired queueName-bound serviceBusTrigger
// bindings.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

// sbFuncAppBase returns the ARM base path of a function app used by these tests.
func sbFuncAppBase(app string) string {
	return "/subscriptions/sub-sbt/resourceGroups/rg-sbt/providers/Microsoft.Web/sites/" + app
}

// createSBTopicTriggeredApp creates a function app plus one deployed function
// declaring a serviceBusTrigger binding on (topicName, subscriptionName).
func createSBTopicTriggeredApp(t *testing.T, ts *httptest.Server, app, topic, sub string) {
	t.Helper()

	base := sbFuncAppBase(app)
	armPut(t, ts, base+"?api-version=2022-03-01", `{"location":"eastus","properties":{"siteConfig":{}}}`)
	armPut(t, ts, base+"/functions/consume?api-version=2022-03-01",
		`{"properties":{"config":{"bindings":[`+
			`{"name":"item","type":"serviceBusTrigger","direction":"in",`+
			`"topicName":"`+topic+`","subscriptionName":"`+sub+`","connection":"AzureWebJobsServiceBus"}]}}}`)
}

// createSBTopicSub creates a topic and one subscription under the shared
// routing-test namespace (seedSBNamespace/nsScope, from
// queue_servicebus_routing_test.go).
func createSBTopicSub(t *testing.T, ts *httptest.Server, topic, sub string) {
	t.Helper()

	sbDo(t, ts, http.MethodPut, nsScope()+"/topics/"+topic+sbAPIVer, `{"properties":{}}`, http.StatusOK)
	sbDo(t, ts, http.MethodPut, nsScope()+"/topics/"+topic+"/subscriptions/"+sub+sbAPIVer, `{"properties":{}}`, http.StatusOK)
}

func TestServiceBusTopicTriggerInvokesFunction(t *testing.T) {
	ts, p := newFullAzureServerWithProvider(t)

	const (
		app   = "sbt-app"
		topic = "orders"
		sub   = "billing"
	)

	createSBTopicTriggeredApp(t, ts, app, topic, sub)

	got := make(chan string, 1)
	p.Functions.RegisterHandler(app, func(_ context.Context, payload []byte) ([]byte, error) {
		select {
		case got <- string(payload):
		default:
		}

		return payload, nil
	})

	seedSBNamespace(t, ts)
	createSBTopicSub(t, ts, topic, sub)

	sbDo(t, ts, http.MethodPost, "/"+sbNS+"/"+topic+"/messages", "topic-payload", http.StatusCreated)

	// Delivery is synchronous, so the function has fired by the time the
	// publish HTTP call returns.
	select {
	case body := <-got:
		if body != "topic-payload" {
			t.Fatalf("function received %q, want topic-payload", body)
		}
	default:
		t.Fatal("topic/subscription-triggered function was not invoked")
	}
}

// TestServiceBusTopicTriggerWrongTargetDoesNotFire proves a function bound to
// (topicA, subA) does not fire for a message published to a different topic, or
// to a different subscription of the SAME topic.
func TestServiceBusTopicTriggerWrongTargetDoesNotFire(t *testing.T) {
	ts, p := newFullAzureServerWithProvider(t)

	const (
		app        = "sbt-app2"
		boundTopic = "orders"
		boundSub   = "billing"
	)

	createSBTopicTriggeredApp(t, ts, app, boundTopic, boundSub)

	fired := make(chan struct{}, 1)
	p.Functions.RegisterHandler(app, func(_ context.Context, payload []byte) ([]byte, error) {
		fired <- struct{}{}
		return payload, nil
	})

	seedSBNamespace(t, ts)

	// A different topic entirely, with a subscription of the SAME name.
	createSBTopicSub(t, ts, "shipping", boundSub)
	sbDo(t, ts, http.MethodPost, "/"+sbNS+"/shipping/messages", "x", http.StatusCreated)

	// The SAME topic, but a different subscription.
	createSBTopicSub(t, ts, boundTopic, "audit")
	sbDo(t, ts, http.MethodPost, "/"+sbNS+"/"+boundTopic+"/messages", "y", http.StatusCreated)

	select {
	case <-fired:
		t.Fatal("function fired for a topic/subscription it is not bound to")
	default:
	}
}

// createQueueStorageTriggeredApp creates a function app plus one deployed
// function declaring a Queue Storage queueTrigger binding, mirroring #997's
// TestQueueStorageTriggerInvokesFunction.
func createQueueStorageTriggeredApp(t *testing.T, ts *httptest.Server, app, queue string) {
	t.Helper()

	base := sbFuncAppBase(app)
	armPut(t, ts, base+"?api-version=2022-03-01", `{"location":"eastus","properties":{"siteConfig":{}}}`)
	armPut(t, ts, base+"/functions/consume?api-version=2022-03-01",
		`{"properties":{"config":{"bindings":[`+
			`{"name":"item","type":"queueTrigger","direction":"in",`+
			`"queueName":"`+queue+`","connection":"AzureWebJobsStorage"}]}}}`)
}

// TestServiceBusQueueAndTopicTriggersCoexist proves the #997 Queue Storage
// queueTrigger path still fires unchanged alongside a new Service Bus
// topic/subscription trigger, with no cross-fire between them. (The
// queueName-bound serviceBusTrigger path is not exercised here — real
// ARM-provisioned Service Bus queues are stored namespace-prefixed
// (server/azure/servicebus/queue.go:72, "{namespace}/{queue}"), which
// bindingMatchesQueue's bare queueName comparison never matches; that gap
// predates this change and is out of scope for topic/subscription delivery.)
func TestServiceBusQueueAndTopicTriggersCoexist(t *testing.T) {
	ts, p := newFullAzureServerWithProvider(t)
	ctx := context.Background()

	const (
		queueApp = "sbt-queue-app"
		queue    = "jobs"
		topicApp = "sbt-topic-app"
		topic    = "orders"
		sub      = "billing"
	)

	createQueueStorageTriggeredApp(t, ts, queueApp, queue)
	createSBTopicTriggeredApp(t, ts, topicApp, topic, sub)

	queueGot := make(chan string, 1)
	p.Functions.RegisterHandler(queueApp, func(_ context.Context, payload []byte) ([]byte, error) {
		queueGot <- string(payload)
		return payload, nil
	})

	topicGot := make(chan string, 1)
	p.Functions.RegisterHandler(topicApp, func(_ context.Context, payload []byte) ([]byte, error) {
		topicGot <- string(payload)
		return payload, nil
	})

	seedSBNamespace(t, ts)
	createStorageQueue(t, ts, queue)
	createSBTopicSub(t, ts, topic, sub)

	qc := newStorageQueueClient(t, ts, queue)
	if _, err := qc.EnqueueMessage(ctx, "queue-payload", nil); err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}

	sbDo(t, ts, http.MethodPost, "/"+sbNS+"/"+topic+"/messages", "topic-payload", http.StatusCreated)

	select {
	case body := <-queueGot:
		if body != "queue-payload" {
			t.Fatalf("queue-bound function received %q, want queue-payload", body)
		}
	default:
		t.Fatal("queue-bound queueTrigger function was not invoked (#997 regression)")
	}

	select {
	case body := <-topicGot:
		if body != "topic-payload" {
			t.Fatalf("topic-bound function received %q, want topic-payload", body)
		}
	default:
		t.Fatal("topic/subscription-bound function was not invoked")
	}

	// Neither function received the other's message.
	select {
	case <-queueGot:
		t.Fatal("queue-bound function fired a second time (cross-fire from the topic publish)")
	default:
	}

	select {
	case <-topicGot:
		t.Fatal("topic-bound function fired a second time (cross-fire from the queue publish)")
	default:
	}
}

// TestServiceBusTopicTriggerDisabledFunctionSkipped proves a disabled deployed
// function does not fire even though its binding matches the published topic
// and subscription.
func TestServiceBusTopicTriggerDisabledFunctionSkipped(t *testing.T) {
	ts, p := newFullAzureServerWithProvider(t)

	const (
		app   = "sbt-disabled-app"
		topic = "orders"
		sub   = "billing"
	)

	base := sbFuncAppBase(app)
	armPut(t, ts, base+"?api-version=2022-03-01", `{"location":"eastus","properties":{"siteConfig":{}}}`)
	armPut(t, ts, base+"/functions/consume?api-version=2022-03-01",
		`{"properties":{"isDisabled":true,"config":{"bindings":[`+
			`{"name":"item","type":"serviceBusTrigger","direction":"in",`+
			`"topicName":"`+topic+`","subscriptionName":"`+sub+`","connection":"AzureWebJobsServiceBus"}]}}}`)

	fired := make(chan struct{}, 1)
	p.Functions.RegisterHandler(app, func(_ context.Context, payload []byte) ([]byte, error) {
		fired <- struct{}{}
		return payload, nil
	})

	seedSBNamespace(t, ts)
	createSBTopicSub(t, ts, topic, sub)

	sbDo(t, ts, http.MethodPost, "/"+sbNS+"/"+topic+"/messages", "x", http.StatusCreated)

	select {
	case <-fired:
		t.Fatal("a disabled function must not fire")
	default:
	}
}

// TestServiceBusTopicTriggerRecursionGuard proves a function that re-publishes
// to its own trigger topic terminates at recursionguard.MaxDepth rather than
// recursing unbounded, mirroring
// TestS3LambdaNotificationWriteBackDoesNotRecurseUnbounded. The handler
// forwards the ctx it was invoked with into its own SendMessage call (a direct
// provider call, not a fresh HTTP round trip) — that ctx-carried depth is the
// channel the guard rides on.
func TestServiceBusTopicTriggerRecursionGuard(t *testing.T) {
	ts, p := newFullAzureServerWithProvider(t)
	ctx := context.Background()

	const (
		app   = "sbt-loop-app"
		topic = "loop-topic"
		sub   = "loop-sub"
	)

	createSBTopicTriggeredApp(t, ts, app, topic, sub)

	seedSBNamespace(t, ts)
	createSBTopicSub(t, ts, topic, sub)

	// Resolve the subscription's backing driver queue URL so the handler can
	// re-publish directly through the provider. The prefix also matches the
	// subscription's paired $DeadLetterQueue store, so pick the exact name.
	entityName := sbNS + "/" + topic + "/subscriptions/" + sub

	infos, err := p.ServiceBus.ListQueues(ctx, entityName)
	if err != nil {
		t.Fatalf("ListQueues: %v", err)
	}

	var subQueueURL string

	for _, info := range infos {
		if info.Name == entityName {
			subQueueURL = info.URL
			break
		}
	}

	if subQueueURL == "" {
		t.Fatalf("no queue named %q among %d ListQueues results", entityName, len(infos))
	}

	var invocations int32

	p.Functions.RegisterHandler(app, func(ctx context.Context, payload []byte) ([]byte, error) {
		atomic.AddInt32(&invocations, 1)

		_, err := p.ServiceBus.SendMessage(ctx, mqdriver.SendMessageInput{QueueURL: subQueueURL, Body: string(payload)})

		return payload, err
	})

	// The single top-level publish that starts the chain.
	sbDo(t, ts, http.MethodPost, "/"+sbNS+"/"+topic+"/messages", "seed", http.StatusCreated)

	if got := atomic.LoadInt32(&invocations); got != int32(recursionguard.MaxDepth) {
		t.Fatalf("handler invoked %d times, want exactly %d (recursive-loop guard did not bound the chain)",
			got, recursionguard.MaxDepth)
	}
}
