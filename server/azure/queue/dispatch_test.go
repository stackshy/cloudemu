package queue_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/aztables"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azqueue"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

// TestNoShadowing registers Blob, Queue and Table on the same server and
// confirms each service's SDK routes to its own handler — i.e. the permissive
// Blob fallback does not swallow Queue or Table requests, and Queue and Table
// do not claim each other's or Blob's paths.
func TestNoShadowing(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		BlobStorage:  cloudP.BlobStorage,
		QueueStorage: cloudP.QueueStorage,
		TableStorage: cloudP.TableStorage,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	transport := policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}}

	// Blob: create a container.
	blobClient, err := azblob.NewClientWithNoCredential(ts.URL+"/", &azblob.ClientOptions{ClientOptions: transport})
	if err != nil {
		t.Fatalf("azblob client: %v", err)
	}

	if _, err := blobClient.CreateContainer(ctx, "cont", nil); err != nil {
		t.Fatalf("Blob CreateContainer routed wrong: %v", err)
	}

	// Note: the root GET /?comp=list shape (list-containers vs list-queues) is
	// byte-for-byte identical on the wire — Azure disambiguates it only by the
	// {account}.blob vs {account}.queue hostname, which a single test server
	// can't see. When Blob and Queue coexist behind one endpoint, that one
	// shape is inherently ambiguous; the Queue handler owns it (registered
	// first). Every other surface is disjoint, which is what this test asserts:
	// container/blob paths, /{queue}/messages, and OData table paths never
	// collide.

	// Blob: put+get a blob (a two-segment /{container}/{blob} path) — must
	// reach the Blob handler, not Queue or Table.
	if _, err := blobClient.UploadBuffer(ctx, "cont", "obj", []byte("data"), nil); err != nil {
		t.Fatalf("Blob UploadBuffer routed wrong: %v", err)
	}

	// Queue: create a queue named "cont" (same name as the container) and
	// enqueue — must hit the queue handler, not blob.
	qSvc, err := azqueue.NewServiceClientWithNoCredential(ts.URL+"/", &azqueue.ClientOptions{ClientOptions: transport})
	if err != nil {
		t.Fatalf("azqueue client: %v", err)
	}

	if _, err := qSvc.CreateQueue(ctx, "cont", nil); err != nil {
		t.Fatalf("Queue CreateQueue routed wrong: %v", err)
	}

	qClient, err := azqueue.NewQueueClientWithNoCredential(ts.URL+"/cont", &azqueue.ClientOptions{ClientOptions: transport})
	if err != nil {
		t.Fatalf("azqueue queue client: %v", err)
	}

	if _, err := qClient.EnqueueMessage(ctx, "m", nil); err != nil {
		t.Fatalf("Queue EnqueueMessage routed wrong: %v", err)
	}

	// Table: create a table named "cont" and insert — must hit the table
	// handler.
	tSvc, err := aztables.NewServiceClientWithNoCredential(ts.URL+"/", &aztables.ClientOptions{ClientOptions: transport})
	if err != nil {
		t.Fatalf("aztables client: %v", err)
	}

	if _, err := tSvc.CreateTable(ctx, "cont", nil); err != nil {
		t.Fatalf("Table CreateTable routed wrong: %v", err)
	}

	ent, _ := json.Marshal(map[string]any{"PartitionKey": "p", "RowKey": "r", "V": "1"})
	if _, err := tSvc.NewClient("cont").AddEntity(ctx, ent, nil); err != nil {
		t.Fatalf("Table AddEntity routed wrong: %v", err)
	}
}
