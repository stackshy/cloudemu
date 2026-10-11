package vnet_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func rawPut(t *testing.T, ts *httptest.Server, path, body string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut,
		ts.URL+path+"?api-version=2023-09-01", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(data)
}

func TestNICBackendPoolReferenceValidated(t *testing.T) {
	p := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Network: p.VNet, LB: p.LB}))
	t.Cleanup(ts.Close)

	ensureRG(t, ts, "sub-1", "rg-1")

	base := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/"

	vnetBody := `{"location":"eastus","properties":{"addressSpace":{"addressPrefixes":["10.0.0.0/16"]},` +
		`"subnets":[{"name":"s1","properties":{"addressPrefix":"10.0.1.0/24"}}]}}`
	if code, body := rawPut(t, ts, base+"virtualNetworks/vn1", vnetBody); code/100 != 2 {
		t.Fatalf("vnet: %d %s", code, body)
	}

	if code, body := rawPut(t, ts, base+"loadBalancers/lb1", `{"location":"eastus","sku":{"name":"Standard"}}`); code/100 != 2 {
		t.Fatalf("lb: %d %s", code, body)
	}

	if code, body := rawPut(t, ts, base+"loadBalancers/lb1/backendAddressPools/pool1", `{}`); code/100 != 2 {
		t.Fatalf("pool: %d %s", code, body)
	}

	nic := func(pool string) string {
		return `{"location":"eastus","properties":{"ipConfigurations":[{"name":"ip1","properties":{` +
			`"subnet":{"id":"` + base + `virtualNetworks/vn1/subnets/s1"},` +
			`"loadBalancerBackendAddressPools":[{"id":"` + base + `loadBalancers/lb1/backendAddressPools/` + pool + `"}]}}]}}`
	}

	code, body := rawPut(t, ts, base+"networkInterfaces/nic-bad", nic("ghost"))
	if code != http.StatusBadRequest || !strings.Contains(body, "InvalidResourceReference") {
		t.Fatalf("missing pool: %d %s, want 400 InvalidResourceReference", code, body)
	}

	if code, body = rawPut(t, ts, base+"networkInterfaces/nic-ok", nic("pool1")); code/100 != 2 {
		t.Fatalf("existing pool: %d %s", code, body)
	}
}
