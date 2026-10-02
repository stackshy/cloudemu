package athena

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

// TestFailedDDLStateChangeReasonOmitsInternalCodePrefix covers that a DDL
// statement failing against the catalog records only the human message in
// StateChangeReason, never the internal error-code name.
func TestFailedDDLStateChangeReasonOmitsInternalCodePrefix(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	start := func() *driver.QueryExecution {
		t.Helper()

		id, err := m.StartQueryExecution(ctx, driver.StartQueryExecutionInput{
			QueryString:         "CREATE DATABASE analytics",
			ResultConfiguration: &driver.ResultConfiguration{OutputLocation: "s3://out/"},
		})
		requireNoError(t, err, "StartQueryExecution")

		qe, err := m.GetQueryExecution(ctx, id)
		requireNoError(t, err, "GetQueryExecution")

		return qe
	}

	start()

	qe := start()
	if qe.Status.State != driver.QueryStateFailed {
		t.Fatalf("duplicate CREATE DATABASE state = %q, want FAILED", qe.Status.State)
	}

	if reason := qe.Status.StateChangeReason; reason != "Database analytics already exists" {
		t.Fatalf("StateChangeReason = %q, want the bare human message (no internal code prefix)", reason)
	}
}
