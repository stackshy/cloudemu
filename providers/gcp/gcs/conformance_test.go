package gcs

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/drivertest"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// TestBucketConformance runs the shared storage/driver.Bucket acceptance
// suite (internal/drivertest) against the GCS mock, so the assertions stay
// identical to the ones run against AWS S3 and Azure Blob Storage.
func TestBucketConformance(t *testing.T) {
	drivertest.RunBucketConformance(t, func() driver.Bucket {
		return newTestMock()
	})
}
