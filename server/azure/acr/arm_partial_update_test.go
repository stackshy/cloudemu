package acr_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const acrAPIVersion = "?api-version=2023-07-01"

// rawARMServer returns an httptest TLS server serving the ACR ARM plane plus a
// client wired to trust it, for tests that need to inspect raw status codes and
// bodies (the azure-sdk poller hides the create vs. replace status code).
func rawARMServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{ACR: cloudP.ACR})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	return ts, ts.Client()
}

func armDo(t *testing.T, client *http.Client, method, url, body string) (int, map[string]any) {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, url, reader)
	if err != nil {
		t.Fatalf("NewRequest %s %s: %v", method, url, err)
	}

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var decoded map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}

	return resp.StatusCode, decoded
}

func TestARMRegistryPutStatusAndPatchMerge(t *testing.T) {
	ts, client := rawARMServer(t)

	regURL := ts.URL + "/subscriptions/sub-1/resourceGroups/rg-1" +
		"/providers/Microsoft.ContainerRegistry/registries/myreg" + acrAPIVersion

	// First PUT creates: 201 Created.
	code, _ := armDo(t, client, http.MethodPut, regURL,
		`{"location":"eastus","sku":{"name":"Premium"},"properties":{"adminUserEnabled":true},"tags":{"team":"core"}}`)
	if code != http.StatusCreated {
		t.Fatalf("first PUT: got %d, want 201", code)
	}

	// Second PUT replaces the existing registry: 200 OK.
	code, _ = armDo(t, client, http.MethodPut, regURL,
		`{"location":"eastus","sku":{"name":"Premium"},"properties":{"adminUserEnabled":true}}`)
	if code != http.StatusOK {
		t.Fatalf("second PUT: got %d, want 200", code)
	}

	// PATCH only the tags: SKU, location and adminUserEnabled must survive.
	code, _ = armDo(t, client, http.MethodPatch, regURL, `{"tags":{"env":"prod"}}`)
	if code != http.StatusOK {
		t.Fatalf("PATCH: got %d, want 200", code)
	}

	_, body := armDo(t, client, http.MethodGet, regURL, "")

	sku, _ := body["sku"].(map[string]any)
	if sku == nil || sku["name"] != "Premium" {
		t.Fatalf("PATCH clobbered sku: %v", body["sku"])
	}

	props, _ := body["properties"].(map[string]any)
	if props == nil || props["adminUserEnabled"] != true {
		t.Fatalf("PATCH clobbered adminUserEnabled: %v", body["properties"])
	}

	if props["loginServer"] != "myreg.azurecr.io" {
		t.Fatalf("PATCH clobbered loginServer: %v", props["loginServer"])
	}

	tags, _ := body["tags"].(map[string]any)
	if tags == nil || tags["env"] != "prod" {
		t.Fatalf("PATCH did not apply tags: %v", body["tags"])
	}
}

// TestARMRegistryPatchPropertiesPreservesAdminUser reproduces the partial-merge
// blocker: a PATCH whose properties body touches only another field (here
// publicNetworkAccess) must not silently reset a previously-set adminUserEnabled.
func TestARMRegistryPatchPropertiesPreservesAdminUser(t *testing.T) {
	ts, client := rawARMServer(t)

	regURL := ts.URL + "/subscriptions/sub-1/resourceGroups/rg-1" +
		"/providers/Microsoft.ContainerRegistry/registries/netreg" + acrAPIVersion

	code, _ := armDo(t, client, http.MethodPut, regURL,
		`{"location":"eastus","sku":{"name":"Premium"},"properties":{"adminUserEnabled":true}}`)
	if code != http.StatusCreated {
		t.Fatalf("PUT: got %d, want 201", code)
	}

	// PATCH a properties block that only sets publicNetworkAccess (a field absent
	// from our decode shape); adminUserEnabled is omitted and must survive.
	code, _ = armDo(t, client, http.MethodPatch, regURL,
		`{"properties":{"publicNetworkAccess":"Disabled"}}`)
	if code != http.StatusOK {
		t.Fatalf("PATCH: got %d, want 200", code)
	}

	_, body := armDo(t, client, http.MethodGet, regURL, "")

	props, _ := body["properties"].(map[string]any)
	if props == nil || props["adminUserEnabled"] != true {
		t.Fatalf("PATCH reset adminUserEnabled: %v", body["properties"])
	}
}

// TestARMDeleteMissingIsIdempotent204 asserts ARM DELETE is idempotent: deleting
// a never-created registry/webhook/replication is a successful no-op returning
// 204, matching the ACR swagger ("does not exist in the subscription").
func TestARMDeleteMissingIsIdempotent204(t *testing.T) {
	ts, client := rawARMServer(t)

	base := ts.URL + "/subscriptions/sub-1/resourceGroups/rg-1" +
		"/providers/Microsoft.ContainerRegistry/registries"

	code, _ := armDo(t, client, http.MethodDelete, base+"/ghost"+acrAPIVersion, "")
	if code != http.StatusNoContent {
		t.Fatalf("DELETE missing registry: got %d, want 204", code)
	}

	// Create a parent registry so the webhook/replication paths resolve.
	code, _ = armDo(t, client, http.MethodPut, base+"/idemreg"+acrAPIVersion,
		`{"location":"eastus","sku":{"name":"Premium"}}`)
	if code != http.StatusCreated {
		t.Fatalf("registry PUT: got %d, want 201", code)
	}

	code, _ = armDo(t, client, http.MethodDelete, base+"/idemreg/webhooks/ghost"+acrAPIVersion, "")
	if code != http.StatusNoContent {
		t.Fatalf("DELETE missing webhook: got %d, want 204", code)
	}

	code, _ = armDo(t, client, http.MethodDelete, base+"/idemreg/replications/ghost"+acrAPIVersion, "")
	if code != http.StatusNoContent {
		t.Fatalf("DELETE missing replication: got %d, want 204", code)
	}
}

func TestARMRegistryPatchMissingIs404(t *testing.T) {
	ts, client := rawARMServer(t)

	url := ts.URL + "/subscriptions/sub-1/resourceGroups/rg-1" +
		"/providers/Microsoft.ContainerRegistry/registries/ghost" + acrAPIVersion

	code, _ := armDo(t, client, http.MethodPatch, url, `{"tags":{"env":"prod"}}`)
	if code != http.StatusNotFound {
		t.Fatalf("PATCH missing registry: got %d, want 404", code)
	}
}

func TestARMWebhookPutStatusAndPatchMerge(t *testing.T) {
	ts, client := rawARMServer(t)

	base := ts.URL + "/subscriptions/sub-1/resourceGroups/rg-1" +
		"/providers/Microsoft.ContainerRegistry/registries/hookreg"

	code, _ := armDo(t, client, http.MethodPut, base+acrAPIVersion,
		`{"location":"eastus","sku":{"name":"Standard"}}`)
	if code != http.StatusCreated {
		t.Fatalf("registry PUT: got %d, want 201", code)
	}

	whURL := base + "/webhooks/wh1" + acrAPIVersion

	// First PUT creates the webhook: 201.
	code, _ = armDo(t, client, http.MethodPut, whURL,
		`{"location":"eastus","properties":{"serviceUri":"https://example.com/hook","actions":["push"],"status":"enabled"}}`)
	if code != http.StatusCreated {
		t.Fatalf("webhook PUT: got %d, want 201", code)
	}

	// PATCH only status: actions must survive.
	code, _ = armDo(t, client, http.MethodPatch, whURL, `{"properties":{"status":"disabled"}}`)
	if code != http.StatusOK {
		t.Fatalf("webhook PATCH: got %d, want 200", code)
	}

	_, body := armDo(t, client, http.MethodGet, whURL, "")

	props, _ := body["properties"].(map[string]any)
	if props == nil {
		t.Fatalf("webhook GET missing properties: %v", body)
	}

	if props["status"] != "disabled" {
		t.Fatalf("webhook PATCH did not apply status: %v", props["status"])
	}

	actions, _ := props["actions"].([]any)
	if len(actions) != 1 || actions[0] != "push" {
		t.Fatalf("webhook PATCH clobbered actions: %v", props["actions"])
	}
}

func TestARMReplicationPutStatusAndPatchMerge(t *testing.T) {
	ts, client := rawARMServer(t)

	base := ts.URL + "/subscriptions/sub-1/resourceGroups/rg-1" +
		"/providers/Microsoft.ContainerRegistry/registries/repreg"

	code, _ := armDo(t, client, http.MethodPut, base+acrAPIVersion,
		`{"location":"eastus","sku":{"name":"Premium"}}`)
	if code != http.StatusCreated {
		t.Fatalf("registry PUT: got %d, want 201", code)
	}

	repURL := base + "/replications/westus" + acrAPIVersion

	// First PUT creates the replication with regionEndpointEnabled=false: 201.
	code, _ = armDo(t, client, http.MethodPut, repURL,
		`{"location":"westus","properties":{"regionEndpointEnabled":false}}`)
	if code != http.StatusCreated {
		t.Fatalf("replication PUT: got %d, want 201", code)
	}

	// PATCH only tags: regionEndpointEnabled must survive (not reset to zero).
	code, _ = armDo(t, client, http.MethodPatch, repURL, `{"tags":{"env":"prod"}}`)
	if code != http.StatusOK {
		t.Fatalf("replication PATCH: got %d, want 200", code)
	}

	_, body := armDo(t, client, http.MethodGet, repURL, "")

	props, _ := body["properties"].(map[string]any)
	if props == nil {
		t.Fatalf("replication GET missing properties: %v", body)
	}

	if props["regionEndpointEnabled"] != false {
		t.Fatalf("replication PATCH clobbered regionEndpointEnabled: %v", props["regionEndpointEnabled"])
	}

	tags, _ := body["tags"].(map[string]any)
	if tags == nil || tags["env"] != "prod" {
		t.Fatalf("replication PATCH did not apply tags: %v", body["tags"])
	}
}
