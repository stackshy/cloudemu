package azure

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestAzureStorageCompat drives an Azure Blob container + blob lifecycle
// through the real azure-sdk-for-go azblob client. Azure containers/blobs map
// onto the portable "storage" driver, so operation names match S3's in
// docs/coverage/coverage.json (container = bucket, blob = object).
func TestAzureStorageCompat(t *testing.T) {
	provider := cloudemu.NewAzure()
	sess := compat.BootAzure(t, azureserver.Drivers{BlobStorage: provider.BlobStorage})

	client, err := sess.BlobClient()
	if err != nil {
		t.Fatalf("blob client: %v", err)
	}

	ctx := context.Background()

	const (
		svc       = "storage"
		container = "compat-container"
		blob      = "greeting.txt"
	)

	body := []byte("hello cloudemu")

	sess.Op(svc, "CreateBucket", func() error {
		_, err := client.CreateContainer(ctx, container, nil)
		return err
	})

	sess.Op(svc, "ListBuckets", func() error {
		pager := client.NewListContainersPager(nil)

		_, err := pager.NextPage(ctx)

		return err
	})

	sess.Op(svc, "PutObject", func() error {
		_, err := client.UploadBuffer(ctx, container, blob, body, nil)
		return err
	})

	sess.Op(svc, "GetObject", func() error {
		resp, err := client.DownloadStream(ctx, container, blob, nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		got, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		if !bytes.Equal(got, body) {
			return fmt.Errorf("blob round-trip mismatch: got %q want %q", got, body)
		}

		return nil
	})

	sess.Op(svc, "HeadObject", func() error {
		blobClient := client.ServiceClient().NewContainerClient(container).NewBlobClient(blob)

		_, err := blobClient.GetProperties(ctx, nil)

		return err
	})

	sess.Op(svc, "ListObjects", func() error {
		pager := client.NewListBlobsFlatPager(container, nil)

		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}

		if len(page.Segment.BlobItems) != 1 {
			return fmt.Errorf("expected 1 blob, got %d", len(page.Segment.BlobItems))
		}

		return nil
	})

	sess.Op(svc, "DeleteObject", func() error {
		_, err := client.DeleteBlob(ctx, container, blob, nil)
		return err
	})

	sess.Op(svc, "DeleteBucket", func() error {
		_, err := client.DeleteContainer(ctx, container, nil)
		return err
	})
}
