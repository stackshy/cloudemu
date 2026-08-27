package blobstorage

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/drivertest"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// TestBucketConformance runs the shared storage/driver.Bucket acceptance
// suite (internal/drivertest) against the Azure Blob Storage mock, so the
// assertions stay identical to the ones run against AWS S3 and GCS.
func TestBucketConformance(t *testing.T) {
	drivertest.RunBucketConformance(t, func() driver.Bucket {
		return newTestMock()
	})
}
