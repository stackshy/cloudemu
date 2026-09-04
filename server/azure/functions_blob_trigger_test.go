package azure_test

// Real-user end-to-end proof that an Azure Functions blobTrigger binding
// actually fires: a function app declares a blobTrigger binding whose path
// names a container via ARM, then a blob written to that container with the
// real azblob SDK synchronously invokes the function with the blob's content
// as its payload. This is the Blob Storage counterpart of
// TestQueueStorageTriggerInvokesFunction (#997) and
// TestServiceBusTopicTriggerInvokesFunction (#1001): before this wiring a
// blobTrigger binding round-tripped as CRUD only and no blob write ever
// reached the function. Real Azure's blobTrigger fires on blob create/update
// only, never on delete.

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
)

// blobFuncAppBase returns the ARM base path of a function app used by these
// tests.
func blobFuncAppBase(app string) string {
	return "/subscriptions/sub-bt/resourceGroups/rg-bt/providers/Microsoft.Web/sites/" + app
}

// createBlobTriggeredApp creates a function app plus one deployed function
// declaring a blobTrigger binding on path.
func createBlobTriggeredApp(t *testing.T, ts *httptest.Server, app, path string) {
	t.Helper()

	base := blobFuncAppBase(app)
	armPut(t, ts, base+"?api-version=2022-03-01", `{"location":"eastus","properties":{"siteConfig":{}}}`)
	armPut(t, ts, base+"/functions/consume?api-version=2022-03-01",
		`{"properties":{"config":{"bindings":[`+
			`{"name":"blob","type":"blobTrigger","direction":"in",`+
			`"path":"`+path+`","connection":"AzureWebJobsStorage"}]}}}`)
}

// newBlobClient returns a real azblob client pointed at ts.
func newBlobClient(t *testing.T, ts *httptest.Server) *azblob.Client {
	t.Helper()

	client, err := azblob.NewClientWithNoCredential(ts.URL+"/", &azblob.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	if err != nil {
		t.Fatalf("azblob.NewClientWithNoCredential: %v", err)
	}

	return client
}

func TestBlobStorageTriggerInvokesFunction(t *testing.T) {
	ts, p := newFullAzureServerWithProvider(t)
	ctx := context.Background()

	const (
		app       = "bt-app"
		container = "images"
	)

	createBlobTriggeredApp(t, ts, app, container+"/{name}")

	got := make(chan string, 1)
	p.Functions.RegisterHandler(app, func(_ context.Context, payload []byte) ([]byte, error) {
		select {
		case got <- string(payload):
		default:
		}

		return payload, nil
	})

	blobClient := newBlobClient(t, ts)
	if _, err := blobClient.CreateContainer(ctx, container, nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	const content = "cat-bytes"
	if _, err := blobClient.UploadBuffer(ctx, container, "cat.png", []byte(content), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	// Delivery is synchronous, so the function has fired by the time the upload
	// returns.
	select {
	case body := <-got:
		if body != content {
			t.Fatalf("function received %q, want %q", body, content)
		}
	default:
		t.Fatal("blob-triggered function was not invoked")
	}
}

// TestBlobStorageTriggerUnboundContainerDoesNotFire proves a blob written to a
// container no function is bound to invokes nothing (the write still
// succeeds).
func TestBlobStorageTriggerUnboundContainerDoesNotFire(t *testing.T) {
	ts, p := newFullAzureServerWithProvider(t)
	ctx := context.Background()

	const app = "bt-app2"

	createBlobTriggeredApp(t, ts, app, "bound-container/{name}")

	fired := make(chan struct{}, 1)
	p.Functions.RegisterHandler(app, func(_ context.Context, payload []byte) ([]byte, error) {
		fired <- struct{}{}

		return payload, nil
	})

	blobClient := newBlobClient(t, ts)
	if _, err := blobClient.CreateContainer(ctx, "other-container", nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	if _, err := blobClient.UploadBuffer(ctx, "other-container", "x", []byte("x"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	select {
	case <-fired:
		t.Fatal("function fired for a container it is not bound to")
	default:
	}
}

// TestBlobStorageTriggerDeleteDoesNotFire proves that, matching real Azure, a
// blob DELETE never fires a blobTrigger: the bound function fires once on
// create, then not again when that same blob is deleted.
func TestBlobStorageTriggerDeleteDoesNotFire(t *testing.T) {
	ts, p := newFullAzureServerWithProvider(t)
	ctx := context.Background()

	const (
		app       = "bt-app3"
		container = "images3"
	)

	createBlobTriggeredApp(t, ts, app, container+"/{name}")

	var invocations int32

	p.Functions.RegisterHandler(app, func(_ context.Context, payload []byte) ([]byte, error) {
		atomic.AddInt32(&invocations, 1)

		return payload, nil
	})

	blobClient := newBlobClient(t, ts)
	if _, err := blobClient.CreateContainer(ctx, container, nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	if _, err := blobClient.UploadBuffer(ctx, container, "k", []byte("v"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	if got := atomic.LoadInt32(&invocations); got != 1 {
		t.Fatalf("invocations after create = %d, want 1", got)
	}

	if _, err := blobClient.DeleteBlob(ctx, container, "k", nil); err != nil {
		t.Fatalf("DeleteBlob: %v", err)
	}

	if got := atomic.LoadInt32(&invocations); got != 1 {
		t.Fatalf("invocations after delete = %d, want it to stay at 1 (delete must not fire a blobTrigger)", got)
	}
}

// TestBlobStorageTriggerOverwriteFires proves a blob UPDATE (overwrite) fires
// the trigger again, matching real Azure's blobTrigger create-AND-update
// semantics.
func TestBlobStorageTriggerOverwriteFires(t *testing.T) {
	ts, p := newFullAzureServerWithProvider(t)
	ctx := context.Background()

	const (
		app       = "bt-app4"
		container = "images4"
	)

	createBlobTriggeredApp(t, ts, app, container+"/{name}")

	var invocations int32

	p.Functions.RegisterHandler(app, func(_ context.Context, payload []byte) ([]byte, error) {
		atomic.AddInt32(&invocations, 1)

		return payload, nil
	})

	blobClient := newBlobClient(t, ts)
	if _, err := blobClient.CreateContainer(ctx, container, nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	if _, err := blobClient.UploadBuffer(ctx, container, "k", []byte("v1"), nil); err != nil {
		t.Fatalf("first UploadBuffer: %v", err)
	}

	if _, err := blobClient.UploadBuffer(ctx, container, "k", []byte("v2"), nil); err != nil {
		t.Fatalf("second UploadBuffer (overwrite): %v", err)
	}

	if got := atomic.LoadInt32(&invocations); got != 2 {
		t.Fatalf("invocations after create+overwrite = %d, want 2", got)
	}
}

// TestBlobStorageTriggerDisabledFunctionSkipped proves a disabled deployed
// function does not fire even though its binding matches the written
// container.
func TestBlobStorageTriggerDisabledFunctionSkipped(t *testing.T) {
	ts, p := newFullAzureServerWithProvider(t)
	ctx := context.Background()

	const (
		app       = "bt-disabled-app"
		container = "images5"
	)

	base := blobFuncAppBase(app)
	armPut(t, ts, base+"?api-version=2022-03-01", `{"location":"eastus","properties":{"siteConfig":{}}}`)
	armPut(t, ts, base+"/functions/consume?api-version=2022-03-01",
		`{"properties":{"isDisabled":true,"config":{"bindings":[`+
			`{"name":"blob","type":"blobTrigger","direction":"in",`+
			`"path":"`+container+`/{name}","connection":"AzureWebJobsStorage"}]}}}`)

	fired := make(chan struct{}, 1)
	p.Functions.RegisterHandler(app, func(_ context.Context, payload []byte) ([]byte, error) {
		fired <- struct{}{}

		return payload, nil
	})

	blobClient := newBlobClient(t, ts)
	if _, err := blobClient.CreateContainer(ctx, container, nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	if _, err := blobClient.UploadBuffer(ctx, container, "k", []byte("v"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	select {
	case <-fired:
		t.Fatal("a disabled function must not fire")
	default:
	}
}

// TestBlobStorageTriggerRecursionGuard proves a function that writes back into
// its own trigger container terminates at recursionguard.MaxDepth rather than
// recursing unbounded, mirroring TestServiceBusTopicTriggerRecursionGuard. The
// handler forwards the ctx it was invoked with into its own PutObject call (a
// direct provider call, not a fresh HTTP round trip) — that ctx-carried depth
// is the channel the guard rides on.
func TestBlobStorageTriggerRecursionGuard(t *testing.T) {
	ts, p := newFullAzureServerWithProvider(t)
	ctx := context.Background()

	const (
		app       = "bt-loop-app"
		container = "loop-container"
	)

	createBlobTriggeredApp(t, ts, app, container+"/{name}")

	blobClient := newBlobClient(t, ts)
	if _, err := blobClient.CreateContainer(ctx, container, nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	var invocations int32

	p.Functions.RegisterHandler(app, func(ctx context.Context, payload []byte) ([]byte, error) {
		atomic.AddInt32(&invocations, 1)

		err := p.BlobStorage.PutObject(ctx, container, "again.txt", payload, "text/plain", nil)

		return payload, err
	})

	// The single top-level write that starts the chain.
	if _, err := blobClient.UploadBuffer(ctx, container, "seed.txt", []byte("seed"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	if got := atomic.LoadInt32(&invocations); got != int32(recursionguard.MaxDepth) {
		t.Fatalf("handler invoked %d times, want exactly %d (recursive-loop guard did not bound the chain)",
			got, recursionguard.MaxDepth)
	}
}

// TestBlobAndQueueStorageTriggersCoexist proves the #997 Queue Storage
// queueTrigger path still fires unchanged alongside a new blobTrigger, with no
// cross-fire between them.
func TestBlobAndQueueStorageTriggersCoexist(t *testing.T) {
	ts, p := newFullAzureServerWithProvider(t)
	ctx := context.Background()

	const (
		queueApp  = "bt-queue-app"
		queue     = "jobs"
		blobApp   = "bt-blob-app"
		container = "images6"
	)

	createQueueStorageTriggeredApp(t, ts, queueApp, queue)
	createBlobTriggeredApp(t, ts, blobApp, container+"/{name}")

	queueGot := make(chan string, 1)
	p.Functions.RegisterHandler(queueApp, func(_ context.Context, payload []byte) ([]byte, error) {
		queueGot <- string(payload)
		return payload, nil
	})

	blobGot := make(chan string, 1)
	p.Functions.RegisterHandler(blobApp, func(_ context.Context, payload []byte) ([]byte, error) {
		blobGot <- string(payload)
		return payload, nil
	})

	createStorageQueue(t, ts, queue)
	blobClient := newBlobClient(t, ts)

	if _, err := blobClient.CreateContainer(ctx, container, nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	qc := newStorageQueueClient(t, ts, queue)
	if _, err := qc.EnqueueMessage(ctx, "queue-payload", nil); err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}

	if _, err := blobClient.UploadBuffer(ctx, container, "k", []byte("blob-payload"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	select {
	case body := <-queueGot:
		if body != "queue-payload" {
			t.Fatalf("queue-bound function received %q, want queue-payload", body)
		}
	default:
		t.Fatal("queue-bound queueTrigger function was not invoked (#997 regression)")
	}

	select {
	case body := <-blobGot:
		if body != "blob-payload" {
			t.Fatalf("blob-bound function received %q, want blob-payload", body)
		}
	default:
		t.Fatal("blob-bound blobTrigger function was not invoked")
	}

	// Neither function received the other's payload.
	select {
	case <-queueGot:
		t.Fatal("queue-bound function fired a second time (cross-fire from the blob write)")
	default:
	}

	select {
	case <-blobGot:
		t.Fatal("blob-bound function fired a second time (cross-fire from the queue publish)")
	default:
	}
}
