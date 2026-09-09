package frontdoor_test

import (
	"bytes"
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
	testSub  = "sub-1"
	testRG   = "rg-1"
	profile  = "profile-1"
	apiQuery = "?api-version=2025-04-15"
)

// newServer stands up the full Azure wire server backed by a fresh in-memory
// provider. BlobStorage is wired too so we prove Microsoft.Cdn/profiles is claimed
// by the Front Door handler and not swallowed by the permissive blob fallback.
func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		FrontDoor:   cloudP.FrontDoor,
		BlobStorage: cloudP.BlobStorage,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func profileURL(base, name string) string {
	return base + "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/Microsoft.Cdn/profiles/" + name
}

// do issues an ARM request and returns the status plus the decoded JSON body (nil
// for an empty body).
func do(t *testing.T, ts *httptest.Server, method, url string, body any) (int, map[string]any) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}

		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url+apiQuery, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if len(bytes.TrimSpace(raw)) == 0 {
		return resp.StatusCode, nil
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s %s body %q: %v", method, url, raw, err)
	}

	return resp.StatusCode, out
}

func props(t *testing.T, body map[string]any) map[string]any {
	t.Helper()

	p, ok := body["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties missing or wrong type in %v", body)
	}

	return p
}

// createProfile PUTs a Standard Front Door profile and returns the response body.
func createProfile(t *testing.T, ts *httptest.Server, name string) map[string]any {
	t.Helper()

	status, body := do(t, ts, http.MethodPut, profileURL(ts.URL, name), map[string]any{
		"location": "global",
		"sku":      map[string]any{"name": "Standard_AzureFrontDoor"},
	})
	if status != http.StatusCreated {
		t.Fatalf("create profile status = %d, want 201", status)
	}

	return body
}

// TestProfileRoundTrip proves the top-level sku/location/kind, the computed
// frontDoorId and the terminal provisioningState/resourceState survive create→get.
func TestProfileRoundTrip(t *testing.T) {
	ts := newServer(t)

	created := createProfile(t, ts, profile)

	if sku, _ := created["sku"].(map[string]any); sku == nil || sku["name"] != "Standard_AzureFrontDoor" {
		t.Errorf("sku not round-tripped top-level: %v", created["sku"])
	}

	if created["kind"] != "frontdoor" {
		t.Errorf("kind = %v, want frontdoor", created["kind"])
	}

	if created["location"] != "global" {
		t.Errorf("location = %v, want global", created["location"])
	}

	p := props(t, created)
	if p["provisioningState"] != "Succeeded" {
		t.Errorf("provisioningState = %v, want Succeeded", p["provisioningState"])
	}

	if p["resourceState"] != "Active" {
		t.Errorf("resourceState = %v, want Active", p["resourceState"])
	}

	if fid, _ := p["frontDoorId"].(string); fid == "" {
		t.Error("frontDoorId not computed")
	}

	if p["originResponseTimeoutSeconds"] == nil {
		t.Error("originResponseTimeoutSeconds default not injected")
	}
}

// TestFrontDoorIDStableAcrossGets is the #1 drift guard: the computed frontDoorId
// must be identical on create and on every subsequent GET, and unchanged by a
// re-PUT update.
func TestFrontDoorIDStableAcrossGets(t *testing.T) {
	ts := newServer(t)

	created := createProfile(t, ts, profile)
	want, _ := props(t, created)["frontDoorId"].(string)

	if want == "" {
		t.Fatal("frontDoorId empty on create")
	}

	for i := 0; i < 3; i++ {
		status, body := do(t, ts, http.MethodGet, profileURL(ts.URL, profile), nil)
		if status != http.StatusOK {
			t.Fatalf("get #%d status = %d, want 200", i, status)
		}

		if got, _ := props(t, body)["frontDoorId"].(string); got != want {
			t.Fatalf("frontDoorId drift on get #%d: got %q, want %q", i, got, want)
		}
	}

	// A re-PUT (update) must not regenerate the id.
	status, updated := do(t, ts, http.MethodPut, profileURL(ts.URL, profile), map[string]any{
		"location": "global",
		"sku":      map[string]any{"name": "Premium_AzureFrontDoor"},
	})
	if status != http.StatusOK {
		t.Fatalf("update status = %d, want 200", status)
	}

	if got, _ := props(t, updated)["frontDoorId"].(string); got != want {
		t.Errorf("frontDoorId changed on update: got %q, want %q", got, want)
	}
}

// TestProfileTagsReplace proves PATCH replaces the tag map wholesale while leaving
// the modeled sku intact.
func TestProfileTagsReplace(t *testing.T) {
	ts := newServer(t)

	do(t, ts, http.MethodPut, profileURL(ts.URL, profile), map[string]any{
		"location": "global",
		"sku":      map[string]any{"name": "Standard_AzureFrontDoor"},
		"tags":     map[string]any{"env": "prod", "team": "net"},
	})

	status, patched := do(t, ts, http.MethodPatch, profileURL(ts.URL, profile), map[string]any{
		"tags": map[string]any{"env": "dev"},
	})
	if status != http.StatusOK {
		t.Fatalf("patch status = %d, want 200", status)
	}

	tags, _ := patched["tags"].(map[string]any)
	if len(tags) != 1 || tags["env"] != "dev" {
		t.Errorf("tags = %v, want only {env:dev}", tags)
	}

	if sku, _ := patched["sku"].(map[string]any); sku == nil || sku["name"] != "Standard_AzureFrontDoor" {
		t.Error("patch dropped sku")
	}
}

// TestEndpointRoundTrip proves the endpoint hostName is computed and stable, the
// enabledState default is injected, and an explicit Disabled round-trips.
func TestEndpointRoundTrip(t *testing.T) {
	ts := newServer(t)
	createProfile(t, ts, profile)

	epURL := profileURL(ts.URL, profile) + "/afdEndpoints/ep-1"

	status, created := do(t, ts, http.MethodPut, epURL, map[string]any{
		"location":   "global",
		"properties": map[string]any{},
	})
	if status != http.StatusCreated {
		t.Fatalf("create endpoint status = %d, want 201", status)
	}

	p := props(t, created)
	host, _ := p["hostName"].(string)

	if !strings.HasPrefix(host, "ep-1-") || !strings.HasSuffix(host, ".z01.azurefd.net") {
		t.Errorf("hostName = %q, want ep-1-<token>.z01.azurefd.net", host)
	}

	if p["enabledState"] != "Enabled" {
		t.Errorf("enabledState default = %v, want Enabled", p["enabledState"])
	}

	// hostName must be stable across GETs.
	_, got := do(t, ts, http.MethodGet, epURL, nil)
	if h, _ := props(t, got)["hostName"].(string); h != host {
		t.Errorf("hostName drift: got %q, want %q", h, host)
	}

	// Explicit Disabled round-trips.
	_, disabled := do(t, ts, http.MethodPut, epURL, map[string]any{
		"location":   "global",
		"properties": map[string]any{"enabledState": "Disabled"},
	})
	if props(t, disabled)["enabledState"] != "Disabled" {
		t.Errorf("explicit enabledState=Disabled not round-tripped: %v", props(t, disabled)["enabledState"])
	}
}

// TestOriginGroupRoundTrip proves the nested loadBalancingSettings/healthProbeSettings
// round-trip verbatim (including an explicit additionalLatencyInMilliseconds=0) and
// that no location/tags are stamped.
func TestOriginGroupRoundTrip(t *testing.T) {
	ts := newServer(t)
	createProfile(t, ts, profile)

	ogURL := profileURL(ts.URL, profile) + "/originGroups/og-1"

	status, created := do(t, ts, http.MethodPut, ogURL, map[string]any{
		"properties": map[string]any{
			"loadBalancingSettings": map[string]any{
				"sampleSize":                      16,
				"successfulSamplesRequired":       3,
				"additionalLatencyInMilliseconds": 0,
			},
			"healthProbeSettings": map[string]any{
				"probePath":        "/",
				"probeRequestType": "HEAD",
				"probeProtocol":    "Http",
			},
			"sessionAffinityState": "Enabled",
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("create origin group status = %d, want 201", status)
	}

	if created["location"] != nil {
		t.Errorf("origin group should have no location, got %v", created["location"])
	}

	p := props(t, created)
	lb, _ := p["loadBalancingSettings"].(map[string]any)

	if lb == nil || lb["sampleSize"].(float64) != 16 {
		t.Errorf("loadBalancingSettings.sampleSize not round-tripped: %v", lb)
	}

	if lb["additionalLatencyInMilliseconds"].(float64) != 0 {
		t.Errorf("explicit additionalLatencyInMilliseconds=0 swallowed: %v", lb["additionalLatencyInMilliseconds"])
	}

	hp, _ := p["healthProbeSettings"].(map[string]any)
	if hp == nil || hp["probePath"] != "/" {
		t.Errorf("healthProbeSettings.probePath not round-tripped: %v", hp)
	}

	if p["provisioningState"] != "Succeeded" {
		t.Errorf("provisioningState = %v, want Succeeded", p["provisioningState"])
	}
}

// TestChildUnderMissingProfile proves a child PUT under an absent profile 404s
// rather than creating an orphan.
func TestChildUnderMissingProfile(t *testing.T) {
	ts := newServer(t)

	status, _ := do(t, ts, http.MethodPut,
		profileURL(ts.URL, "missing")+"/afdEndpoints/ep-1",
		map[string]any{"location": "global"})
	if status != http.StatusNotFound {
		t.Errorf("endpoint under missing profile status = %d, want 404", status)
	}
}

// TestDeleteProfileCascades proves deleting a profile removes its children.
func TestDeleteProfileCascades(t *testing.T) {
	ts := newServer(t)
	createProfile(t, ts, profile)

	epURL := profileURL(ts.URL, profile) + "/afdEndpoints/ep-1"
	ogURL := profileURL(ts.URL, profile) + "/originGroups/og-1"

	do(t, ts, http.MethodPut, epURL, map[string]any{"location": "global"})
	do(t, ts, http.MethodPut, ogURL, map[string]any{"properties": map[string]any{}})

	if status, _ := do(t, ts, http.MethodDelete, profileURL(ts.URL, profile), nil); status != http.StatusOK {
		t.Fatalf("delete profile status = %d, want 200", status)
	}

	if status, _ := do(t, ts, http.MethodGet, profileURL(ts.URL, profile), nil); status != http.StatusNotFound {
		t.Errorf("profile GET after delete = %d, want 404", status)
	}

	if status, _ := do(t, ts, http.MethodGet, epURL, nil); status != http.StatusNotFound {
		t.Errorf("endpoint GET after cascade = %d, want 404", status)
	}

	if status, _ := do(t, ts, http.MethodGet, ogURL, nil); status != http.StatusNotFound {
		t.Errorf("origin group GET after cascade = %d, want 404", status)
	}
}

// TestEndpointListDeterministic proves the child list is stably ordered.
func TestEndpointListDeterministic(t *testing.T) {
	ts := newServer(t)
	createProfile(t, ts, profile)

	for _, name := range []string{"ep-c", "ep-a", "ep-b"} {
		do(t, ts, http.MethodPut, profileURL(ts.URL, profile)+"/afdEndpoints/"+name,
			map[string]any{"location": "global"})
	}

	status, body := do(t, ts, http.MethodGet, profileURL(ts.URL, profile)+"/afdEndpoints", nil)
	if status != http.StatusOK {
		t.Fatalf("list endpoints status = %d, want 200", status)
	}

	value, _ := body["value"].([]any)
	want := []string{"ep-a", "ep-b", "ep-c"}

	if len(value) != len(want) {
		t.Fatalf("list len = %d, want %d", len(value), len(want))
	}

	for i, want := range want {
		item, _ := value[i].(map[string]any)
		if item["name"] != want {
			t.Errorf("list[%d] = %v, want %q", i, item["name"], want)
		}
	}
}

// TestDeferredSubResource proves a deeper, unmodeled child path (routes) is
// rejected cleanly rather than misparsed.
func TestDeferredSubResource(t *testing.T) {
	ts := newServer(t)
	createProfile(t, ts, profile)

	status, _ := do(t, ts, http.MethodGet,
		profileURL(ts.URL, profile)+"/ruleSets/rs-1", nil)
	if status != http.StatusNotFound {
		t.Errorf("deferred sub-resource status = %d, want 404", status)
	}
}
