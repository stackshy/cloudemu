package pubsub_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	cloudfunctions2 "google.golang.org/api/cloudfunctions/v2"
	"google.golang.org/api/option"
	pubsubv1 "google.golang.org/api/pubsub/v1"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/server"
	"github.com/stackshy/cloudemu/v2/server/gcp/cloudfunctions"
	"github.com/stackshy/cloudemu/v2/server/gcp/pubsub"
)

// gen2FnParent is the v2 Cloud Functions parent every test below deploys
// under.
const gen2FnParent = "projects/" + project + "/locations/us-central1"

// gen2PubsubCloudEvent mirrors the structured-mode CloudEvent body a gen2
// Pub/Sub-triggered function receives (cloudfunctions.gen2PubsubEvent,
// redeclared here since that's an unexported wire-package type).
type gen2PubsubCloudEvent struct {
	SpecVersion     string `json:"specversion"`
	Type            string `json:"type"`
	Source          string `json:"source"`
	ID              string `json:"id"`
	Time            string `json:"time"`
	DataContentType string `json:"datacontenttype"`
	Data            struct {
		Message struct {
			Data        string            `json:"data"`
			Attributes  map[string]string `json:"attributes"`
			MessageID   string            `json:"messageId"`
			PublishTime string            `json:"publishTime"`
		} `json:"message"`
		Subscription string `json:"subscription"`
	} `json:"data"`
}

// gen2Clients returns real Pub/Sub v1 and Cloud Functions v2 SDK clients
// pointed at srvURL.
func gen2Clients(t *testing.T, srvURL string) (*pubsubv1.Service, *cloudfunctions2.Service) {
	t.Helper()

	ctx := context.Background()

	psSvc, err := pubsubv1.NewService(ctx, option.WithEndpoint(srvURL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("pubsubv1.NewService: %v", err)
	}

	cfSvc, err := cloudfunctions2.NewService(ctx, option.WithEndpoint(srvURL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("cloudfunctions2.NewService: %v", err)
	}

	return psSvc, cfSvc
}

// createGen2PubsubFunction deploys a gen2 function whose eventTrigger binds
// topic, mirroring how google_cloudfunctions2_function.event_trigger is
// configured for a Pub/Sub trigger.
func createGen2PubsubFunction(t *testing.T, cfSvc *cloudfunctions2.Service, name, topic string) {
	t.Helper()

	_, err := cfSvc.Projects.Locations.Functions.Create(gen2FnParent, &cloudfunctions2.Function{
		BuildConfig: &cloudfunctions2.BuildConfig{Runtime: "go121", EntryPoint: "Handle"},
		EventTrigger: &cloudfunctions2.EventTrigger{
			EventType:   "google.cloud.pubsub.topic.v1.messagePublished",
			PubsubTopic: "projects/" + project + "/topics/" + topic,
		},
	}).FunctionId(name).Context(context.Background()).Do()
	if err != nil {
		t.Fatalf("Create gen2 function %s: %v", name, err)
	}
}

func publishMessage(t *testing.T, psSvc *pubsubv1.Service, topic, data string, attrs map[string]string) {
	t.Helper()

	_, err := psSvc.Projects.Topics.Publish("projects/"+project+"/topics/"+topic, &pubsubv1.PublishRequest{
		Messages: []*pubsubv1.PubsubMessage{
			{Data: base64.StdEncoding.EncodeToString([]byte(data)), Attributes: attrs},
		},
	}).Context(context.Background()).Do()
	if err != nil {
		t.Fatalf("Publish to %s: %v", topic, err)
	}
}

// TestGen2FunctionFiresOnPubsubTrigger is the world-case delivery test: a gen2
// function with a Pub/Sub eventTrigger fires with the CloudEvent shape a real
// Eventarc-backed trigger delivers ({specversion, type, source, id, time,
// data: {message: {...}, subscription}}), not the flat gen1 legacy shape.
func TestGen2FunctionFiresOnPubsubTrigger(t *testing.T) {
	srv, cloud := newServerWithFunctions(t)
	psSvc, cfSvc := gen2Clients(t, srv.URL)

	doRequest(t, srv, http.MethodPut, topicURL("orders"), `{}`)
	createGen2PubsubFunction(t, cfSvc, "on-order", "orders")

	var (
		mu       sync.Mutex
		payloads [][]byte
	)

	cloud.CloudFunctions.RegisterHandler("on-order", func(_ context.Context, payload []byte) ([]byte, error) {
		mu.Lock()
		payloads = append(payloads, append([]byte(nil), payload...))
		mu.Unlock()

		return nil, nil
	})

	publishMessage(t, psSvc, "orders", "order-42", map[string]string{"region": "us"})

	got := waitForBytes(t, &mu, &payloads, 1)

	var evt gen2PubsubCloudEvent
	if err := json.Unmarshal(got[0], &evt); err != nil {
		t.Fatalf("decode CloudEvent: %v (raw %s)", err, got[0])
	}

	if evt.SpecVersion != "1.0" {
		t.Errorf("specversion = %q, want 1.0", evt.SpecVersion)
	}

	if evt.Type != "google.cloud.pubsub.topic.v1.messagePublished" {
		t.Errorf("type = %q, want google.cloud.pubsub.topic.v1.messagePublished", evt.Type)
	}

	if evt.Source != "//pubsub.googleapis.com/projects/"+project+"/topics/orders" {
		t.Errorf("source = %q", evt.Source)
	}

	if evt.ID == "" {
		t.Error("id empty")
	}

	if evt.Data.Subscription == "" {
		t.Error("data.subscription empty")
	}

	data, err := base64.StdEncoding.DecodeString(evt.Data.Message.Data)
	if err != nil {
		t.Fatalf("decode message.data: %v", err)
	}

	if string(data) != "order-42" {
		t.Errorf("message.data = %q, want order-42", data)
	}

	if evt.Data.Message.MessageID == "" {
		t.Error("message.messageId empty")
	}

	if evt.Data.Message.PublishTime == "" {
		t.Error("message.publishTime empty")
	}

	if evt.Data.Message.Attributes["region"] != "us" {
		t.Errorf("message.attributes = %v, want region=us", evt.Data.Message.Attributes)
	}
}

// TestGen2FunctionDoesNotFireForUnboundTopic confirms a publish to a topic no
// gen2 function's eventTrigger names never invokes it.
func TestGen2FunctionDoesNotFireForUnboundTopic(t *testing.T) {
	srv, cloud := newServerWithFunctions(t)
	psSvc, cfSvc := gen2Clients(t, srv.URL)

	doRequest(t, srv, http.MethodPut, topicURL("bound"), `{}`)
	doRequest(t, srv, http.MethodPut, topicURL("other"), `{}`)
	createGen2PubsubFunction(t, cfSvc, "on-bound", "bound")

	var invocations int

	cloud.CloudFunctions.RegisterHandler("on-bound", func(_ context.Context, _ []byte) ([]byte, error) {
		invocations++
		return nil, nil
	})

	publishMessage(t, psSvc, "other", "irrelevant", nil)

	if invocations != 0 {
		t.Fatalf("invocations = %d, want 0 (publish to an unbound topic must not fire)", invocations)
	}
}

// TestGen2FunctionDoesNotFireAfterDelete confirms a publish after the gen2
// function is deleted no longer invokes it (the driver entry is removed
// alongside the gen2 map entry, per #996).
func TestGen2FunctionDoesNotFireAfterDelete(t *testing.T) {
	srv, cloud := newServerWithFunctions(t)
	psSvc, cfSvc := gen2Clients(t, srv.URL)

	doRequest(t, srv, http.MethodPut, topicURL("deletable"), `{}`)
	createGen2PubsubFunction(t, cfSvc, "on-deletable", "deletable")

	var invocations int

	cloud.CloudFunctions.RegisterHandler("on-deletable", func(_ context.Context, _ []byte) ([]byte, error) {
		invocations++
		return nil, nil
	})

	if _, err := cfSvc.Projects.Locations.Functions.
		Delete(gen2FnParent + "/functions/on-deletable").Context(context.Background()).Do(); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	publishMessage(t, psSvc, "deletable", "too-late", nil)

	if invocations != 0 {
		t.Fatalf("invocations = %d, want 0 (a deleted function must not fire)", invocations)
	}
}

// TestGen2PubsubTriggerRecursionBounded confirms a gen2 function that
// republishes to its own trigger topic terminates at recursionguard.MaxDepth
// instead of recursing unbounded (Publish -> InvokeForTopic -> Invoke ->
// handler -> Publish -> ...), mirroring the AWS S3 -> Lambda and DynamoDB
// Streams -> Lambda write-back regression tests. It wires the pubsub and
// cloudfunctions wire handlers directly (rather than through
// newServerWithFunctions) so the handler can drive the recursive publish
// in-process through the exact same PublishToTopic entrypoint GCS
// object-change notifications use, keeping ctx (and its recursion depth)
// flowing on one goroutine's call stack — the scenario the guard exists for.
func TestGen2PubsubTriggerRecursionBounded(t *testing.T) {
	cloud := cloudemu.NewGCP()

	psHandler := pubsub.New(cloud.PubSub)
	cfHandler := cloudfunctions.New(cloud.CloudFunctions)
	psHandler.SetFunctionInvoker(cfHandler)

	srv := httptest.NewServer(server.New(cfHandler, psHandler))
	t.Cleanup(srv.Close)

	doRequest(t, srv, http.MethodPut, topicURL("loopback"), `{}`)

	_, cfSvc := gen2Clients(t, srv.URL)
	createGen2PubsubFunction(t, cfSvc, "on-loopback", "loopback")

	var (
		mu          sync.Mutex
		invocations int
	)

	cloud.CloudFunctions.RegisterHandler("on-loopback", func(ctx context.Context, _ []byte) ([]byte, error) {
		mu.Lock()
		invocations++
		mu.Unlock()

		psHandler.PublishToTopic(ctx, project, "loopback", []byte("again"), nil)

		return nil, nil
	})

	psHandler.PublishToTopic(context.Background(), project, "loopback", []byte("start"), nil)

	mu.Lock()
	got := invocations
	mu.Unlock()

	if got != recursionguard.MaxDepth {
		t.Fatalf("handler invoked %d times, want exactly %d (recursive-loop guard did not bound the chain)",
			got, recursionguard.MaxDepth)
	}
}
