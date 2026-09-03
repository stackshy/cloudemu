package gcs_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	cloudfunctions2 "google.golang.org/api/cloudfunctions/v2"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	gcpprov "github.com/stackshy/cloudemu/v2/providers/gcp"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// fnTriggerProject / fnTriggerLocation name the project/location every gen2
// storage-triggered function in this file deploys under.
const (
	fnTriggerProject  = "p1"
	fnTriggerLocation = "us-central1"

	// gen2StorageFinalizedType / gen2StorageDeletedType are the CloudEvent type
	// strings a real gen2 Cloud Storage eventTrigger uses, matching what a
	// google_cloudfunctions2_function.event_trigger.event_type is deployed
	// with in production.
	gen2StorageFinalizedType = "google.cloud.storage.object.v1.finalized"
	gen2StorageDeletedType   = "google.cloud.storage.object.v1.deleted"
)

func fnTriggerParent() string {
	return "projects/" + fnTriggerProject + "/locations/" + fnTriggerLocation
}

// fnTriggerEnv boots the FULL production GCP server (every handler, as
// `cloudemu serve --providers gcp` does) with real Storage and Cloud
// Functions v2 clients, so the GCS -> Cloud Functions gen2 storage-trigger
// wiring in server/gcp/gcp.go is exercised exactly as a real user would hit
// it.
type fnTriggerEnv struct {
	ts      *httptest.Server
	cloud   *gcpprov.Provider
	storage *storage.Client
	cf      *cloudfunctions2.Service
}

func newFnTriggerEnv(t *testing.T) *fnTriggerEnv {
	t.Helper()

	cloud := cloudemu.NewGCP()
	ts := httptest.NewServer(gcpserver.NewFromProvider(cloud))
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

	cfSvc, err := cloudfunctions2.NewService(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("cloudfunctions2.NewService: %v", err)
	}

	return &fnTriggerEnv{ts: ts, cloud: cloud, storage: sc, cf: cfSvc}
}

func (e *fnTriggerEnv) createBucket(t *testing.T, name string) *storage.BucketHandle {
	t.Helper()

	b := e.storage.Bucket(name)
	if err := b.Create(context.Background(), fnTriggerProject, nil); err != nil {
		t.Fatalf("bucket.Create(%s): %v", name, err)
	}

	return b
}

// createStorageTriggerFunction deploys a gen2 function whose eventTrigger
// binds bucket via an eventFilters "bucket" attribute, mirroring how
// google_cloudfunctions2_function.event_trigger is configured for a Cloud
// Storage trigger.
func (e *fnTriggerEnv) createStorageTriggerFunction(t *testing.T, name, bucket, eventType string) {
	t.Helper()

	_, err := e.cf.Projects.Locations.Functions.Create(fnTriggerParent(), &cloudfunctions2.Function{
		BuildConfig: &cloudfunctions2.BuildConfig{Runtime: "go121", EntryPoint: "Handle"},
		EventTrigger: &cloudfunctions2.EventTrigger{
			EventType:    eventType,
			EventFilters: []*cloudfunctions2.EventFilter{{Attribute: "bucket", Value: bucket}},
		},
	}).FunctionId(name).Context(context.Background()).Do()
	if err != nil {
		t.Fatalf("Create gen2 storage function %s: %v", name, err)
	}
}

func (e *fnTriggerEnv) deleteFunction(t *testing.T, name string) {
	t.Helper()

	if _, err := e.cf.Projects.Locations.Functions.
		Delete(fnTriggerParent() + "/functions/" + name).Context(context.Background()).Do(); err != nil {
		t.Fatalf("Delete function %s: %v", name, err)
	}
}

// gen2StorageCloudEvent mirrors the structured-mode CloudEvent body a gen2
// Cloud Storage-triggered function receives (cloudfunctions.gen2StorageEvent,
// redeclared here since that's an unexported wire-package type).
type gen2StorageCloudEvent struct {
	SpecVersion     string `json:"specversion"`
	Type            string `json:"type"`
	Source          string `json:"source"`
	Subject         string `json:"subject"`
	ID              string `json:"id"`
	DataContentType string `json:"datacontenttype"`
	Data            struct {
		Kind        string `json:"kind"`
		Name        string `json:"name"`
		Bucket      string `json:"bucket"`
		Generation  string `json:"generation"`
		Size        string `json:"size"`
		ContentType string `json:"contentType"`
	} `json:"data"`
}

// uploadTriggerObject writes content to bucket/key with contentType through
// the real storage SDK writer.
func uploadTriggerObject(t *testing.T, b *storage.BucketHandle, key, contentType, content string) {
	t.Helper()

	w := b.Object(key).NewWriter(context.Background())
	w.ContentType = contentType

	if _, err := io.Copy(w, strings.NewReader(content)); err != nil {
		t.Fatalf("write %s: %v", key, err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close %s: %v", key, err)
	}
}

// waitForCount polls check (which must do its own locking) until it reports
// at least want or the deadline lapses, returning whatever count it last
// observed either way.
func waitForCount(timeout time.Duration, want int, check func() int) int {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if got := check(); got >= want {
			return got
		}

		time.Sleep(10 * time.Millisecond)
	}

	return check()
}

// TestGCSObjectFinalizeInvokesGen2StorageTrigger is the world-case delivery
// test: a gen2 function with a Cloud Storage eventTrigger (finalized, bound to
// a specific bucket via eventFilters) fires when an object is uploaded to
// that bucket, with the CloudEvent shape a real Eventarc-backed trigger
// delivers — data is the storage#object resource, never the object bytes.
func TestGCSObjectFinalizeInvokesGen2StorageTrigger(t *testing.T) {
	e := newFnTriggerEnv(t)
	b := e.createBucket(t, "b-fin-trigger")
	e.createStorageTriggerFunction(t, "on-finalize", "b-fin-trigger", gen2StorageFinalizedType)

	var (
		mu       sync.Mutex
		payloads [][]byte
	)

	e.cloud.CloudFunctions.RegisterHandler("on-finalize", func(_ context.Context, payload []byte) ([]byte, error) {
		mu.Lock()
		payloads = append(payloads, append([]byte(nil), payload...))
		mu.Unlock()

		return nil, nil
	})

	const body = "hello-world"

	uploadTriggerObject(t, b, "trigger.txt", "text/plain", body)

	if n := waitForCount(2*time.Second, 1, func() int {
		mu.Lock()
		defer mu.Unlock()

		return len(payloads)
	}); n < 1 {
		t.Fatal("gen2 storage-triggered function was not invoked by object upload")
	}

	mu.Lock()
	raw := append([]byte(nil), payloads[0]...)
	mu.Unlock()

	var evt gen2StorageCloudEvent
	if err := json.Unmarshal(raw, &evt); err != nil {
		t.Fatalf("decode CloudEvent: %v (raw %s)", err, raw)
	}

	if evt.SpecVersion != "1.0" {
		t.Errorf("specversion = %q, want 1.0", evt.SpecVersion)
	}

	if evt.Type != gen2StorageFinalizedType {
		t.Errorf("type = %q, want %s", evt.Type, gen2StorageFinalizedType)
	}

	if evt.Source != "//storage.googleapis.com/projects/_/buckets/b-fin-trigger" {
		t.Errorf("source = %q", evt.Source)
	}

	if evt.Subject != "objects/trigger.txt" {
		t.Errorf("subject = %q, want objects/trigger.txt", evt.Subject)
	}

	if evt.ID == "" {
		t.Error("id empty")
	}

	if evt.Data.Kind != "storage#object" || evt.Data.Bucket != "b-fin-trigger" || evt.Data.Name != "trigger.txt" {
		t.Errorf("data kind/bucket/name = %q/%q/%q", evt.Data.Kind, evt.Data.Bucket, evt.Data.Name)
	}

	if evt.Data.Size != strconv.Itoa(len(body)) {
		t.Errorf("data.size = %q, want %d", evt.Data.Size, len(body))
	}

	if evt.Data.ContentType != "text/plain" {
		t.Errorf("data.contentType = %q, want text/plain", evt.Data.ContentType)
	}
}

// TestGCSObjectDeleteInvokesGen2StorageTrigger covers the deleted event: a
// gen2 function bound to (bucket, deleted) fires on Object.Delete and not on
// the prior upload.
func TestGCSObjectDeleteInvokesGen2StorageTrigger(t *testing.T) {
	e := newFnTriggerEnv(t)
	b := e.createBucket(t, "b-del-trigger")
	e.createStorageTriggerFunction(t, "on-delete", "b-del-trigger", gen2StorageDeletedType)

	var (
		mu       sync.Mutex
		payloads [][]byte
	)

	e.cloud.CloudFunctions.RegisterHandler("on-delete", func(_ context.Context, payload []byte) ([]byte, error) {
		mu.Lock()
		payloads = append(payloads, append([]byte(nil), payload...))
		mu.Unlock()

		return nil, nil
	})

	uploadTriggerObject(t, b, "gone.txt", "text/plain", "bye")

	// The finalize event must not fire a function bound only to deleted.
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	preDelete := len(payloads)
	mu.Unlock()

	if preDelete != 0 {
		t.Fatalf("upload fired %d invocations, want 0 (only deleted is bound)", preDelete)
	}

	if err := b.Object("gone.txt").Delete(context.Background()); err != nil {
		t.Fatalf("Delete object: %v", err)
	}

	if n := waitForCount(2*time.Second, 1, func() int {
		mu.Lock()
		defer mu.Unlock()

		return len(payloads)
	}); n < 1 {
		t.Fatal("gen2 storage-triggered function was not invoked by object delete")
	}

	mu.Lock()
	raw := append([]byte(nil), payloads[0]...)
	mu.Unlock()

	var evt gen2StorageCloudEvent
	if err := json.Unmarshal(raw, &evt); err != nil {
		t.Fatalf("decode CloudEvent: %v", err)
	}

	if evt.Type != gen2StorageDeletedType {
		t.Errorf("type = %q, want %s", evt.Type, gen2StorageDeletedType)
	}

	if evt.Data.Name != "gone.txt" || evt.Data.Bucket != "b-del-trigger" {
		t.Errorf("data name/bucket = %q/%q", evt.Data.Name, evt.Data.Bucket)
	}
}

// TestGCSObjectFinalizeDoesNotFireForUnboundBucket confirms an object upload
// to a bucket no gen2 function's eventTrigger names never invokes it.
func TestGCSObjectFinalizeDoesNotFireForUnboundBucket(t *testing.T) {
	e := newFnTriggerEnv(t)
	e.createBucket(t, "b-bound")
	other := e.createBucket(t, "b-other")
	e.createStorageTriggerFunction(t, "on-bound", "b-bound", gen2StorageFinalizedType)

	var (
		mu          sync.Mutex
		invocations int
	)

	e.cloud.CloudFunctions.RegisterHandler("on-bound", func(_ context.Context, _ []byte) ([]byte, error) {
		mu.Lock()
		invocations++
		mu.Unlock()

		return nil, nil
	})

	uploadTriggerObject(t, other, "irrelevant.txt", "text/plain", "x")

	got := waitForCount(300*time.Millisecond, 1, func() int {
		mu.Lock()
		defer mu.Unlock()

		return invocations
	})
	if got != 0 {
		t.Fatalf("invocations = %d, want 0 (write to an unbound bucket must not fire)", got)
	}
}

// TestGCSObjectFinalizeDoesNotFireAfterFunctionDeleted confirms an upload
// after the gen2 function is deleted no longer invokes it.
func TestGCSObjectFinalizeDoesNotFireAfterFunctionDeleted(t *testing.T) {
	e := newFnTriggerEnv(t)
	b := e.createBucket(t, "b-deletable")
	e.createStorageTriggerFunction(t, "on-deletable", "b-deletable", gen2StorageFinalizedType)

	var (
		mu          sync.Mutex
		invocations int
	)

	e.cloud.CloudFunctions.RegisterHandler("on-deletable", func(_ context.Context, _ []byte) ([]byte, error) {
		mu.Lock()
		invocations++
		mu.Unlock()

		return nil, nil
	})

	e.deleteFunction(t, "on-deletable")

	uploadTriggerObject(t, b, "too-late.txt", "text/plain", "x")

	got := waitForCount(300*time.Millisecond, 1, func() int {
		mu.Lock()
		defer mu.Unlock()

		return invocations
	})
	if got != 0 {
		t.Fatalf("invocations = %d, want 0 (a deleted function must not fire)", got)
	}
}

// doUploadWithDepth PUTs an object via the media-upload endpoint, stamping
// recursionguard.DepthHeader with depth. depth 0 omits any header effect
// (equivalent to an ordinary top-level request), matching how a fresh chain
// of requests starts at depth 0.
func doUploadWithDepth(t *testing.T, ts *httptest.Server, bucket, name, contentType, body string, depth int) {
	t.Helper()

	url := ts.URL + "/upload/storage/v1/b/" + bucket + "/o?uploadType=media&name=" + name

	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", contentType)
	req.Header.Set(recursionguard.DepthHeader, strconv.Itoa(depth))

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload = %d: %s", resp.StatusCode, b)
	}
}

// TestGCSStorageTriggerRecursionBounded confirms a gen2 function that writes
// back to its own trigger bucket terminates at recursionguard.MaxDepth
// instead of recursing unbounded. Invoking a gen2 function is in-process
// (InvokeForObjectEvent -> Handler.Invoke), but the write-back a real deployed
// function's storage SDK call performs is a fresh network hop back into this
// same server, so the handler below plays the role production delivery code
// plays elsewhere (e.g. Event Grid webhook delivery): it forwards the
// ctx-carried depth across that hop via recursionguard.DepthHeader, exactly
// as a well-behaved real function's SDK call crossing back into the emulator
// would need to for the guard to keep counting instead of resetting to zero
// each hop.
func TestGCSStorageTriggerRecursionBounded(t *testing.T) {
	e := newFnTriggerEnv(t)

	const bucket = "b-loopback"

	e.createBucket(t, bucket)
	e.createStorageTriggerFunction(t, "on-loopback", bucket, gen2StorageFinalizedType)

	var (
		mu          sync.Mutex
		invocations int
	)

	e.cloud.CloudFunctions.RegisterHandler("on-loopback", func(ctx context.Context, _ []byte) ([]byte, error) {
		mu.Lock()
		invocations++
		mu.Unlock()

		doUploadWithDepth(t, e.ts, bucket, "loop.txt", "text/plain", "again", recursionguard.Depth(ctx))

		return nil, nil
	})

	doUploadWithDepth(t, e.ts, bucket, "loop.txt", "text/plain", "start", 0)

	got := waitForCount(3*time.Second, recursionguard.MaxDepth, func() int {
		mu.Lock()
		defer mu.Unlock()

		return invocations
	})
	if got != recursionguard.MaxDepth {
		t.Fatalf("handler invoked %d times, want exactly %d (recursive-loop guard did not bound the chain)",
			got, recursionguard.MaxDepth)
	}
}
