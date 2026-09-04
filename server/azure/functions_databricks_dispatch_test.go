// Regression tests for the Functions-vs-other-data-plane dispatch boundary on
// the shared standalone listener. NewFromProvider mounts the Functions invoke
// handler AND the Databricks workspace data plane on one server; both are
// reached under /api/. The Functions invoke matcher used to claim ANY /api/
// path, so — registering before Databricks — it swallowed every Databricks
// data-plane call (/api/2.1/clusters/create, /api/2.0/jobs/...), making the
// whole Databricks data plane unreachable through `cloudemu serve`.
//
// These tests drive the fully-assembled server and prove Databricks /api/{ver}/
// calls reach the Databricks handler while a genuine Functions invoke
// (/api/{name}) still reaches Functions.
package azure_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	databricks "github.com/databricks/databricks-sdk-go"
	dbxconfig "github.com/databricks/databricks-sdk-go/config"
	"github.com/databricks/databricks-sdk-go/service/compute"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestDatabricksDataPlaneNotSwallowedByFunctions proves a real databricks-sdk-go
// client can create a cluster (POST /api/2.1/clusters/create) against the shared
// stack where Functions is also mounted. The version-shaped first path segment
// ("2.1") must route to Databricks, not the Functions invoke handler.
//
// Fail-when-reverted: on the old over-broad Matches, Functions claims
// /api/2.1/clusters/create, tries to invoke a function literally named
// "2.1/clusters/create", 404s, and the SDK Create fails here.
func TestDatabricksDataPlaneNotSwallowedByFunctions(t *testing.T) {
	provider := cloudemu.NewAzure()
	srv := azureserver.NewFromProvider(provider)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	w, err := databricks.NewWorkspaceClient(&databricks.Config{
		Host:        ts.URL,
		Token:       "test-token",
		Credentials: dbxconfig.PatCredentials{},
	})
	if err != nil {
		t.Fatalf("new workspace client: %v", err)
	}

	ctx := context.Background()

	wait, err := w.Clusters.Create(ctx, compute.CreateCluster{
		ClusterName:  "cluster-1",
		SparkVersion: "13.3.x-scala2.12",
		NodeTypeId:   "Standard_DS3_v2",
		NumWorkers:   2,
	})
	if err != nil {
		t.Fatalf("Clusters.Create routed away from the Databricks data plane "+
			"(Functions swallowed /api/2.1/clusters/create): %v", err)
	}

	if wait.ClusterId == "" {
		t.Fatal("expected a cluster id from the Databricks data plane")
	}

	got, err := w.Clusters.GetByClusterId(ctx, wait.ClusterId)
	if err != nil {
		t.Fatalf("Clusters.Get (GET /api/2.1/clusters/get): %v", err)
	}

	if got.State != compute.StateRunning {
		t.Fatalf("cluster state = %q, want RUNNING", got.State)
	}
}

// TestFunctionsInvokeStillRoutesToFunctions is the no-regression guard: a
// genuine Functions invoke (/api/{functionName}, a non-version first segment)
// on the same shared stack still reaches the Functions invoke handler and runs
// the registered handler.
func TestFunctionsInvokeStillRoutesToFunctions(t *testing.T) {
	provider := cloudemu.NewAzure()
	srv := azureserver.NewFromProvider(provider)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// Provision the site so the invoke target exists, then register its handler.
	sitePath := ts.URL + "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Web/sites/echo" +
		"?api-version=2022-03-01"
	putBody := `{"kind":"functionapp","location":"eastus","properties":{"siteConfig":{}}}`

	putReq, err := http.NewRequestWithContext(context.Background(), http.MethodPut, sitePath, strings.NewReader(putBody))
	if err != nil {
		t.Fatalf("new PUT request: %v", err)
	}

	putResp, err := ts.Client().Do(putReq)
	if err != nil {
		t.Fatalf("PUT site: %v", err)
	}

	_, _ = io.Copy(io.Discard, putResp.Body)
	_ = putResp.Body.Close()

	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT site = %d, want 200", putResp.StatusCode)
	}

	provider.Functions.RegisterHandler("echo", func(_ context.Context, payload []byte) ([]byte, error) {
		return append([]byte(`{"got":`), append(payload, '}')...), nil
	})

	invReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/api/echo",
		strings.NewReader(`"hello"`))
	if err != nil {
		t.Fatalf("new invoke request: %v", err)
	}

	invResp, err := ts.Client().Do(invReq)
	if err != nil {
		t.Fatalf("POST /api/echo: %v", err)
	}

	defer invResp.Body.Close()

	body, _ := io.ReadAll(invResp.Body)

	if invResp.StatusCode != http.StatusOK {
		t.Fatalf("invoke status = %d, want 200 (body %q)", invResp.StatusCode, body)
	}

	if string(body) != `{"got":"hello"}` {
		t.Fatalf("invoke body = %q, want {\"got\":\"hello\"} — request did not reach the Functions handler", body)
	}
}
