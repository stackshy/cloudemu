package cloudfunctions_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/api/cloudfunctions/v1"
	"google.golang.org/api/googleapi"
)

// TestSDKCloudFunctionsErrorMessagesOmitCodePrefix guards writeErr
// (server/gcp/cloudfunctions) against baking cloudemu's internal cerrors code
// name (e.g. "NotFound: ...", "AlreadyExists: ...") into the wire error
// message an SDK caller sees. Real Cloud Functions never prefixes its error
// messages with an internal error-taxonomy name.
func TestSDKCloudFunctionsErrorMessagesOmitCodePrefix(t *testing.T) {
	svc := newGCPSDKService(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"

	t.Run("NotFound Functions.Get", func(t *testing.T) {
		_, err := svc.Projects.Locations.Functions.Get(parent + "/functions/no-such-fn").Context(ctx).Do()

		var gErr *googleapi.Error
		if !errors.As(err, &gErr) {
			t.Fatalf("expected a googleapi.Error, got %T: %v", err, err)
		}
		assertNoCodePrefix(t, gErr.Message)
	})

	t.Run("AlreadyExists Functions.Create duplicate", func(t *testing.T) {
		fn := &cloudfunctions.CloudFunction{
			Name:              parent + "/functions/errmsg-dupe",
			Runtime:           "go121",
			EntryPoint:        "Hello",
			AvailableMemoryMb: 128,
			Timeout:           "60s",
		}

		if _, err := svc.Projects.Locations.Functions.Create(parent, fn).Context(ctx).Do(); err != nil {
			t.Fatalf("first Create: %v", err)
		}

		_, err := svc.Projects.Locations.Functions.Create(parent, fn).Context(ctx).Do()

		var gErr *googleapi.Error
		if !errors.As(err, &gErr) {
			t.Fatalf("expected a googleapi.Error, got %T: %v", err, err)
		}
		assertNoCodePrefix(t, gErr.Message)
	})
}

// assertNoCodePrefix fails if msg contains one of cloudemu's internal
// canonical error-code names followed by a colon — the shape err.Error()
// produces for a *cerrors.Error, as opposed to cerrors.Message(err).
func assertNoCodePrefix(t *testing.T, msg string) {
	t.Helper()

	for _, prefix := range []string{"NotFound:", "AlreadyExists:", "InvalidArgument:", "FailedPrecondition:", "Internal:"} {
		if strings.Contains(msg, prefix) {
			t.Errorf("wire error message %q leaks internal code prefix %q", msg, prefix)
		}
	}
}
