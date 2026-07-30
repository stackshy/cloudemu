package azuresql_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// The armsql SDK version vendored here exposes managed-instance failover but
// not start/stop, so those POST action routes are exercised with raw HTTP.
func TestManagedInstanceStartStopRaw(t *testing.T) {
	ts := newRawServer(t)
	ctx := context.Background()

	const base = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Sql/managedInstances/mi1"
	const apiVersion = "?api-version=2021-11-01"

	body := `{"location":"eastus","properties":{"administratorLogin":"miadmin","subnetId":"/subnets/mi","vCores":4}}`

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, ts.URL+base+apiVersion, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new PUT request: %v", err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("PUT managed instance: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		t.Fatalf("PUT managed instance: status %d", resp.StatusCode)
	}

	for _, action := range []string{"/stop", "/start"} {
		areq, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+base+action+apiVersion, nil)
		if err != nil {
			t.Fatalf("new POST %s: %v", action, err)
		}

		aresp, err := ts.Client().Do(areq)
		if err != nil {
			t.Fatalf("POST %s: %v", action, err)
		}
		aresp.Body.Close()

		if aresp.StatusCode >= http.StatusBadRequest {
			t.Fatalf("POST %s: status %d", action, aresp.StatusCode)
		}
	}
}
