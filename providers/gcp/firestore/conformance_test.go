package firestore

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/drivertest"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

// TestDatabaseConformance runs the shared database/driver.Database acceptance
// suite (internal/drivertest) against the GCP Firestore mock, so the assertions
// stay identical to the ones run against AWS DynamoDB and Azure Cosmos DB.
func TestDatabaseConformance(t *testing.T) {
	drivertest.RunDatabaseConformance(t, func() driver.Database {
		return newTestMock()
	})
}
