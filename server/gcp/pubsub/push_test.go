package pubsub_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/providers/gcp"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// pushMessage is the "message" member of a Pub/Sub push envelope.
type pushMessage struct {
	Data        string            `json:"data"`
	Attributes  map[string]string `json:"attributes"`
	MessageID   string            `json:"messageId"`
	PublishTime string            `json:"publishTime"`
}

type pushBody struct {
	Message      pushMessage `json:"message"`
	Subscription string      `json:"subscription"`
}

// newServerWithFunctions builds a GCP server wiring both PubSub and Cloud
// Functions (so the PubSub -> Cloud Functions invoker is connected) and returns
// the underlying provider so a test can register a capturing handler.
func newServerWithFunctions(t *testing.T) (*httptest.Server, *gcp.Provider) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := httptest.NewServer(gcpserver.New(gcpserver.Drivers{
		PubSub:         cloud.PubSub,
		CloudFunctions: cloud.CloudFunctions,
		Firestore:      cloud.Firestore,
	}))
	t.Cleanup(srv.Close)

	return srv, cloud
}

// TestPushSubscriptionDeliversEnvelope covers BUG1: a push subscription's
// endpoint receives the push envelope on publish (data / messageId /
// subscription), with no pull ever issued.
func TestPushSubscriptionDeliversEnvelope(t *testing.T) {
	srv := newServer(t)

	var (
		mu       sync.Mutex
		received []pushBody
	)

	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body pushBody
		_ = json.NewDecoder(r.Body).Decode(&body)

		mu.Lock()
		received = append(received, body)
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(receiver.Close)

	if resp := doRequest(t, srv, http.MethodPut, topicURL("push-topic"), `{}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("create topic = %d", resp.StatusCode)
	}

	subBody := `{"topic":"projects/` + project + `/topics/push-topic",` +
		`"pushConfig":{"pushEndpoint":"` + receiver.URL + `"}}`
	if resp := doRequest(t, srv, http.MethodPut, subURL("push-sub"), subBody); resp.StatusCode != http.StatusOK {
		t.Fatalf("create push sub = %d: %s", resp.StatusCode, readBody(t, resp))
	}

	pub := doRequest(t, srv, http.MethodPost, topicURL("push-topic")+":publish",
		`{"messages":[{"data":"`+base64.StdEncoding.EncodeToString([]byte("hello"))+
			`","attributes":{"k":"v"}}]}`)
	if pub.StatusCode != http.StatusOK {
		t.Fatalf("publish = %d: %s", pub.StatusCode, readBody(t, pub))
	}

	_ = readBody(t, pub)

	got := waitForPush(t, &mu, &received, 1)

	if got[0].Subscription != "projects/"+project+"/subscriptions/push-sub" {
		t.Errorf("subscription = %q", got[0].Subscription)
	}

	data, _ := base64.StdEncoding.DecodeString(got[0].Message.Data)
	if string(data) != "hello" {
		t.Errorf("data = %q, want hello", data)
	}

	if got[0].Message.Attributes["k"] != "v" {
		t.Errorf("attributes = %v", got[0].Message.Attributes)
	}

	if got[0].Message.MessageID == "" {
		t.Error("messageId empty")
	}
}

// TestPushDeliveryAutoAcksSoPullSkips covers regression item (c): a message a
// push endpoint accepted (2xx) is auto-acked, so a fallback pull on the same
// subscription does not redeliver it.
func TestPushDeliveryAutoAcksSoPullSkips(t *testing.T) {
	srv := newServer(t)

	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(receiver.Close)

	doRequest(t, srv, http.MethodPut, topicURL("ack-topic"), `{}`)
	subBody := `{"topic":"projects/` + project + `/topics/ack-topic",` +
		`"pushConfig":{"pushEndpoint":"` + receiver.URL + `"}}`
	doRequest(t, srv, http.MethodPut, subURL("ack-sub"), subBody)

	doRequest(t, srv, http.MethodPost, topicURL("ack-topic")+":publish",
		`{"messages":[{"data":"`+base64.StdEncoding.EncodeToString([]byte("x"))+`"}]}`)

	// Give the synchronous best-effort push time to record the ack.
	pull := doRequest(t, srv, http.MethodPost, subURL("ack-sub")+":pull", `{"maxMessages":10}`)

	var pr struct {
		ReceivedMessages []json.RawMessage `json:"receivedMessages"`
	}
	if err := json.Unmarshal([]byte(readBody(t, pull)), &pr); err != nil {
		t.Fatalf("decode pull: %v", err)
	}

	if len(pr.ReceivedMessages) != 0 {
		t.Errorf("pull after 2xx push returned %d messages, want 0 (auto-acked)", len(pr.ReceivedMessages))
	}
}

// TestPublishSucceedsWhenPushEndpointUnreachable covers regression item (a): an
// unreachable push endpoint never fails the publish (best-effort), and the
// unacked message stays available for pull.
func TestPublishSucceedsWhenPushEndpointUnreachable(t *testing.T) {
	srv := newServer(t)

	// A closed server yields an address that refuses connections immediately.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	doRequest(t, srv, http.MethodPut, topicURL("dead-topic"), `{}`)
	subBody := `{"topic":"projects/` + project + `/topics/dead-topic",` +
		`"pushConfig":{"pushEndpoint":"` + deadURL + `"}}`
	doRequest(t, srv, http.MethodPut, subURL("dead-sub"), subBody)

	pub := doRequest(t, srv, http.MethodPost, topicURL("dead-topic")+":publish",
		`{"messages":[{"data":"`+base64.StdEncoding.EncodeToString([]byte("y"))+`"}]}`)
	if pub.StatusCode != http.StatusOK {
		t.Fatalf("publish with unreachable endpoint = %d, want 200", pub.StatusCode)
	}
	_ = readBody(t, pub)

	// Not acked (push failed) -> still pullable.
	pull := doRequest(t, srv, http.MethodPost, subURL("dead-sub")+":pull", `{"maxMessages":10}`)

	var pr struct {
		ReceivedMessages []json.RawMessage `json:"receivedMessages"`
	}
	_ = json.Unmarshal([]byte(readBody(t, pull)), &pr)

	if len(pr.ReceivedMessages) != 1 {
		t.Errorf("pull after failed push returned %d messages, want 1 (left for redelivery)", len(pr.ReceivedMessages))
	}
}

// TestPullSubscriptionUnaffected covers regression item (c): a plain pull
// subscription (no pushConfig) still delivers and acks normally.
func TestPullSubscriptionUnaffected(t *testing.T) {
	srv := newServer(t)

	doRequest(t, srv, http.MethodPut, topicURL("pull-topic"), `{}`)
	doRequest(t, srv, http.MethodPut, subURL("pull-sub"),
		`{"topic":"projects/`+project+`/topics/pull-topic"}`)

	doRequest(t, srv, http.MethodPost, topicURL("pull-topic")+":publish",
		`{"messages":[{"data":"`+base64.StdEncoding.EncodeToString([]byte("z"))+`"}]}`)

	pull := doRequest(t, srv, http.MethodPost, subURL("pull-sub")+":pull", `{"maxMessages":10}`)

	var pr struct {
		ReceivedMessages []json.RawMessage `json:"receivedMessages"`
	}
	if err := json.Unmarshal([]byte(readBody(t, pull)), &pr); err != nil {
		t.Fatalf("decode pull: %v", err)
	}

	if len(pr.ReceivedMessages) != 1 {
		t.Fatalf("pull sub returned %d messages, want 1", len(pr.ReceivedMessages))
	}
}

// TestPublishInvokesEventTriggeredFunction covers BUG2: publishing to a topic
// invokes a Cloud Function whose gen1 eventTrigger targets that topic, with the
// message delivered as the event (data + attributes).
func TestPublishInvokesEventTriggeredFunction(t *testing.T) {
	srv, cloud := newServerWithFunctions(t)

	var (
		mu       sync.Mutex
		payloads [][]byte
	)

	cloud.CloudFunctions.RegisterHandler("on-publish", func(_ context.Context, payload []byte) ([]byte, error) {
		mu.Lock()
		payloads = append(payloads, append([]byte(nil), payload...))
		mu.Unlock()

		return nil, nil
	})

	// Topic the function is triggered by.
	doRequest(t, srv, http.MethodPut, topicURL("fn-topic"), `{}`)

	// gen1 function with a Pub/Sub eventTrigger on that topic.
	fnPath := "/v1/projects/" + project + "/locations/us-central1/functions?functionId=on-publish"
	createFn := `{"eventTrigger":{"eventType":"google.pubsub.topic.publish",` +
		`"resource":"projects/` + project + `/topics/fn-topic","service":"pubsub.googleapis.com"}}`
	if resp := doRequest(t, srv, http.MethodPost, fnPath, createFn); resp.StatusCode != http.StatusOK {
		t.Fatalf("create function = %d: %s", resp.StatusCode, readBody(t, resp))
	}

	pub := doRequest(t, srv, http.MethodPost, topicURL("fn-topic")+":publish",
		`{"messages":[{"data":"`+base64.StdEncoding.EncodeToString([]byte("payload"))+
			`","attributes":{"a":"b"}}]}`)
	if pub.StatusCode != http.StatusOK {
		t.Fatalf("publish = %d: %s", pub.StatusCode, readBody(t, pub))
	}
	_ = readBody(t, pub)

	got := waitForBytes(t, &mu, &payloads, 1)

	var event pushMessage
	if err := json.Unmarshal(got[0], &event); err != nil {
		t.Fatalf("decode event: %v (raw %s)", err, got[0])
	}

	data, _ := base64.StdEncoding.DecodeString(event.Data)
	if string(data) != "payload" {
		t.Errorf("event data = %q, want payload", data)
	}

	if event.Attributes["a"] != "b" {
		t.Errorf("event attributes = %v", event.Attributes)
	}
}

// TestPublishNoFunctionNoPushStillWorks covers regression item (c): a topic with
// neither a push subscription nor an event-triggered function publishes fine.
func TestPublishNoFunctionNoPushStillWorks(t *testing.T) {
	srv, _ := newServerWithFunctions(t)

	doRequest(t, srv, http.MethodPut, topicURL("plain-topic"), `{}`)

	pub := doRequest(t, srv, http.MethodPost, topicURL("plain-topic")+":publish",
		`{"messages":[{"data":"`+base64.StdEncoding.EncodeToString([]byte("q"))+`"}]}`)
	if pub.StatusCode != http.StatusOK {
		t.Fatalf("publish = %d, want 200", pub.StatusCode)
	}

	var out struct {
		MessageIDs []string `json:"messageIds"`
	}
	if err := json.Unmarshal([]byte(readBody(t, pub)), &out); err != nil {
		t.Fatalf("decode publish: %v", err)
	}

	if len(out.MessageIDs) != 1 {
		t.Errorf("messageIds = %v, want 1", out.MessageIDs)
	}
}

func waitForPush(t *testing.T, mu *sync.Mutex, store *[]pushBody, want int) []pushBody {
	t.Helper()

	for range 100 {
		mu.Lock()
		n := len(*store)
		mu.Unlock()

		if n >= want {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(*store) < want {
		t.Fatalf("got %d push deliveries, want %d", len(*store), want)
	}

	return append([]pushBody(nil), *store...)
}

func waitForBytes(t *testing.T, mu *sync.Mutex, store *[][]byte, want int) [][]byte {
	t.Helper()

	for range 100 {
		mu.Lock()
		n := len(*store)
		mu.Unlock()

		if n >= want {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(*store) < want {
		t.Fatalf("got %d invocations, want %d", len(*store), want)
	}

	return append([][]byte(nil), *store...)
}
