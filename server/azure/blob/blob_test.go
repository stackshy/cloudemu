package blob_test

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

// TestSDKBlobRoundTrip drives Azure Blob storage operations with the real
// azblob client against our handler. azblob requires either a SharedKey, SAS,
// or anonymous access — we use anonymous so the test doesn't have to forge
// signatures.
func TestSDKBlobRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{BlobStorage: cloudP.BlobStorage})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	clientOpts := &azblob.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	svcClient, err := azblob.NewClientWithNoCredential(ts.URL+"/", clientOpts)
	if err != nil {
		t.Fatalf("NewClientWithNoCredential: %v", err)
	}

	// Create container.
	if _, err := svcClient.CreateContainer(ctx, "c1", nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	// Upload blob.
	body := []byte("hello, azure blob")
	if _, err := svcClient.UploadBuffer(ctx, "c1", "k1", body, &azblob.UploadBufferOptions{
		Metadata: map[string]*string{
			"author": ptrStr("cloudemu"),
		},
	}); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	// Download blob and verify content.
	dl, err := svcClient.DownloadStream(ctx, "c1", "k1", nil)
	if err != nil {
		t.Fatalf("DownloadStream: %v", err)
	}

	got, err := io.ReadAll(dl.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if !bytes.Equal(got, body) {
		t.Errorf("body mismatch: got=%q want=%q", got, body)
	}

	// List containers via the service-level client.
	{
		fullSvc, err := service.NewClientWithNoCredential(ts.URL+"/", &service.ClientOptions{
			ClientOptions: policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
		})
		if err != nil {
			t.Fatalf("service.NewClient: %v", err)
		}

		pager := fullSvc.NewListContainersPager(nil)

		seen := map[string]bool{}
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				t.Fatalf("ListContainers: %v", err)
			}

			for _, c := range page.ContainerItems {
				if c.Name != nil {
					seen[*c.Name] = true
				}
			}
		}

		if !seen["c1"] {
			t.Errorf("c1 not in container list: %v", seen)
		}
	}

	// List blobs in the container via container client.
	{
		cClient, err := container.NewClientWithNoCredential(ts.URL+"/c1", &container.ClientOptions{
			ClientOptions: policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
		})
		if err != nil {
			t.Fatalf("container.NewClient: %v", err)
		}

		pager := cClient.NewListBlobsFlatPager(nil)

		seen := map[string]bool{}
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				t.Fatalf("ListBlobs: %v", err)
			}

			for _, b := range page.Segment.BlobItems {
				if b.Name != nil {
					seen[*b.Name] = true
				}
			}
		}

		if !seen["k1"] {
			t.Errorf("k1 not in blob list: %v", seen)
		}
	}

	// Head blob via blockblob client.
	{
		bbClient, err := blockblob.NewClientWithNoCredential(ts.URL+"/c1/k1", &blockblob.ClientOptions{
			ClientOptions: policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
		})
		if err != nil {
			t.Fatalf("blockblob.NewClient: %v", err)
		}

		props, err := bbClient.GetProperties(ctx, nil)
		if err != nil {
			t.Fatalf("GetProperties: %v", err)
		}

		if props.ContentLength == nil || *props.ContentLength != int64(len(body)) {
			t.Errorf("ContentLength=%v want %d", props.ContentLength, len(body))
		}
	}

	// Delete blob and container.
	if _, err := svcClient.DeleteBlob(ctx, "c1", "k1", nil); err != nil {
		t.Errorf("DeleteBlob: %v", err)
	}

	if _, err := svcClient.DeleteContainer(ctx, "c1", nil); err != nil {
		t.Errorf("DeleteContainer: %v", err)
	}

	// Confirm 404 after delete.
	if _, err := svcClient.DownloadStream(ctx, "c1", "k1", nil); err == nil ||
		!strings.Contains(err.Error(), "BlobNotFound") {
		t.Errorf("expected BlobNotFound after delete, got %v", err)
	}
}

func ptrStr(s string) *string { return &s }
