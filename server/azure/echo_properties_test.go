package azure_test

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

// echoTestServer wires a full Azure server (with the unmodeled-property overlay)
// over a plain-HTTP httptest server. These tests drive the wire directly with an
// http.Client rather than an SDK, so they can send properties no typed SDK model
// exposes — which is the whole point of the fidelity fix.
func echoTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()

	srv := azureserver.NewFromProvider(cloudemu.NewAzure())
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts, ts.Client()
}

func putJSON(t *testing.T, c *http.Client, url string, body map[string]any) map[string]any {
	t.Helper()

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	return doJSON(t, c, req)
}

func getJSON(t *testing.T, c *http.Client, url string) map[string]any {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	return doJSON(t, c, req)
}

func doJSON(t *testing.T, c *http.Client, req *http.Request) map[string]any {
	t.Helper()

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("%s %s: status %d: %s", req.Method, req.URL.Path, resp.StatusCode, data)
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode %s: %v (%s)", req.URL.Path, err, data)
	}

	return out
}

func props(t *testing.T, resource map[string]any) map[string]any {
	t.Helper()

	p, ok := resource["properties"].(map[string]any)
	if !ok {
		t.Fatalf("resource has no properties object: %v", resource)
	}

	return p
}

// TestEchoUnmodeledPropertiesRoundTrip is the load-bearing fidelity test: an
// unmodeled top-level property and an unmodeled leaf nested under a modeled
// parent both survive the create response and a later GET, while the modeled
// fields the handler owns remain authoritative.
func TestEchoUnmodeledPropertiesRoundTrip(t *testing.T) {
	ts, c := echoTestServer(t)
	url := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/vm1?api-version=2023-07-01"

	created := putJSON(t, c, url, map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"hardwareProfile": map[string]any{
				"vmSize":           "Standard_D2s_v5",
				"vmSizeProperties": map[string]any{"vCPUsAvailable": float64(2)},
			},
			"evictionPolicy": "Deallocate",
		},
	})

	cp := props(t, created)
	if cp["evictionPolicy"] != "Deallocate" {
		t.Errorf("create response dropped evictionPolicy: %v", cp["evictionPolicy"])
	}

	// A create is a long-running op: the handler's authoritative create value
	// is "Creating" (it settles to "Succeeded" on a later GET).
	if cp["provisioningState"] != "Creating" {
		t.Errorf("create response lost modeled provisioningState: %v", cp["provisioningState"])
	}

	got := getJSON(t, c, url)
	gp := props(t, got)

	if gp["evictionPolicy"] != "Deallocate" {
		t.Errorf("GET dropped unmodeled evictionPolicy: %v", gp["evictionPolicy"])
	}

	hw, ok := gp["hardwareProfile"].(map[string]any)
	if !ok {
		t.Fatalf("GET has no hardwareProfile: %v", gp)
	}

	if hw["vmSize"] != "Standard_D2s_v5" {
		t.Errorf("GET lost modeled vmSize: %v", hw["vmSize"])
	}

	nested, ok := hw["vmSizeProperties"].(map[string]any)
	if !ok || nested["vCPUsAvailable"] != float64(2) {
		t.Errorf("GET dropped unmodeled nested vmSizeProperties: %v", hw["vmSizeProperties"])
	}
}

// TestEchoDoesNotOverrideModeledFields confirms the overlay never overwrites a
// property the handler models: a request value for a modeled field is ignored
// in favor of the driver's authoritative value.
func TestEchoDoesNotOverrideModeledFields(t *testing.T) {
	ts, c := echoTestServer(t)
	url := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/vm2?api-version=2023-07-01"

	created := putJSON(t, c, url, map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"hardwareProfile":   map[string]any{"vmSize": "Standard_D2s_v5"},
			"provisioningState": "Succeeded", // the handler owns this — must win
		},
	})

	// The handler's authoritative create value ("Creating") must win over the
	// request-supplied value rather than the overlay clobbering it.
	if got := props(t, created)["provisioningState"]; got != "Creating" {
		t.Errorf("overlay overrode modeled provisioningState: got %v, want Creating", got)
	}
}

func postJSON(t *testing.T, c *http.Client, url string) map[string]any {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	return doJSON(t, c, req)
}

// TestEchoSurvivesBodyReturningLifecycleAction is the regression for the High
// finding: a lifecycle action (POST .../stop) that returns a full resource body
// must not wipe the unmodeled properties preserved on create. Flex servers take
// this path (their stop echoes the server), unlike VMs which return a bodiless
// 202.
func TestEchoSurvivesBodyReturningLifecycleAction(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{MySQLFlex: cloudP.MySQLFlex})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	c := ts.Client()

	base := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.DBforMySQL/flexibleServers/db1"

	putJSON(t, c, base+"?api-version=2023-12-30", map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"administratorLogin": "adm",
			"version":            "8.0.21",
			// maintenanceWindow is not modeled by the flex handler; the overlay
			// must preserve it.
			"maintenanceWindow": map[string]any{"dayOfWeek": float64(3)},
		},
	})

	if props(t, getJSON(t, c, base+"?api-version=2023-12-30"))["maintenanceWindow"] == nil {
		t.Fatal("unmodeled maintenanceWindow was not preserved after create")
	}

	// Stop returns the full server body — the path that previously wiped the overlay.
	postJSON(t, c, base+"/stop?api-version=2023-12-30")

	mw := props(t, getJSON(t, c, base+"?api-version=2023-12-30"))["maintenanceWindow"]
	if mw == nil {
		t.Fatal("lifecycle action (stop) wiped the preserved maintenanceWindow")
	}

	if m, ok := mw.(map[string]any); !ok || m["dayOfWeek"] != float64(3) {
		t.Errorf("maintenanceWindow corrupted after stop: %v", mw)
	}
}

// TestEchoPartialPatchKeepsPreservedProps confirms a partial PATCH that does not
// resend an earlier unmodeled property keeps it (union, not replace).
func TestEchoPartialPatchKeepsPreservedProps(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{MySQLFlex: cloudP.MySQLFlex})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	c := ts.Client()

	base := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.DBforMySQL/flexibleServers/db2"

	putJSON(t, c, base+"?api-version=2023-12-30", map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"administratorLogin": "adm",
			"maintenanceWindow":  map[string]any{"dayOfWeek": float64(3)},
		},
	})

	// PATCH a different (also unmodeled) field, without resending maintenanceWindow.
	patch, err := http.NewRequestWithContext(context.Background(), http.MethodPatch,
		base+"?api-version=2023-12-30",
		bytesReader(t, map[string]any{"properties": map[string]any{"tags2": "x"}}))
	if err != nil {
		t.Fatal(err)
	}

	patch.Header.Set("Content-Type", "application/json")
	doJSON(t, c, patch)

	got := props(t, getJSON(t, c, base+"?api-version=2023-12-30"))
	if got["maintenanceWindow"] == nil {
		t.Error("partial PATCH dropped the earlier preserved maintenanceWindow")
	}
}

func bytesReader(t *testing.T, v map[string]any) *bytes.Reader {
	t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return bytes.NewReader(raw)
}
