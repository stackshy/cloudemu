package appinsights_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	subID  = "00000000-0000-0000-0000-000000000000"
	apiVer = "?api-version=2020-02-02"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{SubscriptionID: subID, IAM: cloudP.IAM, Monitor: cloudP.Monitor})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func componentURL(ts *httptest.Server, rg, name string) string {
	return ts.URL + "/subscriptions/" + subID + "/resourceGroups/" + rg +
		"/providers/Microsoft.Insights/components/" + name + apiVer
}

// do issues a JSON ARM request and returns the decoded body and status.
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

func props(t *testing.T, resource map[string]any) map[string]any {
	t.Helper()

	p, ok := resource["properties"].(map[string]any)
	if !ok {
		t.Fatalf("response has no properties object: %v", resource)
	}

	return p
}

func TestComponentCreateGetRoundTrip(t *testing.T) {
	ts := newServer(t)
	url := componentURL(ts, "rg-ai", "my-app")

	created, status := do(t, ts, http.MethodPut, url, map[string]any{
		"location": "South Central US",
		"kind":     "web",
		"tags":     map[string]string{"env": "prod"},
		"properties": map[string]any{
			"Application_Type":   "web",
			"RetentionInDays":    90,
			"SamplingPercentage": 100,
		},
	})

	if status != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", status)
	}

	if created["kind"] != "web" {
		t.Errorf("top-level kind = %v, want web (echo overlay cannot reach it — must be modeled)", created["kind"])
	}

	if created["type"] != "Microsoft.Insights/components" {
		t.Errorf("type = %v", created["type"])
	}

	p := props(t, created)

	if p["provisioningState"] != "Succeeded" {
		t.Errorf("provisioningState = %v, want Succeeded", p["provisioningState"])
	}

	if p["Application_Type"] != "web" {
		t.Errorf("Application_Type = %v, want web", p["Application_Type"])
	}

	if p["ApplicationId"] != "my-app" {
		t.Errorf("ApplicationId = %v, want my-app (mirrors name)", p["ApplicationId"])
	}

	if p["IngestionMode"] != "ApplicationInsights" {
		t.Errorf("IngestionMode = %v, want ApplicationInsights default", p["IngestionMode"])
	}

	if got, _ := p["RetentionInDays"].(float64); got != 90 {
		t.Errorf("RetentionInDays = %v, want 90", p["RetentionInDays"])
	}

	ikey, _ := p["InstrumentationKey"].(string)
	if !looksLikeGUID(ikey) {
		t.Errorf("InstrumentationKey = %q, want GUID-shaped computed value", ikey)
	}

	appID, _ := p["AppId"].(string)
	if !looksLikeGUID(appID) {
		t.Errorf("AppId = %q, want GUID-shaped computed value", appID)
	}

	cs, _ := p["ConnectionString"].(string)
	if !strings.Contains(cs, "InstrumentationKey="+ikey) ||
		!strings.Contains(cs, "IngestionEndpoint=https://southcentralus.in.applicationinsights.azure.com/") ||
		!strings.Contains(cs, "ApplicationId="+appID) {
		t.Errorf("ConnectionString = %q, missing modern form for key/region/appid", cs)
	}
}

// TestComputedKeysStableAcrossGets is the #1 Terraform-drift guard: the
// instrumentation key and app id must be byte-for-byte identical on every read.
func TestComputedKeysStableAcrossGets(t *testing.T) {
	ts := newServer(t)
	url := componentURL(ts, "rg-ai", "stable-app")

	created, _ := do(t, ts, http.MethodPut, url, map[string]any{
		"location": "westus2", "kind": "web", "properties": map[string]any{},
	})
	c0 := props(t, created)

	get1, s1 := do(t, ts, http.MethodGet, url, nil)
	get2, s2 := do(t, ts, http.MethodGet, url, nil)

	if s1 != http.StatusOK || s2 != http.StatusOK {
		t.Fatalf("get statuses = %d, %d, want 200", s1, s2)
	}

	p1, p2 := props(t, get1), props(t, get2)

	for _, k := range []string{"InstrumentationKey", "AppId", "ConnectionString", "TenantId", "CreationDate"} {
		if p1[k] != p2[k] || p1[k] != c0[k] {
			t.Errorf("%s not stable: create=%v get1=%v get2=%v", k, c0[k], p1[k], p2[k])
		}
	}
}

// TestPutCannotChangeComputedKeys verifies the REST contract: a PUT specifying a
// different InstrumentationKey/AppId is ignored; the create-time values persist.
func TestPutCannotChangeComputedKeys(t *testing.T) {
	ts := newServer(t)
	url := componentURL(ts, "rg-ai", "pin-app")

	created, _ := do(t, ts, http.MethodPut, url, map[string]any{
		"location": "westus2", "kind": "web", "properties": map[string]any{},
	})
	orig := props(t, created)

	updated, status := do(t, ts, http.MethodPut, url, map[string]any{
		"location": "westus2", "kind": "web",
		"properties": map[string]any{
			"InstrumentationKey": "11111111-1111-1111-1111-111111111111",
			"AppId":              "22222222-2222-2222-2222-222222222222",
			"RetentionInDays":    30,
		},
	})

	if status != http.StatusOK {
		t.Fatalf("second put status = %d, want 200", status)
	}

	up := props(t, updated)
	if up["InstrumentationKey"] != orig["InstrumentationKey"] {
		t.Errorf("InstrumentationKey changed on PUT: %v -> %v", orig["InstrumentationKey"], up["InstrumentationKey"])
	}

	if up["AppId"] != orig["AppId"] {
		t.Errorf("AppId changed on PUT: %v -> %v", orig["AppId"], up["AppId"])
	}

	if got, _ := up["RetentionInDays"].(float64); got != 30 {
		t.Errorf("RetentionInDays = %v, want updated 30", up["RetentionInDays"])
	}
}

// TestExplicitFalseBoolsPreserved guards DisableIpMasking / ImmediatePurgeDataOn30Days:
// an explicit false must round-trip (not be dropped as a zero value).
func TestExplicitFalseBoolsPreserved(t *testing.T) {
	ts := newServer(t)
	url := componentURL(ts, "rg-ai", "bool-app")

	created, _ := do(t, ts, http.MethodPut, url, map[string]any{
		"location": "westus2", "kind": "web",
		"properties": map[string]any{
			"DisableIpMasking":           false,
			"ImmediatePurgeDataOn30Days": false,
		},
	})

	p := props(t, created)
	if v, ok := p["DisableIpMasking"].(bool); !ok || v {
		t.Errorf("DisableIpMasking = %v (%T), want explicit false", p["DisableIpMasking"], p["DisableIpMasking"])
	}

	if v, ok := p["ImmediatePurgeDataOn30Days"].(bool); !ok || v {
		t.Errorf("ImmediatePurgeDataOn30Days = %v, want explicit false", p["ImmediatePurgeDataOn30Days"])
	}
}

// TestPatchReplacesTagsMergesProps: PATCH replaces the tag set wholesale and
// merges properties over the stored ones, without disturbing computed keys.
func TestPatchReplacesTagsMergesProps(t *testing.T) {
	ts := newServer(t)
	url := componentURL(ts, "rg-ai", "patch-app")

	created, _ := do(t, ts, http.MethodPut, url, map[string]any{
		"location": "westus2", "kind": "web",
		"tags":       map[string]string{"a": "1", "b": "2"},
		"properties": map[string]any{"RetentionInDays": 90, "SamplingPercentage": 100},
	})
	origKey := props(t, created)["InstrumentationKey"]

	patched, status := do(t, ts, http.MethodPatch, url, map[string]any{
		"tags":       map[string]string{"c": "3"},
		"properties": map[string]any{"RetentionInDays": 30},
	})

	if status != http.StatusOK {
		t.Fatalf("patch status = %d, want 200", status)
	}

	tags, _ := patched["tags"].(map[string]any)
	if len(tags) != 1 || tags["c"] != "3" {
		t.Errorf("tags = %v, want wholesale replacement {c:3}", tags)
	}

	p := props(t, patched)
	if got, _ := p["RetentionInDays"].(float64); got != 30 {
		t.Errorf("RetentionInDays = %v, want merged 30", p["RetentionInDays"])
	}

	if got, _ := p["SamplingPercentage"].(float64); got != 100 {
		t.Errorf("SamplingPercentage = %v, want preserved 100", p["SamplingPercentage"])
	}

	if p["InstrumentationKey"] != origKey {
		t.Errorf("InstrumentationKey changed on PATCH: %v -> %v", origKey, p["InstrumentationKey"])
	}
}

func TestPatchMissingIs404(t *testing.T) {
	ts := newServer(t)
	_, status := do(t, ts, http.MethodPatch, componentURL(ts, "rg-ai", "ghost"), map[string]any{
		"tags": map[string]string{"x": "1"},
	})

	if status != http.StatusNotFound {
		t.Fatalf("patch missing status = %d, want 404", status)
	}
}

func TestDeleteIdempotentThen404(t *testing.T) {
	ts := newServer(t)
	url := componentURL(ts, "rg-ai", "del-app")

	do(t, ts, http.MethodPut, url, map[string]any{"location": "westus2", "kind": "web", "properties": map[string]any{}})

	if _, s := do(t, ts, http.MethodDelete, url, nil); s != http.StatusOK {
		t.Fatalf("first delete status = %d, want 200", s)
	}

	if _, s := do(t, ts, http.MethodDelete, url, nil); s != http.StatusNoContent {
		t.Fatalf("second delete status = %d, want 204", s)
	}

	if _, s := do(t, ts, http.MethodGet, url, nil); s != http.StatusNotFound {
		t.Fatalf("get after delete status = %d, want 404", s)
	}
}

func TestListByResourceGroupAndSubscription(t *testing.T) {
	ts := newServer(t)

	do(t, ts, http.MethodPut, componentURL(ts, "rg-1", "app-a"),
		map[string]any{"location": "westus2", "kind": "web", "properties": map[string]any{}})
	do(t, ts, http.MethodPut, componentURL(ts, "rg-1", "app-b"),
		map[string]any{"location": "westus2", "kind": "web", "properties": map[string]any{}})
	do(t, ts, http.MethodPut, componentURL(ts, "rg-2", "app-c"),
		map[string]any{"location": "westus2", "kind": "web", "properties": map[string]any{}})

	rgList, s := do(t, ts, http.MethodGet,
		ts.URL+"/subscriptions/"+subID+"/resourceGroups/rg-1/providers/Microsoft.Insights/components"+apiVer, nil)
	if s != http.StatusOK {
		t.Fatalf("rg list status = %d", s)
	}

	if got := listNames(rgList); got != "app-a,app-b" {
		t.Errorf("rg-1 list = %q, want app-a,app-b (deterministic order, scoped to rg)", got)
	}

	subList, s := do(t, ts, http.MethodGet,
		ts.URL+"/subscriptions/"+subID+"/providers/Microsoft.Insights/components"+apiVer, nil)
	if s != http.StatusOK {
		t.Fatalf("sub list status = %d", s)
	}

	if got := listNames(subList); got != "app-a,app-b,app-c" {
		t.Errorf("subscription list = %q, want all three", got)
	}
}

func listNames(list map[string]any) string {
	values, _ := list["value"].([]any)

	names := make([]string, 0, len(values))

	for _, v := range values {
		item, _ := v.(map[string]any)
		if n, ok := item["name"].(string); ok {
			names = append(names, n)
		}
	}

	return strings.Join(names, ",")
}

func looksLikeGUID(s string) bool {
	if len(s) != 36 {
		return false
	}

	return s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-'
}
