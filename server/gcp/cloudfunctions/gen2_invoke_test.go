package cloudfunctions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	cloudfunctionsv1 "google.golang.org/api/cloudfunctions/v1"
	cloudfunctions2 "google.golang.org/api/cloudfunctions/v2"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// gen2Fixture spins up a server backed by one Cloud Functions driver and returns
// the base URL plus a v2 and v1 client pointed at it, so a test can exercise the
// gen2 (v2) surface, the gen2 invoke path (raw :call), and the v1 surface against
// the same shared driver store.
func gen2Fixture(t *testing.T) (string, *cloudfunctions2.Service, *cloudfunctionsv1.Service) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{CloudFunctions: cloud.CloudFunctions})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	svc2, err := cloudfunctions2.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("NewService (v2): %v", err)
	}

	svc1, err := cloudfunctionsv1.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("NewService (v1): %v", err)
	}

	return ts.URL, svc2, svc1
}

// callGen2 POSTs to the gen2 invoke path .../functions/{name}:call (the v2 Go
// client has no typed Call, so a real user hits it over REST) and returns the
// decoded {executionId, result|error} body and HTTP status.
func callGen2(t *testing.T, baseURL, name, data string) (map[string]string, int) {
	t.Helper()

	url := baseURL + "/v2/projects/demo/locations/us-central1/functions/" + name + ":call"

	body, _ := json.Marshal(map[string]string{"data": data})

	resp, err := http.Post(url, "application/json", bytes.NewReader(body)) //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("POST :call: %v", err)
	}
	defer resp.Body.Close()

	var out map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&out)

	return out, resp.StatusCode
}

// TestGen2InvokeThroughDriver is the world-case gen2 fix: a gen2 function must
// reach the shared driver so it is invokable like gen1. It deploys a gen2
// function, invokes it via :call and asserts the echo-stub result model (payload
// echoed) matches gen1, then confirms the v2 GET still returns the v2 shape
// (ACTIVE + Cloud Run uri) and that the gen2 function stays out of the v1 API.
func TestGen2InvokeThroughDriver(t *testing.T) {
	baseURL, svc2, svc1 := gen2Fixture(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"
	name := parent + "/functions/g2"

	if _, err := svc2.Projects.Locations.Functions.Create(parent, &cloudfunctions2.Function{
		BuildConfig: &cloudfunctions2.BuildConfig{
			Runtime:    "go121",
			EntryPoint: "Hello",
			Source: &cloudfunctions2.Source{
				StorageSource: &cloudfunctions2.StorageSource{Bucket: "b", Object: "o.zip"},
			},
		},
		ServiceConfig: &cloudfunctions2.ServiceConfig{AvailableMemory: "512M", TimeoutSeconds: 120},
	}).FunctionId("g2").Context(ctx).Do(); err != nil {
		t.Fatalf("Create (gen2): %v", err)
	}

	// Invoke through the gen2 invoke path — the function is now in the driver, so
	// the echo stub returns the request payload just like a gen1 :call.
	payload := `{"hello":"world"}`

	out, status := callGen2(t, baseURL, "g2", payload)
	if status != http.StatusOK {
		t.Fatalf(":call status = %d, want 200 (body %v)", status, out)
	}

	if out["result"] != payload {
		t.Fatalf(":call result = %q, want echoed payload %q", out["result"], payload)
	}

	if out["executionId"] == "" {
		t.Fatal(":call executionId empty")
	}

	// The v2 GET still returns the gen2 shape unchanged.
	got, err := svc2.Projects.Locations.Functions.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get (gen2): %v", err)
	}

	if got.State != "ACTIVE" || got.Environment != "GEN_2" {
		t.Fatalf("gen2 shape drifted: state=%q env=%q", got.State, got.Environment)
	}

	if got.ServiceConfig == nil || got.ServiceConfig.Uri == "" {
		t.Fatalf("serviceConfig.uri missing after invoke: %+v", got.ServiceConfig)
	}

	if got.ServiceConfig.AvailableMemory != "512M" {
		t.Fatalf("availableMemory = %q, want 512M", got.ServiceConfig.AvailableMemory)
	}

	// The gen2 function must NOT surface through the v1 API even though it shares
	// the driver store: v1 list excludes it and v1 get 404s.
	listV1, err := svc1.Projects.Locations.Functions.List(parent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List (v1): %v", err)
	}

	if len(listV1.Functions) != 0 {
		t.Fatalf("v1 list returned %d functions, want 0 (gen2 must not leak)", len(listV1.Functions))
	}

	if _, err := svc1.Projects.Locations.Functions.Get(name).Context(ctx).Do(); err == nil {
		t.Fatal("v1 Get on a gen2 function returned nil error, want NotFound")
	}
}

// TestGen2InvokeMissingAndAfterDelete confirms the invoke path 404s for an
// unknown gen2 function and stops resolving once the function is deleted (the
// driver entry is removed alongside the gen2 map entry).
func TestGen2InvokeMissingAndAfterDelete(t *testing.T) {
	baseURL, svc2, _ := gen2Fixture(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"
	name := parent + "/functions/g2"

	if _, status := callGen2(t, baseURL, "ghost", "{}"); status != http.StatusNotFound {
		t.Fatalf(":call on missing gen2 status = %d, want 404", status)
	}

	if _, err := svc2.Projects.Locations.Functions.Create(parent, &cloudfunctions2.Function{
		BuildConfig: &cloudfunctions2.BuildConfig{Runtime: "go121", EntryPoint: "Hello"},
	}).FunctionId("g2").Context(ctx).Do(); err != nil {
		t.Fatalf("Create (gen2): %v", err)
	}

	if _, status := callGen2(t, baseURL, "g2", "{}"); status != http.StatusOK {
		t.Fatalf(":call after create status = %d, want 200", status)
	}

	if _, err := svc2.Projects.Locations.Functions.Delete(name).Context(ctx).Do(); err != nil {
		t.Fatalf("Delete (gen2): %v", err)
	}

	if _, status := callGen2(t, baseURL, "g2", "{}"); status != http.StatusNotFound {
		t.Fatalf(":call after delete status = %d, want 404 (driver entry must be gone)", status)
	}
}

// TestSDKGen2ConcurrentPatchDelete races a PATCH against a DELETE of the same
// gen2 function. patchV2 releases h.mu before syncing the driver, so a delete can
// remove the map+driver entry in the window; the sync must then propagate the
// driver's NotFound (a 404 PATCH) rather than resurrect a zombie driver entry
// with no gen2 map entry. Whatever the interleaving, once both complete the name
// must be FULLY absent: a fresh create of the same name must SUCCEED (a lingering
// zombie would make it fail ALREADY_EXISTS forever). Run under -race.
func TestSDKGen2ConcurrentPatchDelete(t *testing.T) {
	_, svc2, _ := gen2Fixture(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"
	name := parent + "/functions/g2"

	const iterations = 25

	for i := 0; i < iterations; i++ {
		// Create must succeed every iteration — the previous iteration must have
		// left no zombie driver entry poisoning the name.
		if _, err := svc2.Projects.Locations.Functions.Create(parent, &cloudfunctions2.Function{
			BuildConfig: &cloudfunctions2.BuildConfig{Runtime: "go121", EntryPoint: "Hello"},
		}).FunctionId("g2").Context(ctx).Do(); err != nil {
			t.Fatalf("iteration %d: Create must succeed (zombie left by prior race?): %v", i, err)
		}

		var wg sync.WaitGroup

		wg.Add(2)

		// Patch: may succeed or 404 depending on the interleaving — both are valid,
		// so its error is not asserted.
		go func() {
			defer wg.Done()

			_, _ = svc2.Projects.Locations.Functions.Patch(name, &cloudfunctions2.Function{
				ServiceConfig: &cloudfunctions2.ServiceConfig{AvailableMemory: "1024M"},
			}).UpdateMask("serviceConfig.availableMemory").Context(ctx).Do()
		}()

		// Delete always removes both the map and driver entries.
		go func() {
			defer wg.Done()

			_, _ = svc2.Projects.Locations.Functions.Delete(name).Context(ctx).Do()
		}()

		wg.Wait()

		// The function must be gone regardless of who won the race.
		if _, err := svc2.Projects.Locations.Functions.Get(name).Context(ctx).Do(); err == nil {
			t.Fatalf("iteration %d: Get after patch/delete race returned nil error, want NotFound", i)
		}
	}
}
