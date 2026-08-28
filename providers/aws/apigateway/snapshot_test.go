package apigateway_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	src := newMock(t)
	apiID, _, resID := deployProxyAPI(t, src, "hello", "GET", lambdaURI)

	data, err := src.Snapshot(ctx(), false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx(), data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// The API, resource tree, integration and stage all survive under their ids.
	if _, err := dst.GetRestAPI(ctx(), apiID); err != nil {
		t.Fatalf("restored GetRestAPI: %v", err)
	}

	ig, err := dst.GetIntegration(ctx(), apiID, resID, "GET")
	if err != nil {
		t.Fatalf("restored GetIntegration: %v", err)
	}

	if ig.Type != driver.IntegrationAWSProxy || ig.URI != lambdaURI {
		t.Fatalf("integration not restored: %+v", ig)
	}

	if _, err := dst.GetStage(ctx(), apiID, "prod"); err != nil {
		t.Fatalf("restored GetStage: %v", err)
	}
}
