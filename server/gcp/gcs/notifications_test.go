package gcs_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
	pubsubv1 "google.golang.org/api/pubsub/v1"

	"github.com/stackshy/cloudemu/v2"
	gcpprov "github.com/stackshy/cloudemu/v2/providers/gcp"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const notifProject = "p1"

// notifEnv wires a GCP server with Storage + PubSub + CloudFunctions so the
// GCS -> Pub/Sub -> Cloud Functions notification chain is fully connected.
type notifEnv struct {
	ts      *httptest.Server
	cloud   *gcpprov.Provider
	storage *storage.Client
	pubsub  *pubsubv1.Service
}

func newNotifEnv(t *testing.T) *notifEnv {
	t.Helper()

	cloud := cloudemu.NewGCP()
	ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{
		Storage:        cloud.GCS,
		PubSub:         cloud.PubSub,
		CloudFunctions: cloud.CloudFunctions,
		Firestore:      cloud.Firestore,
	}))
	t.Cleanup(ts.Close)

	ctx := context.Background()

	sc, err := storage.NewClient(ctx,
		option.WithEndpoint(ts.URL+"/storage/v1/"),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("storage.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = sc.Close() })

	psvc, err := pubsubv1.NewService(ctx,
		option.WithEndpoint(ts.URL+"/"),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("pubsubv1.NewService: %v", err)
	}

	return &notifEnv{ts: ts, cloud: cloud, storage: sc, pubsub: psvc}
}

func (e *notifEnv) createBucket(t *testing.T, name string) *storage.BucketHandle {
	t.Helper()

	b := e.storage.Bucket(name)
	if err := b.Create(context.Background(), notifProject, nil); err != nil {
		t.Fatalf("bucket.Create(%s): %v", name, err)
	}

	return b
}

func (e *notifEnv) createTopic(t *testing.T, id string) string {
	t.Helper()

	name := "projects/" + notifProject + "/topics/" + id
	if _, err := e.pubsub.Projects.Topics.Create(name, &pubsubv1.Topic{}).Do(); err != nil {
		t.Fatalf("topics.Create(%s): %v", id, err)
	}

	return name
}

func (e *notifEnv) createSubscription(t *testing.T, id, topic string) string {
	t.Helper()

	name := "projects/" + notifProject + "/subscriptions/" + id
	if _, err := e.pubsub.Projects.Subscriptions.Create(name, &pubsubv1.Subscription{Topic: topic}).Do(); err != nil {
		t.Fatalf("subscriptions.Create(%s): %v", id, err)
	}

	return name
}

// pullMessages polls a subscription until it has at least want messages or the
// deadline lapses.
func (e *notifEnv) pullMessages(t *testing.T, sub string, want int) []*pubsubv1.ReceivedMessage {
	t.Helper()

	var got []*pubsubv1.ReceivedMessage

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := e.pubsub.Projects.Subscriptions.Pull(sub, &pubsubv1.PullRequest{
			MaxMessages: 10, ReturnImmediately: true,
		}).Do()
		if err != nil {
			t.Fatalf("subscriptions.Pull: %v", err)
		}

		got = append(got, resp.ReceivedMessages...)
		if len(got) >= want {
			return got
		}

		time.Sleep(10 * time.Millisecond)
	}

	return got
}

func uploadObject(t *testing.T, b *storage.BucketHandle, key, content string) {
	t.Helper()

	w := b.Object(key).NewWriter(context.Background())
	w.ContentType = "text/plain"

	if _, err := io.Copy(w, strings.NewReader(content)); err != nil {
		t.Fatalf("write %s: %v", key, err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close %s: %v", key, err)
	}
}

// TestNotificationConfigCRUD covers (a): AddNotification is listed by
// Notifications(); delete removes it.
func TestNotificationConfigCRUD(t *testing.T) {
	e := newNotifEnv(t)
	b := e.createBucket(t, "b-crud")
	e.createTopic(t, "t-crud")

	ctx := context.Background()

	created, err := b.AddNotification(ctx, &storage.Notification{
		TopicProjectID:   notifProject,
		TopicID:          "t-crud",
		PayloadFormat:    storage.JSONPayload,
		EventTypes:       []string{storage.ObjectFinalizeEvent},
		ObjectNamePrefix: "logs/",
		CustomAttributes: map[string]string{"team": "core"},
	})
	if err != nil {
		t.Fatalf("AddNotification: %v", err)
	}

	if created.ID == "" {
		t.Fatal("AddNotification returned empty ID")
	}

	list, err := b.Notifications(ctx)
	if err != nil {
		t.Fatalf("Notifications: %v", err)
	}

	got, ok := list[created.ID]
	if !ok {
		t.Fatalf("notification %s not listed (got %v)", created.ID, list)
	}

	if got.TopicID != "t-crud" || got.PayloadFormat != storage.JSONPayload {
		t.Errorf("round-trip mismatch: topic=%q format=%q", got.TopicID, got.PayloadFormat)
	}

	if got.ObjectNamePrefix != "logs/" || got.CustomAttributes["team"] != "core" {
		t.Errorf("prefix/attrs not round-tripped: %+v", got)
	}

	if err := b.DeleteNotification(ctx, created.ID); err != nil {
		t.Fatalf("DeleteNotification: %v", err)
	}

	list, err = b.Notifications(ctx)
	if err != nil {
		t.Fatalf("Notifications after delete: %v", err)
	}

	if _, ok := list[created.ID]; ok {
		t.Errorf("notification %s still present after delete", created.ID)
	}
}

// TestNotificationFinalizeDelivered covers (b): an OBJECT_FINALIZE event with
// JSON_API_V1 data lands on a subscription of the configured topic.
func TestNotificationFinalizeDelivered(t *testing.T) {
	e := newNotifEnv(t)
	b := e.createBucket(t, "b-fin")
	topic := e.createTopic(t, "t-fin")
	sub := e.createSubscription(t, "s-fin", topic)

	if _, err := b.AddNotification(context.Background(), &storage.Notification{
		TopicProjectID: notifProject,
		TopicID:        "t-fin",
		PayloadFormat:  storage.JSONPayload,
		EventTypes:     []string{storage.ObjectFinalizeEvent},
	}); err != nil {
		t.Fatalf("AddNotification: %v", err)
	}

	uploadObject(t, b, "file.txt", "hello")

	msgs := e.pullMessages(t, sub, 1)
	if len(msgs) == 0 {
		t.Fatal("no OBJECT_FINALIZE message delivered")
	}

	m := msgs[0].Message
	if m.Attributes["eventType"] != storage.ObjectFinalizeEvent {
		t.Errorf("eventType = %q, want OBJECT_FINALIZE", m.Attributes["eventType"])
	}

	if m.Attributes["objectId"] != "file.txt" || m.Attributes["bucketId"] != "b-fin" {
		t.Errorf("objectId/bucketId attrs = %v", m.Attributes)
	}

	if m.Attributes["payloadFormat"] != storage.JSONPayload {
		t.Errorf("payloadFormat = %q", m.Attributes["payloadFormat"])
	}

	// JSON_API_V1 carries the object resource as the message data.
	raw, err := base64.StdEncoding.DecodeString(m.Data)
	if err != nil {
		t.Fatalf("decode data: %v", err)
	}

	var obj struct {
		Kind, Name, Bucket string
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal object resource: %v (raw %s)", err, raw)
	}

	if obj.Kind != "storage#object" || obj.Name != "file.txt" || obj.Bucket != "b-fin" {
		t.Errorf("object resource payload = %+v", obj)
	}
}

// TestNotificationDeleteEvent covers (c): OBJECT_DELETE fires only when
// configured.
func TestNotificationDeleteEvent(t *testing.T) {
	e := newNotifEnv(t)
	b := e.createBucket(t, "b-del")
	topic := e.createTopic(t, "t-del")
	sub := e.createSubscription(t, "s-del", topic)

	if _, err := b.AddNotification(context.Background(), &storage.Notification{
		TopicProjectID: notifProject,
		TopicID:        "t-del",
		PayloadFormat:  storage.JSONPayload,
		EventTypes:     []string{storage.ObjectDeleteEvent},
	}); err != nil {
		t.Fatalf("AddNotification: %v", err)
	}

	ctx := context.Background()

	// Upload must NOT fire (only OBJECT_DELETE is configured).
	uploadObject(t, b, "gone.txt", "bye")
	if msgs := e.pullMessages(t, sub, 1); len(msgs) != 0 {
		t.Fatalf("upload fired %d messages, want 0 (filter drops OBJECT_FINALIZE)", len(msgs))
	}

	if err := b.Object("gone.txt").Delete(ctx); err != nil {
		t.Fatalf("Delete object: %v", err)
	}

	msgs := e.pullMessages(t, sub, 1)
	if len(msgs) == 0 {
		t.Fatal("no OBJECT_DELETE message delivered")
	}

	if et := msgs[0].Message.Attributes["eventType"]; et != storage.ObjectDeleteEvent {
		t.Errorf("eventType = %q, want OBJECT_DELETE", et)
	}
}

// TestNotificationPrefixFilter covers (c): object_name_prefix gates delivery.
func TestNotificationPrefixFilter(t *testing.T) {
	e := newNotifEnv(t)
	b := e.createBucket(t, "b-pre")
	topic := e.createTopic(t, "t-pre")
	sub := e.createSubscription(t, "s-pre", topic)

	if _, err := b.AddNotification(context.Background(), &storage.Notification{
		TopicProjectID:   notifProject,
		TopicID:          "t-pre",
		PayloadFormat:    storage.NoPayload,
		ObjectNamePrefix: "logs/",
	}); err != nil {
		t.Fatalf("AddNotification: %v", err)
	}

	uploadObject(t, b, "data/x.txt", "no")
	if msgs := e.pullMessages(t, sub, 1); len(msgs) != 0 {
		t.Fatalf("non-matching prefix fired %d messages, want 0", len(msgs))
	}

	uploadObject(t, b, "logs/y.txt", "yes")

	msgs := e.pullMessages(t, sub, 1)
	if len(msgs) == 0 {
		t.Fatal("matching-prefix upload delivered no message")
	}

	if id := msgs[0].Message.Attributes["objectId"]; id != "logs/y.txt" {
		t.Errorf("objectId = %q, want logs/y.txt", id)
	}

	// NONE payload => no data body.
	if msgs[0].Message.Data != "" {
		t.Errorf("NONE payload carried data: %q", msgs[0].Message.Data)
	}
}

// TestNotificationInvokesFunction covers (d): the end-to-end
// GCS -> Pub/Sub -> Cloud Function chain — an object upload invokes a function
// event-triggered on the notification topic.
func TestNotificationInvokesFunction(t *testing.T) {
	e := newNotifEnv(t)
	b := e.createBucket(t, "b-fn")
	e.createTopic(t, "t-fn")

	var (
		mu       sync.Mutex
		payloads [][]byte
	)

	e.cloud.CloudFunctions.RegisterHandler("on-object", func(_ context.Context, payload []byte) ([]byte, error) {
		mu.Lock()
		payloads = append(payloads, append([]byte(nil), payload...))
		mu.Unlock()

		return nil, nil
	})

	createEventFunction(t, e.ts, "on-object", "projects/"+notifProject+"/topics/t-fn")

	if _, err := b.AddNotification(context.Background(), &storage.Notification{
		TopicProjectID: notifProject,
		TopicID:        "t-fn",
		PayloadFormat:  storage.JSONPayload,
		EventTypes:     []string{storage.ObjectFinalizeEvent},
	}); err != nil {
		t.Fatalf("AddNotification: %v", err)
	}

	uploadObject(t, b, "trigger.txt", "go")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(payloads)
		mu.Unlock()

		if n >= 1 {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("cloud function was not invoked by object upload")
}

// TestNotificationNilPubSubStillUploads covers (e): a Storage-only server (no
// Pub/Sub wired) still serves object CRUD without panicking. notificationConfigs
// is still served (backed by the storage driver), just never delivered.
func TestNotificationNilPubSubStillUploads(t *testing.T) {
	cloud := cloudemu.NewGCP()
	ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{Storage: cloud.GCS}))
	t.Cleanup(ts.Close)

	ctx := context.Background()

	sc, err := storage.NewClient(ctx,
		option.WithEndpoint(ts.URL+"/storage/v1/"),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("storage.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = sc.Close() })

	b := sc.Bucket("b-nil")
	if err := b.Create(ctx, notifProject, nil); err != nil {
		t.Fatalf("bucket.Create: %v", err)
	}

	// A notification config referencing a topic that will never deliver.
	if _, err := b.AddNotification(ctx, &storage.Notification{
		TopicProjectID: notifProject,
		TopicID:        "ghost",
		PayloadFormat:  storage.JSONPayload,
	}); err != nil {
		t.Fatalf("AddNotification: %v", err)
	}

	// Object CRUD must still succeed with no Pub/Sub publisher wired.
	uploadObject(t, b, "safe.txt", "ok")

	rd, err := b.Object("safe.txt").NewReader(ctx)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	defer func() { _ = rd.Close() }()

	body, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if string(body) != "ok" {
		t.Errorf("body = %q, want ok", body)
	}

	if err := b.Object("safe.txt").Delete(ctx); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

// createEventFunction creates a gen1 Cloud Function with a Pub/Sub eventTrigger
// on topic, via the Cloud Functions REST API.
func createEventFunction(t *testing.T, ts *httptest.Server, name, topic string) {
	t.Helper()

	url := ts.URL + "/v1/projects/" + notifProject + "/locations/us-central1/functions?functionId=" + name
	body := `{"eventTrigger":{"eventType":"google.pubsub.topic.publish",` +
		`"resource":"` + topic + `","service":"pubsub.googleapis.com"}}`

	resp, err := ts.Client().Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("create function: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create function = %d: %s", resp.StatusCode, b)
	}
}
