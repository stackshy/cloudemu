package objectstorage_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/oci/objectstorage"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// TestConcurrentObjectOperations exercises the paths that read one store while
// writing another, which is what m.mu spans.
func TestConcurrentObjectOperations(t *testing.T) {
	m := objectstorage.New(config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(testCompartment),
	))
	ctx := context.Background()

	_, err := m.CreateBucketWith(ctx, objectstorage.BucketSpec{
		Name: testBucket, CompartmentID: testCompartment, Versioning: objectstorage.VersioningEnabled,
	})
	require.NoError(t, err)

	const workers = 8

	var wg sync.WaitGroup

	for i := range workers {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			key := fmt.Sprintf("k-%d", n)
			_ = m.PutObject(ctx, testBucket, key, []byte("v"), "text/plain", nil)
			_, _ = m.GetObject(ctx, testBucket, key)
			_, _ = m.HeadObject(ctx, testBucket, key)
			_, _ = m.ListObjects(ctx, testBucket, driver.ListOptions{})
			_, _ = m.ListObjectVersions(ctx, testBucket, driver.ListOptions{})
			_, _ = m.BucketDetails(ctx, testBucket)
			_, _ = m.ListBucketsIn(ctx, testCompartment)
			_, _ = m.GeneratePresignedURL(ctx, driver.PresignedURLRequest{
				Bucket: testBucket, Key: key, Method: "GET",
			})
			_, _ = m.ListPARs(ctx, testBucket, "")
			_, _, _ = m.DeleteObjectVersion(ctx, testBucket, key, "")
		}(i)
	}

	wg.Wait()
}

// TestConcurrentMultipartUploads runs several uploads in parallel over the
// shared multipart store.
func TestConcurrentMultipartUploads(t *testing.T) {
	m := objectstorage.New(config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(testCompartment),
	))
	ctx := context.Background()

	_, err := m.CreateBucketWith(ctx, objectstorage.BucketSpec{
		Name: testBucket, CompartmentID: testCompartment,
	})
	require.NoError(t, err)

	const workers = 8

	var wg sync.WaitGroup

	for i := range workers {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			key := fmt.Sprintf("big-%d", n)

			up, upErr := m.CreateMultipartUploadWith(ctx, testBucket, objectstorage.MultipartUploadSpec{Object: key})
			if upErr != nil {
				return
			}

			_, _ = m.UploadPart(ctx, testBucket, key, up.UploadID, 1, []byte("aaa"))
			_, _ = m.ListParts(ctx, testBucket, key, up.UploadID)
			_, _ = m.ListMultipartUploads(ctx, testBucket)
			_ = m.CompleteMultipartUpload(ctx, testBucket, key, up.UploadID,
				[]driver.UploadPart{{PartNumber: 1}})
		}(i)
	}

	wg.Wait()
}
