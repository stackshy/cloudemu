package dynamodb

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/drivertest"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

// TestDatabaseConformance runs the shared database/driver.Database acceptance
// suite (internal/drivertest) against the AWS DynamoDB mock, so the assertions
// stay identical to the ones run against Azure Cosmos DB and GCP Firestore.
func TestDatabaseConformance(t *testing.T) {
	drivertest.RunDatabaseConformance(t, func() driver.Database {
		return newTestMock()
	})
}
