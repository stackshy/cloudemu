package privatedns_test

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
	testSub    = "sub-1"
	testRG     = "rg-1"
	zoneName   = "example.internal"
	apiVersion = "?api-version=2018-09-01"
)

// newServer stands up the full Azure wire server backed by a fresh in-memory
// provider. The public DNS and VNet handlers are wired too so we prove the
// privateDnsZones resource type is not shadowed by — and does not shadow — the
// public dnsZones handler on the same Microsoft.Network provider.
func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	p := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		PrivateDNS: p.PrivateDNS,
		DNS:        p.DNS,
		Network:    p.VNet,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

// doJSON issues an ARM request and decodes the JSON response into a generic map.
func doJSON(t *testing.T, ts *httptest.Server, method, path string, body any) (int, map[string]any) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}

		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path+apiVersion, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	out := map[string]any{}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decode %s response %q: %v", path, data, err)
		}
	}

	return resp.StatusCode, out
}

func zonePath(name string) string {
	return "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/Microsoft.Network/privateDnsZones/" + name
}

func props(m map[string]any) map[string]any {
	p, _ := m["properties"].(map[string]any)

	return p
}

// TestZoneRoundTrip proves a zone create → get round-trips location=global, the
// terminal provisioningState and the auto-SOA numberOfRecordSets=1.
func TestZoneRoundTrip(t *testing.T) {
	ts := newServer(t)

	status, created := doJSON(t, ts, http.MethodPut, zonePath(zoneName), map[string]any{
		"location": "Global",
		"tags":     map[string]any{"env": "prod"},
	})
	if status != http.StatusCreated {
		t.Fatalf("PUT zone status = %d, want 201", status)
	}

	assertZone(t, created)

	status, got := doJSON(t, ts, http.MethodGet, zonePath(zoneName), nil)
	if status != http.StatusOK {
		t.Fatalf("GET zone status = %d, want 200", status)
	}

	assertZone(t, got)
}

func assertZone(t *testing.T, z map[string]any) {
	t.Helper()

	if z["location"] != "global" {
		t.Errorf("location = %v, want global", z["location"])
	}

	if z["type"] != "Microsoft.Network/privateDnsZones" {
		t.Errorf("type = %v, want Microsoft.Network/privateDnsZones", z["type"])
	}

	p := props(z)
	if p["provisioningState"] != "Succeeded" {
		t.Errorf("provisioningState = %v, want Succeeded", p["provisioningState"])
	}

	if n, _ := p["numberOfRecordSets"].(float64); n != 1 {
		t.Errorf("numberOfRecordSets = %v, want 1 (auto-SOA)", p["numberOfRecordSets"])
	}
}

// TestPublicDNSStillRoutes proves a public dnsZones request is served by the
// public DNS handler (type Microsoft.Network/dnsZones), not swallowed by the
// private handler — the handlers are disjoint even case-insensitively.
func TestPublicDNSStillRoutes(t *testing.T) {
	ts := newServer(t)

	pubPath := "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/Microsoft.Network/dnsZones/public.example.com"

	status, created := doJSON(t, ts, http.MethodPut, pubPath, map[string]any{"location": "global"})
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("PUT public zone status = %d, want 200/201", status)
	}

	if created["type"] != "Microsoft.Network/dnsZones" {
		t.Errorf("public zone type = %v, want Microsoft.Network/dnsZones", created["type"])
	}
}

// TestLinkRegistrationFalseRoundTrips proves the #1 vnet-link drift: an explicit
// registrationEnabled=false is emitted as false (never swallowed), alongside the
// virtualNetwork.id, the terminal virtualNetworkLinkState and provisioningState.
func TestLinkRegistrationFalseRoundTrips(t *testing.T) {
	ts := newServer(t)
	mustCreateZone(t, ts)

	vnetID := "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/Microsoft.Network/virtualNetworks/vnet-1"
	linkPath := zonePath(zoneName) + "/virtualNetworkLinks/link-1"

	status, created := doJSON(t, ts, http.MethodPut, linkPath, map[string]any{
		"location": "Global",
		"properties": map[string]any{
			"registrationEnabled": false,
			"virtualNetwork":      map[string]any{"id": vnetID},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("PUT link status = %d, want 201", status)
	}

	assertLink(t, created, vnetID)

	_, got := doJSON(t, ts, http.MethodGet, linkPath, nil)
	assertLink(t, got, vnetID)

	// Zone now reports one linked network.
	_, zone := doJSON(t, ts, http.MethodGet, zonePath(zoneName), nil)
	if n, _ := props(zone)["numberOfVirtualNetworkLinks"].(float64); n != 1 {
		t.Errorf("numberOfVirtualNetworkLinks = %v, want 1", props(zone)["numberOfVirtualNetworkLinks"])
	}
}

func assertLink(t *testing.T, l map[string]any, vnetID string) {
	t.Helper()

	p := props(l)

	reg, ok := p["registrationEnabled"]
	if !ok {
		t.Fatal("registrationEnabled absent from response — explicit false was swallowed")
	}

	if reg != false {
		t.Errorf("registrationEnabled = %v, want false", reg)
	}

	if p["virtualNetworkLinkState"] != "Completed" {
		t.Errorf("virtualNetworkLinkState = %v, want Completed", p["virtualNetworkLinkState"])
	}

	if p["provisioningState"] != "Succeeded" {
		t.Errorf("provisioningState = %v, want Succeeded", p["provisioningState"])
	}

	vn, _ := p["virtualNetwork"].(map[string]any)
	if vn["id"] != vnetID {
		t.Errorf("virtualNetwork.id = %v, want %q", vn["id"], vnetID)
	}
}

// TestRecordRoundTrip proves an A record round-trips ttl (as an integer),
// aRecords, the computed fqdn (trailing dot) and isAutoRegistered=false.
func TestRecordRoundTrip(t *testing.T) {
	ts := newServer(t)
	mustCreateZone(t, ts)

	recPath := zonePath(zoneName) + "/A/www"
	status, created := doJSON(t, ts, http.MethodPut, recPath, map[string]any{
		"properties": map[string]any{
			"ttl":      300,
			"aRecords": []any{map[string]any{"ipv4Address": "10.0.0.1"}},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("PUT record status = %d, want 201", status)
	}

	assertARecord(t, created, "www.example.internal.")

	_, got := doJSON(t, ts, http.MethodGet, recPath, nil)
	assertARecord(t, got, "www.example.internal.")

	if got["type"] != "Microsoft.Network/privateDnsZones/A" {
		t.Errorf("record type = %v, want .../A", got["type"])
	}
}

func assertARecord(t *testing.T, r map[string]any, wantFQDN string) {
	t.Helper()

	p := props(r)

	if ttl, _ := p["ttl"].(float64); ttl != 300 {
		t.Errorf("ttl = %v, want 300", p["ttl"])
	}

	if p["fqdn"] != wantFQDN {
		t.Errorf("fqdn = %v, want %q", p["fqdn"], wantFQDN)
	}

	if p["isAutoRegistered"] != false {
		t.Errorf("isAutoRegistered = %v, want false", p["isAutoRegistered"])
	}

	arr, _ := p["aRecords"].([]any)
	if len(arr) != 1 {
		t.Fatalf("aRecords len = %d, want 1", len(arr))
	}

	rec, _ := arr[0].(map[string]any)
	if rec["ipv4Address"] != "10.0.0.1" {
		t.Errorf("aRecords[0].ipv4Address = %v, want 10.0.0.1", rec["ipv4Address"])
	}
}

// TestApexRecordFQDN proves the apex record (@) resolves to the zone name itself.
func TestApexRecordFQDN(t *testing.T) {
	ts := newServer(t)
	mustCreateZone(t, ts)

	status, created := doJSON(t, ts, http.MethodPut, zonePath(zoneName)+"/A/@", map[string]any{
		"properties": map[string]any{"ttl": 60, "aRecords": []any{map[string]any{"ipv4Address": "10.0.0.9"}}},
	})
	if status != http.StatusCreated {
		t.Fatalf("PUT apex record status = %d, want 201", status)
	}

	if props(created)["fqdn"] != "example.internal." {
		t.Errorf("apex fqdn = %v, want example.internal.", props(created)["fqdn"])
	}
}

// TestZoneDeleteCascades proves deleting a zone cascades to its records (a
// subsequent record Get 404s).
func TestZoneDeleteCascades(t *testing.T) {
	ts := newServer(t)
	mustCreateZone(t, ts)

	recPath := zonePath(zoneName) + "/A/www"
	doJSON(t, ts, http.MethodPut, recPath, map[string]any{"properties": map[string]any{"ttl": 300}})

	status, _ := doJSON(t, ts, http.MethodDelete, zonePath(zoneName), nil)
	if status != http.StatusOK {
		t.Fatalf("DELETE zone status = %d, want 200", status)
	}

	if status, _ = doJSON(t, ts, http.MethodGet, recPath, nil); status != http.StatusNotFound {
		t.Errorf("GET record after cascade = %d, want 404", status)
	}
}

// TestZoneListDeterministic proves the subscription-scoped list is stably ordered.
func TestZoneListDeterministic(t *testing.T) {
	ts := newServer(t)

	for _, name := range []string{"c.internal", "a.internal", "b.internal"} {
		doJSON(t, ts, http.MethodPut, zonePath(name), map[string]any{"location": "global"})
	}

	listPath := "/subscriptions/" + testSub + "/providers/Microsoft.Network/privateDnsZones"
	_, resp := doJSON(t, ts, http.MethodGet, listPath, nil)

	value, _ := resp["value"].([]any)

	var names []string

	for _, v := range value {
		z, _ := v.(map[string]any)
		names = append(names, z["name"].(string))
	}

	if got := strings.Join(names, ","); got != "a.internal,b.internal,c.internal" {
		t.Errorf("list order = %q, want a,b,c", got)
	}
}

func mustCreateZone(t *testing.T, ts *httptest.Server) {
	t.Helper()

	status, _ := doJSON(t, ts, http.MethodPut, zonePath(zoneName), map[string]any{"location": "global"})
	if status != http.StatusCreated {
		t.Fatalf("setup zone create status = %d, want 201", status)
	}
}
