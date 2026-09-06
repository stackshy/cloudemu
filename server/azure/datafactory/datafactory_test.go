package datafactory_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	azureprovider "github.com/stackshy/cloudemu/v2/providers/azure"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	subID  = "00000000-0000-0000-0000-000000000000"
	apiVer = "?api-version=2018-06-01"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	p := azureprovider.New()
	ts := httptest.NewTLSServer(azureserver.NewFromProvider(p))
	t.Cleanup(ts.Close)

	return ts
}

func factoryURL(ts *httptest.Server, rg, name string) string {
	return ts.URL + "/subscriptions/" + subID + "/resourceGroups/" + rg +
		"/providers/Microsoft.DataFactory/factories/" + name + apiVer
}

func do(t *testing.T, ts *httptest.Server, method, url string, body any) (map[string]any, int) {
	t.Helper()

	var reader io.Reader

	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, url, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	out := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal response (%d): %v: %s", resp.StatusCode, err, raw)
		}
	}

	return out, resp.StatusCode
}

func props(t *testing.T, body map[string]any) map[string]any {
	t.Helper()

	p, ok := body["properties"].(map[string]any)
	if !ok {
		t.Fatalf("no properties object in %v", body)
	}

	return p
}

func TestCreateGetRoundTrip(t *testing.T) {
	ts := newServer(t)
	url := factoryURL(ts, "rg1", "adf1")

	created, status := do(t, ts, http.MethodPut, url, map[string]any{
		"location": "East US",
		"tags":     map[string]string{"env": "prod"},
		"identity": map[string]any{"type": "SystemAssigned"},
		"properties": map[string]any{
			"publicNetworkAccess": "Disabled",
			"globalParameters": map[string]any{
				"region": map[string]any{"type": "String", "value": "east"},
			},
		},
	})

	if status != http.StatusCreated {
		t.Fatalf("PUT status = %d, want 201", status)
	}

	if created["type"] != "Microsoft.DataFactory/factories" {
		t.Errorf("type = %v", created["type"])
	}

	// identity is TOP-LEVEL with computed principalId/tenantId.
	id, ok := created["identity"].(map[string]any)
	if !ok {
		t.Fatalf("identity missing/not top-level: %v", created)
	}

	principal, _ := id["principalId"].(string)
	tenant, _ := id["tenantId"].(string)

	if principal == "" || tenant == "" {
		t.Errorf("computed identity ids missing: %v", id)
	}

	p := props(t, created)
	if p["provisioningState"] != "Succeeded" {
		t.Errorf("provisioningState = %v", p["provisioningState"])
	}

	if p["version"] != "2018-06-01" {
		t.Errorf("version = %v", p["version"])
	}

	if p["createTime"] == nil || p["createTime"] == "" {
		t.Errorf("createTime missing: %v", p["createTime"])
	}

	if p["publicNetworkAccess"] != "Disabled" {
		t.Errorf("publicNetworkAccess = %v, want Disabled (explicit zero must round-trip)", p["publicNetworkAccess"])
	}

	// GET must return identity + createTime byte-stable.
	got, status := do(t, ts, http.MethodGet, url, nil)
	if status != http.StatusOK {
		t.Fatalf("GET status = %d", status)
	}

	gotID := got["identity"].(map[string]any)
	if gotID["principalId"] != principal || gotID["tenantId"] != tenant {
		t.Errorf("identity drifted across GET: %v vs principal=%q tenant=%q", gotID, principal, tenant)
	}

	if props(t, got)["createTime"] != p["createTime"] {
		t.Errorf("createTime drifted: %v != %v", props(t, got)["createTime"], p["createTime"])
	}
}

func TestCreateStableAcrossRepeatedGets(t *testing.T) {
	ts := newServer(t)
	url := factoryURL(ts, "rg1", "adf1")

	do(t, ts, http.MethodPut, url, map[string]any{
		"location": "East US",
		"identity": map[string]any{"type": "SystemAssigned"},
	})

	first, _ := do(t, ts, http.MethodGet, url, nil)
	fp := first["identity"].(map[string]any)["principalId"]
	ct := props(t, first)["createTime"]

	for i := 0; i < 4; i++ {
		got, _ := do(t, ts, http.MethodGet, url, nil)
		if got["identity"].(map[string]any)["principalId"] != fp {
			t.Fatalf("principalId drifted on GET #%d", i)
		}

		if props(t, got)["createTime"] != ct {
			t.Fatalf("createTime drifted on GET #%d", i)
		}
	}
}

func TestUpdateViaPut(t *testing.T) {
	ts := newServer(t)
	url := factoryURL(ts, "rg1", "adf1")

	first, _ := do(t, ts, http.MethodPut, url, map[string]any{"location": "East US"})
	firstCreate := props(t, first)["createTime"]

	updated, status := do(t, ts, http.MethodPut, url, map[string]any{
		"location":   "East US",
		"tags":       map[string]string{"env": "prod"},
		"properties": map[string]any{"publicNetworkAccess": "Disabled"},
	})
	if status != http.StatusOK {
		t.Fatalf("update PUT status = %d, want 200", status)
	}

	if props(t, updated)["createTime"] != firstCreate {
		t.Errorf("createTime changed on update: %v != %v", props(t, updated)["createTime"], firstCreate)
	}

	if props(t, updated)["publicNetworkAccess"] != "Disabled" {
		t.Errorf("publicNetworkAccess not updated: %v", props(t, updated)["publicNetworkAccess"])
	}
}

func TestPatchReplacesTags(t *testing.T) {
	ts := newServer(t)
	url := factoryURL(ts, "rg1", "adf1")

	do(t, ts, http.MethodPut, url, map[string]any{
		"location": "East US",
		"tags":     map[string]string{"a": "1", "b": "2"},
	})

	patched, status := do(t, ts, http.MethodPatch, url, map[string]any{
		"tags": map[string]string{"c": "3"},
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH status = %d", status)
	}

	tags, _ := patched["tags"].(map[string]any)
	if len(tags) != 1 || tags["c"] != "3" {
		t.Errorf("PATCH should REPLACE tags, got %v", tags)
	}
}

func TestDelete(t *testing.T) {
	ts := newServer(t)
	url := factoryURL(ts, "rg1", "adf1")

	do(t, ts, http.MethodPut, url, map[string]any{"location": "East US"})

	_, status := do(t, ts, http.MethodDelete, url, nil)
	if status != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200", status)
	}

	_, status = do(t, ts, http.MethodGet, url, nil)
	if status != http.StatusNotFound {
		t.Errorf("GET after delete status = %d, want 404", status)
	}
}

func TestListByResourceGroup(t *testing.T) {
	ts := newServer(t)
	do(t, ts, http.MethodPut, factoryURL(ts, "rg1", "adf1"), map[string]any{"location": "East US"})
	do(t, ts, http.MethodPut, factoryURL(ts, "rg1", "adf2"), map[string]any{"location": "East US"})
	do(t, ts, http.MethodPut, factoryURL(ts, "rg2", "adf3"), map[string]any{"location": "East US"})

	listURL := ts.URL + "/subscriptions/" + subID + "/resourceGroups/rg1" +
		"/providers/Microsoft.DataFactory/factories" + apiVer

	body, status := do(t, ts, http.MethodGet, listURL, nil)
	if status != http.StatusOK {
		t.Fatalf("list status = %d", status)
	}

	values, ok := body["value"].([]any)
	if !ok || len(values) != 2 {
		t.Fatalf("rg1 list = %v, want 2 factories", body["value"])
	}
}

func TestSubResourceDeferred(t *testing.T) {
	ts := newServer(t)
	do(t, ts, http.MethodPut, factoryURL(ts, "rg1", "adf1"), map[string]any{"location": "East US"})

	childURL := ts.URL + "/subscriptions/" + subID + "/resourceGroups/rg1" +
		"/providers/Microsoft.DataFactory/factories/adf1/linkedservices/ls1" + apiVer

	_, status := do(t, ts, http.MethodGet, childURL, nil)
	if status != http.StatusNotFound {
		t.Errorf("child resource status = %d, want 404 (deferred)", status)
	}
}
