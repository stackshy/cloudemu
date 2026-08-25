package ec2

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/drivertest"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// TestComputeConformance runs the shared services/compute/driver.Compute
// acceptance suite (internal/drivertest) against the AWS EC2 mock, so the
// assertions stay identical to the ones run against Azure VM and GCP GCE.
func TestComputeConformance(t *testing.T) {
	drivertest.RunComputeConformance(t, func() driver.Compute {
		return newTestMock()
	})
}
