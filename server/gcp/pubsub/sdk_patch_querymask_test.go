package pubsub_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	"google.golang.org/api/option"
	pubsubv1 "google.golang.org/api/pubsub/v1"
)

// newRawServer returns a running wire server plus its base URL for tests that
// must craft raw HTTP requests (terraform/gcloud send updateMask as a query
// parameter, which the typed Go SDK cannot express).
func newRawServer(t *testing.T) (*httptest.Server, *pubsubv1.Service) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{PubSub: cloud.PubSub}))
	t.Cleanup(ts.Close)

	svc, err := pubsubv1.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	return ts, svc
}

// rawPatch issues a PATCH with the updateMask supplied ONLY as a query
// parameter (never in the body) — exactly how terraform and gcloud drive
// Pub/Sub subscriptions.patch / topics.patch.
func rawPatch(t *testing.T, url, body string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPatch, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}

	return resp
}

// TestPubSubSubscriptionPatchQueryMask guards that subscriptions.patch honors an
// updateMask carried as a query parameter (terraform/gcloud style) with no mask
// in the JSON body. Without the fix the mask is empty and the server returns
// 400 "updateMask must be specified and non-empty", breaking terraform updates.
func TestPubSubSubscriptionPatchQueryMask(t *testing.T) {
	ts, svc := newRawServer(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/gcsq")
	mustSub(t, svc, "projects/demo/subscriptions/gcsq-sub", &pubsubv1.Subscription{
		Topic:              "projects/demo/topics/gcsq",
		AckDeadlineSeconds: 20,
		CloudStorageConfig: &pubsubv1.CloudStorageConfig{
			Bucket:         "my-bucket",
			FilenamePrefix: "old-",
		},
	})

	// terraform-style update: mask only in the query string, config in the body.
	url := ts.URL + "/v1/projects/demo/subscriptions/gcsq-sub?alt=json&updateMask=cloudStorageConfig"
	body := `{"subscription":{"cloudStorageConfig":{"bucket":"my-bucket","filenamePrefix":"new-"}}}`

	resp := rawPatch(t, url, body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("PATCH status = %d, want 200; body=%s", resp.StatusCode, b)
	}

	got, err := svc.Projects.Subscriptions.Get("projects/demo/subscriptions/gcsq-sub").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.CloudStorageConfig == nil || got.CloudStorageConfig.FilenamePrefix != "new-" {
		t.Fatalf("cloudStorageConfig.filenamePrefix after query-mask patch = %+v, want new-", got.CloudStorageConfig)
	}

	// Sibling field not named by the mask must be untouched.
	if got.AckDeadlineSeconds != 20 {
		t.Errorf("ackDeadlineSeconds = %d, want 20 (untouched sibling)", got.AckDeadlineSeconds)
	}
}

// TestPubSubTopicPatchQueryMask guards the same query-param updateMask path for
// topics.patch (google_pubsub_topic labels update).
func TestPubSubTopicPatchQueryMask(t *testing.T) {
	ts, svc := newRawServer(t)
	ctx := context.Background()

	if _, err := svc.Projects.Topics.Create("projects/demo/topics/tq",
		&pubsubv1.Topic{Labels: map[string]string{"env": "old", "keep": "yes"}}).Context(ctx).Do(); err != nil {
		t.Fatalf("Topic.Create: %v", err)
	}

	url := ts.URL + "/v1/projects/demo/topics/tq?alt=json&updateMask=labels"
	body := `{"topic":{"labels":{"env":"new","keep":"yes"}}}`

	resp := rawPatch(t, url, body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("PATCH status = %d, want 200; body=%s", resp.StatusCode, b)
	}

	got, err := svc.Projects.Topics.Get("projects/demo/topics/tq").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Labels["env"] != "new" {
		t.Errorf("labels[env] = %q after query-mask patch, want new", got.Labels["env"])
	}

	if got.Labels["keep"] != "yes" {
		t.Errorf("labels[keep] = %q, want yes (untouched sibling)", got.Labels["keep"])
	}
}

// TestPubSubSubscriptionPatchEmptyMask guards that a PATCH with neither a body
// nor a query updateMask still returns 400, matching real Pub/Sub.
func TestPubSubSubscriptionPatchEmptyMask(t *testing.T) {
	ts, svc := newRawServer(t)

	mustTopic(t, svc, "projects/demo/topics/em")
	mustSub(t, svc, "projects/demo/subscriptions/em-sub", &pubsubv1.Subscription{
		Topic: "projects/demo/topics/em",
	})

	url := ts.URL + "/v1/projects/demo/subscriptions/em-sub?alt=json"
	body := `{"subscription":{"ackDeadlineSeconds":30}}`

	resp := rawPatch(t, url, body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("PATCH with no mask status = %d, want 400; body=%s", resp.StatusCode, b)
	}

	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "updateMask") {
		t.Errorf("empty-mask 400 body = %s, want mention of updateMask", b)
	}
}
