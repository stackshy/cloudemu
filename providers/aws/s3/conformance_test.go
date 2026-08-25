package s3

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/drivertest"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// TestBucketConformance runs the shared storage/driver.Bucket acceptance
// suite (internal/drivertest) against the AWS S3 mock, so the assertions
// stay identical to the ones run against Azure Blob Storage and GCS.
func TestBucketConformance(t *testing.T) {
	drivertest.RunBucketConformance(t, func() driver.Bucket {
		return newTestMock()
	})
}
